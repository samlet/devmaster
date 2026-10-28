// Package postgres 是工时的持久化实现。
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/worklog/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/worklog/domain"
)

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

const workLogColumns = `l.id, l.organization_id, l.work_item_id, w.key, l.author_id,
	l.minutes, l.work_date, l.note, l.project_code, l.wbs_code, l.grade_code,
	l.source, l.reversal_of, l.created_at`

type Repository struct {
	db *database.DB
}

var _ application.Repository = (*Repository)(nil)

func NewRepository(db *database.DB) *Repository { return &Repository{db: db} }

func (r *Repository) ResolveWorkItem(ctx context.Context, organizationID uuid.UUID, workItemID *uuid.UUID, key string) (application.WorkItemRef, error) {
	var ref application.WorkItemRef
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		query := `SELECT w.id, w.key, p.key, w.project_code, w.wbs_code
			FROM work_items w JOIN products p ON p.id = w.product_id
			WHERE w.organization_id = $1 AND `
		args := []any{organizationID}
		if workItemID != nil {
			query += `w.id = $2`
			args = append(args, *workItemID)
		} else {
			query += `w.key = $2`
			args = append(args, key)
		}
		return tx.QueryRowContext(ctx, query, args...).Scan(
			&ref.ID, &ref.Key, &ref.ProductCode, &ref.ProjectCode, &ref.WBSCode)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return application.WorkItemRef{}, apperr.NotFound("work item not found")
	}
	if err != nil {
		return application.WorkItemRef{}, fmt.Errorf("resolve work item: %w", err)
	}
	return ref, nil
}

func (r *Repository) Insert(ctx context.Context, log domain.WorkLog, outboxEvents []outbox.Event) (domain.WorkLog, error) {
	err := r.db.InOrgTx(ctx, log.OrganizationID, func(tx *sql.Tx) error {
		if log.ReversalOf != nil {
			// 锁原记录并累计已冲正额：并发两次部分冲正也不能超过原工时。
			var originalMinutes int
			err := tx.QueryRowContext(ctx, `
				SELECT minutes FROM work_logs
				 WHERE organization_id = $1 AND id = $2 FOR UPDATE`,
				log.OrganizationID, *log.ReversalOf).Scan(&originalMinutes)
			if errors.Is(err, sql.ErrNoRows) {
				return apperr.NotFound("original work log not found")
			}
			if err != nil {
				return fmt.Errorf("lock original work log: %w", err)
			}
			var reversed int
			if err := tx.QueryRowContext(ctx, `
				SELECT coalesce(-sum(minutes), 0) FROM work_logs
				 WHERE organization_id = $1 AND reversal_of = $2`,
				log.OrganizationID, *log.ReversalOf).Scan(&reversed); err != nil {
				return fmt.Errorf("sum reversals: %w", err)
			}
			if reversed+(-log.Minutes) > originalMinutes {
				return apperr.Conflict("minutes", fmt.Sprintf(
					"reversal exceeds original: already reversed %d of %d", reversed, originalMinutes))
			}
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO work_logs (id, organization_id, work_item_id, author_id, minutes,
				work_date, note, project_code, wbs_code, grade_code, source, reversal_of, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			log.ID, log.OrganizationID, log.WorkItemID, log.AuthorID, log.Minutes,
			log.WorkDate, log.Note, log.ProjectCode, log.WBSCode, log.GradeCode,
			string(log.Source), log.ReversalOf, log.CreatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case uniqueViolation:
					return apperr.Conflict("reversal_of", "work log was already reversed")
				case foreignKeyViolation:
					return apperr.NotFound("work item not found")
				}
			}
			return fmt.Errorf("insert work log: %w", err)
		}
		for _, event := range outboxEvents {
			if err := outbox.NewRepository().Insert(ctx, tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.WorkLog{}, err
	}
	return log, nil
}

func (r *Repository) Get(ctx context.Context, organizationID, id uuid.UUID) (domain.WorkLog, error) {
	var log domain.WorkLog
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var err error
		log, err = scanWorkLog(tx.QueryRowContext(ctx, `
			SELECT `+workLogColumns+`
			  FROM work_logs l JOIN work_items w ON w.id = l.work_item_id
			 WHERE l.organization_id = $1 AND l.id = $2`, organizationID, id))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkLog{}, apperr.NotFound("work log not found")
	}
	if err != nil {
		return domain.WorkLog{}, err
	}
	return log, nil
}

func (r *Repository) List(ctx context.Context, organizationID uuid.UUID, filter application.ListFilter) ([]domain.WorkLog, int, error) {
	var (
		items []domain.WorkLog
		total int
	)
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		where := ` WHERE l.organization_id = $1`
		args := []any{organizationID}
		if filter.WorkItemID != nil {
			args = append(args, *filter.WorkItemID)
			where += fmt.Sprintf(" AND l.work_item_id = $%d", len(args))
		}
		if filter.AuthorID != nil {
			args = append(args, *filter.AuthorID)
			where += fmt.Sprintf(" AND l.author_id = $%d", len(args))
		}
		if filter.From != nil {
			args = append(args, *filter.From)
			where += fmt.Sprintf(" AND l.work_date >= $%d", len(args))
		}
		if filter.To != nil {
			args = append(args, *filter.To)
			where += fmt.Sprintf(" AND l.work_date <= $%d", len(args))
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT count(*) FROM work_logs l`+where, args...).Scan(&total); err != nil {
			return fmt.Errorf("count work logs: %w", err)
		}
		limit, offset := filter.Limit, filter.Offset
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		args = append(args, limit, offset)
		rows, err := tx.QueryContext(ctx, `
			SELECT `+workLogColumns+`
			  FROM work_logs l JOIN work_items w ON w.id = l.work_item_id`+where+fmt.Sprintf(`
			 ORDER BY l.work_date DESC, l.created_at DESC
			 LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
		if err != nil {
			return fmt.Errorf("list work logs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanWorkLog(rows)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = []domain.WorkLog{}
	}
	return items, total, nil
}

func (r *Repository) FindReversal(ctx context.Context, organizationID, originalID uuid.UUID) (domain.WorkLog, bool, error) {
	var log domain.WorkLog
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var err error
		log, err = scanWorkLog(tx.QueryRowContext(ctx, `
			SELECT `+workLogColumns+`
			  FROM work_logs l JOIN work_items w ON w.id = l.work_item_id
			 WHERE l.organization_id = $1 AND l.reversal_of = $2`, organizationID, originalID))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkLog{}, false, nil
	}
	if err != nil {
		return domain.WorkLog{}, false, err
	}
	return log, true, nil
}

func (r *Repository) Sum(ctx context.Context, organizationID uuid.UUID, filter application.ListFilter) (int, error) {
	var total int
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		where := ` WHERE organization_id = $1`
		args := []any{organizationID}
		if filter.WorkItemID != nil {
			args = append(args, *filter.WorkItemID)
			where += fmt.Sprintf(" AND work_item_id = $%d", len(args))
		}
		if filter.From != nil {
			args = append(args, *filter.From)
			where += fmt.Sprintf(" AND work_date >= $%d", len(args))
		}
		if filter.To != nil {
			args = append(args, *filter.To)
			where += fmt.Sprintf(" AND work_date <= $%d", len(args))
		}
		return tx.QueryRowContext(ctx, `SELECT coalesce(sum(minutes), 0) FROM work_logs`+where, args...).Scan(&total)
	})
	if err != nil {
		return 0, fmt.Errorf("sum work logs: %w", err)
	}
	return total, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorkLog(row rowScanner) (domain.WorkLog, error) {
	var (
		log      domain.WorkLog
		workDate time.Time
		source   string
		reversal *uuid.UUID
	)
	if err := row.Scan(&log.ID, &log.OrganizationID, &log.WorkItemID, &log.WorkItemKey,
		&log.AuthorID, &log.Minutes, &workDate, &log.Note, &log.ProjectCode, &log.WBSCode,
		&log.GradeCode, &source, &reversal, &log.CreatedAt); err != nil {
		return domain.WorkLog{}, err
	}
	log.WorkDate = workDate
	log.Source = domain.Source(source)
	log.ReversalOf = reversal
	return log, nil
}
