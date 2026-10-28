// Package postgres 是工作项的持久化实现。
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

const workItemColumns = `id, organization_id, key, product_id, type, status, resolution,
	title, description_md, acceptance_criteria, priority, severity, environment,
	reporter_id, assignee_id, parent_id, rank, project_code, wbs_code, external_ref,
	custom_fields, blocked_reason, reopened_count, version, created_at, updated_at,
	resolved_at, closed_at, sprint_id, story_points, estimate_minutes,
	platform, build_number, repro_steps, expected_behavior, actual_behavior,
	version_id, affects_version_id`

type Repository struct {
	db     *database.DB
	outbox *outbox.Repository
}

var _ application.Repository = (*Repository)(nil)

func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db, outbox: outbox.NewRepository()}
}

// ReserveIdentity 分配编号与 rank。
//
// 编号靠 product_counters 的行锁串行化；rank 由序号线性映射，天然唯一且单调。
// 代价是"分配后插入失败"会烧掉一个号 —— 编号不回收，这是可接受的空洞。
func (r *Repository) ReserveIdentity(ctx context.Context, organizationID, productID uuid.UUID) (string, string, error) {
	var (
		key  string
		rank string
	)
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		// 先确认产品属于本组织，再分配编号：反过来的话，跨组织调用会先
		// 把别人的计数器 +1 再返回 NotFound —— 一次不需要权限的写入。
		var productKey string
		err := tx.QueryRowContext(ctx, `
			SELECT key FROM products WHERE organization_id = $1 AND id = $2`,
			organizationID, productID).Scan(&productKey)
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.NotFound("product not found")
		}
		if err != nil {
			return fmt.Errorf("load product key: %w", err)
		}
		var sequence int64
		err = tx.QueryRowContext(ctx, `
			INSERT INTO product_counters (product_id, next_seq) VALUES ($1, 1)
			ON CONFLICT (product_id) DO UPDATE
			   SET next_seq = product_counters.next_seq + 1, updated_at = now()
			RETURNING next_seq`, productID).Scan(&sequence)
		if err != nil {
			return fmt.Errorf("allocate work item sequence: %w", err)
		}
		rank, err = domain.RankForSequence(sequence)
		if err != nil {
			return err
		}
		key = productKey + "-" + strconv.FormatInt(sequence, 10)
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return key, rank, nil
}

func (r *Repository) Create(ctx context.Context, item domain.WorkItem, event application.EventRecord, outboxEvents []outbox.Event) (domain.WorkItem, error) {
	err := r.db.InOrgTx(ctx, item.OrganizationID, func(tx *sql.Tx) error {
		if err := insertWorkItem(ctx, tx, item); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return apperr.Conflict("key", fmt.Sprintf("work item key %q already exists", item.Key))
			}
			return err
		}
		if err := insertEvent(ctx, tx, item.OrganizationID, event); err != nil {
			return err
		}
		for _, outboxEvent := range outboxEvents {
			if err := r.outbox.Insert(ctx, tx, outboxEvent); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	return item, nil
}

func insertWorkItem(ctx context.Context, tx *sql.Tx, item domain.WorkItem) error {
	customFields, err := json.Marshal(item.CustomFields)
	if err != nil {
		return fmt.Errorf("marshal custom fields: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO work_items (
			id, organization_id, key, product_id, type, status, resolution,
			title, description_md, acceptance_criteria, priority, severity, environment,
			reporter_id, assignee_id, parent_id, rank, project_code, wbs_code, external_ref,
			custom_fields, blocked_reason, reopened_count, version, created_at, updated_at,
			resolved_at, closed_at, sprint_id, story_points, estimate_minutes,
			platform, build_number, repro_steps, expected_behavior, actual_behavior,
			version_id, affects_version_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38)`,
		item.ID, item.OrganizationID, item.Key, item.ProductID, string(item.Type), string(item.Status),
		derefResolution(item.Resolution), item.Title, item.DescriptionMD, item.AcceptanceCriteria,
		int16(item.Priority), derefSeverity(item.Severity), item.Environment, item.ReporterID,
		item.AssigneeID, item.ParentID, item.Rank, item.ProjectCode, item.WBSCode, item.ExternalRef,
		customFields, item.BlockedReason, item.ReopenedCount, item.Version, item.CreatedAt,
		item.UpdatedAt, item.ResolvedAt, item.ClosedAt,
		item.SprintID, item.StoryPoints, item.EstimateMinutes,
		item.Platform, item.BuildNumber, item.ReproSteps, item.ExpectedBehavior,
		item.ActualBehavior, item.VersionID, item.AffectsVersionID)
	if err != nil {
		return fmt.Errorf("insert work item: %w", err)
	}
	return nil
}

func insertEvent(ctx context.Context, tx *sql.Tx, organizationID uuid.UUID, event application.EventRecord) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO work_item_events (
			organization_id, work_item_id, event_type, from_status, to_status, actor_id, payload, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		organizationID, event.WorkItemID, event.EventType, derefStatus(event.FromStatus),
		derefStatus(event.ToStatus), nullableString(event.ActorID), payload, event.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert work item event: %w", err)
	}
	return nil
}

func (r *Repository) GetByID(ctx context.Context, organizationID, id uuid.UUID) (domain.WorkItem, error) {
	var item domain.WorkItem
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+workItemColumns+`
			FROM work_items WHERE organization_id = $1 AND id = $2`, organizationID, id)
		var err error
		item, err = scanWorkItem(row)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkItem{}, apperr.NotFound("work item not found")
	}
	if err != nil {
		return domain.WorkItem{}, err
	}
	return item, nil
}

func (r *Repository) GetByKey(ctx context.Context, organizationID uuid.UUID, key string) (domain.WorkItem, error) {
	var item domain.WorkItem
	normalized := strings.ToUpper(strings.TrimSpace(key))
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+workItemColumns+`
			FROM work_items WHERE organization_id = $1 AND key = $2`, organizationID, normalized)
		var err error
		item, err = scanWorkItem(row)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkItem{}, apperr.NotFound(fmt.Sprintf("work item %q not found", normalized))
	}
	if err != nil {
		return domain.WorkItem{}, err
	}
	return item, nil
}

func (r *Repository) List(ctx context.Context, organizationID uuid.UUID, filter application.ListFilter) ([]domain.WorkItem, int, error) {
	var (
		items []domain.WorkItem
		total int
	)
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		where := ` WHERE organization_id = $1`
		args := []any{organizationID}
		if filter.ProductID != nil {
			args = append(args, *filter.ProductID)
			where += fmt.Sprintf(" AND product_id = $%d", len(args))
		}
		if filter.Type != "" {
			args = append(args, string(filter.Type))
			where += fmt.Sprintf(" AND type = $%d", len(args))
		}
		if len(filter.Statuses) > 0 {
			placeholders := make([]string, 0, len(filter.Statuses))
			for _, status := range filter.Statuses {
				args = append(args, string(status))
				placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
			}
			where += " AND status IN (" + strings.Join(placeholders, ",") + ")"
		}
		if filter.AssigneeID != nil {
			args = append(args, strings.TrimSpace(*filter.AssigneeID))
			where += fmt.Sprintf(" AND assignee_id = $%d", len(args))
		}
		if filter.SprintID != nil {
			args = append(args, *filter.SprintID)
			where += fmt.Sprintf(" AND sprint_id = $%d", len(args))
		}
		if query := strings.TrimSpace(filter.Query); query != "" {
			args = append(args, "%"+query+"%")
			where += fmt.Sprintf(" AND (title ILIKE $%d OR description_md ILIKE $%d)", len(args), len(args))
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM work_items`+where, args...).Scan(&total); err != nil {
			return fmt.Errorf("count work items: %w", err)
		}
		limit, offset := filter.Limit, filter.Offset
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		args = append(args, limit, offset)
		rows, err := tx.QueryContext(ctx, `SELECT `+workItemColumns+` FROM work_items`+where+fmt.Sprintf(`
			ORDER BY rank, key
			LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
		if err != nil {
			return fmt.Errorf("list work items: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanWorkItem(rows)
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
		items = []domain.WorkItem{}
	}
	return items, total, nil
}

func (r *Repository) Update(ctx context.Context, item domain.WorkItem, expectedVersion int, events []application.EventRecord, outboxEvents []outbox.Event) (domain.WorkItem, error) {
	err := r.db.InOrgTx(ctx, item.OrganizationID, func(tx *sql.Tx) error {
		if err := updateWorkItem(ctx, tx, item, expectedVersion); err != nil {
			return err
		}
		for _, event := range events {
			if err := insertEvent(ctx, tx, item.OrganizationID, event); err != nil {
				return err
			}
		}
		for _, outboxEvent := range outboxEvents {
			if err := r.outbox.Insert(ctx, tx, outboxEvent); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	return item, nil
}

func updateWorkItem(ctx context.Context, tx *sql.Tx, item domain.WorkItem, expectedVersion int) error {
	customFields, err := json.Marshal(item.CustomFields)
	if err != nil {
		return fmt.Errorf("marshal custom fields: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE work_items SET
			status = $1, resolution = $2, title = $3, description_md = $4,
			acceptance_criteria = $5, priority = $6, severity = $7, environment = $8,
			assignee_id = $9, rank = $10, project_code = $11, wbs_code = $12,
			external_ref = $13, custom_fields = $14, blocked_reason = $15,
			reopened_count = $16, version = $17, updated_at = $18,
			resolved_at = $19, closed_at = $20,
			sprint_id = $21, story_points = $22, estimate_minutes = $23,
			platform = $24, build_number = $25, repro_steps = $26,
			expected_behavior = $27, actual_behavior = $28,
			version_id = $29, affects_version_id = $30
		WHERE organization_id = $31 AND id = $32 AND version = $33`,
		string(item.Status), derefResolution(item.Resolution), item.Title, item.DescriptionMD,
		item.AcceptanceCriteria, int16(item.Priority), derefSeverity(item.Severity), item.Environment,
		item.AssigneeID, item.Rank, item.ProjectCode, item.WBSCode, item.ExternalRef,
		customFields, item.BlockedReason, item.ReopenedCount, item.Version, item.UpdatedAt,
		item.ResolvedAt, item.ClosedAt, item.SprintID, item.StoryPoints, item.EstimateMinutes,
		item.Platform, item.BuildNumber, item.ReproSteps, item.ExpectedBehavior,
		item.ActualBehavior, item.VersionID, item.AffectsVersionID,
		item.OrganizationID, item.ID, expectedVersion)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return apperr.NotFound("work item not found")
		}
		return fmt.Errorf("update work item: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return apperr.Conflict("version", "work item was modified by someone else; reload and retry")
	}
	return nil
}

// Move 在行锁内计算 rank，保证并发拖拽不会得到相同的排序值。
//
// 锁的粒度是"产品"而不是"列"：rank 在产品内全局有序，按列加锁会让
// 跨列的相对顺序在重平衡后错乱。
func (r *Repository) Move(ctx context.Context, item domain.WorkItem, expectedVersion int, prevID, nextID *uuid.UUID, event application.EventRecord, outboxEvents []outbox.Event) (domain.WorkItem, error) {
	err := r.db.InOrgTx(ctx, item.OrganizationID, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
			item.ProductID.String()); err != nil {
			return fmt.Errorf("lock product for board move: %w", err)
		}
		rank, err := placeRank(ctx, tx, item, prevID, nextID)
		if err != nil {
			return err
		}
		item.Rank = rank
		if err := updateWorkItem(ctx, tx, item, expectedVersion); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, item.OrganizationID, event); err != nil {
			return err
		}
		for _, outboxEvent := range outboxEvents {
			if err := r.outbox.Insert(ctx, tx, outboxEvent); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	return item, nil
}

func placeRank(ctx context.Context, tx *sql.Tx, item domain.WorkItem, prevID, nextID *uuid.UUID) (string, error) {
	prevRank, err := neighborRank(ctx, tx, item, prevID)
	if err != nil {
		return "", err
	}
	nextRank, err := neighborRank(ctx, tx, item, nextID)
	if err != nil {
		return "", err
	}
	if prevID == nil && nextID == nil {
		var maxRank string
		err := tx.QueryRowContext(ctx, `
			SELECT coalesce(max(rank), '') FROM work_items
			 WHERE organization_id = $1 AND product_id = $2 AND id <> $3`,
			item.OrganizationID, item.ProductID, item.ID).Scan(&maxRank)
		if err != nil {
			return "", fmt.Errorf("load max rank: %w", err)
		}
		return domain.RankAfter(maxRank)
	}
	rank, err := domain.RankBetween(prevRank, nextRank)
	if !errors.Is(err, domain.ErrRankExhausted) {
		return rank, err
	}
	// 空间耗尽：重排整个产品后再算一次。重排保持相对顺序，不会把卡片弹到别处。
	if err := rebalanceProduct(ctx, tx, item.OrganizationID, item.ProductID); err != nil {
		return "", err
	}
	if prevRank, err = neighborRank(ctx, tx, item, prevID); err != nil {
		return "", err
	}
	if nextRank, err = neighborRank(ctx, tx, item, nextID); err != nil {
		return "", err
	}
	return domain.RankBetween(prevRank, nextRank)
}

func neighborRank(ctx context.Context, tx *sql.Tx, item domain.WorkItem, id *uuid.UUID) (string, error) {
	if id == nil {
		return "", nil
	}
	var rank string
	err := tx.QueryRowContext(ctx, `
		SELECT rank FROM work_items
		 WHERE organization_id = $1 AND product_id = $2 AND id = $3`,
		item.OrganizationID, item.ProductID, *id).Scan(&rank)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apperr.Invalid("neighbor_id", "neighbour work item not found in this product")
	}
	if err != nil {
		return "", fmt.Errorf("load neighbour rank: %w", err)
	}
	return rank, nil
}

func rebalanceProduct(ctx context.Context, tx *sql.Tx, organizationID, productID uuid.UUID) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM work_items
		 WHERE organization_id = $1 AND product_id = $2
		 ORDER BY rank, key`, organizationID, productID)
	if err != nil {
		return fmt.Errorf("list items for rebalance: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan rebalance id: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for index, id := range ids {
		rank, err := domain.RankForSequence(int64(index + 1))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE work_items SET rank = $2 WHERE id = $1`, id, rank); err != nil {
			return fmt.Errorf("rebalance rank: %w", err)
		}
	}
	return nil
}

func (r *Repository) AddComment(ctx context.Context, comment domain.Comment, event application.EventRecord) (domain.Comment, error) {
	err := r.db.InOrgTx(ctx, comment.OrganizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO comments (id, organization_id, work_item_id, author_id, body_md, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			comment.ID, comment.OrganizationID, comment.WorkItemID, comment.AuthorID,
			comment.BodyMD, comment.CreatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
				return apperr.NotFound("work item not found")
			}
			return fmt.Errorf("insert comment: %w", err)
		}
		return insertEvent(ctx, tx, comment.OrganizationID, event)
	})
	if err != nil {
		return domain.Comment{}, err
	}
	return comment, nil
}

func (r *Repository) ListComments(ctx context.Context, organizationID, workItemID uuid.UUID) ([]domain.Comment, error) {
	var items []domain.Comment
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, organization_id, work_item_id, author_id, body_md, created_at, edited_at
			  FROM comments
			 WHERE organization_id = $1 AND work_item_id = $2
			 ORDER BY created_at`, organizationID, workItemID)
		if err != nil {
			return fmt.Errorf("list comments: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var item domain.Comment
			if err := rows.Scan(&item.ID, &item.OrganizationID, &item.WorkItemID, &item.AuthorID,
				&item.BodyMD, &item.CreatedAt, &item.EditedAt); err != nil {
				return fmt.Errorf("scan comment: %w", err)
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.Comment{}
	}
	return items, nil
}

func (r *Repository) AddLink(ctx context.Context, link domain.Link, event application.EventRecord) (domain.Link, error) {
	err := r.db.InOrgTx(ctx, link.OrganizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO work_item_links (id, organization_id, from_id, to_id, link_type, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			link.ID, link.OrganizationID, link.FromID, link.ToID, string(link.Type),
			nullableString(link.CreatedBy), link.CreatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				switch pgErr.Code {
				case uniqueViolation:
					return apperr.Conflict("link", "link already exists")
				case foreignKeyViolation:
					return apperr.NotFound("work item not found")
				}
			}
			return fmt.Errorf("insert link: %w", err)
		}
		return insertEvent(ctx, tx, link.OrganizationID, event)
	})
	if err != nil {
		return domain.Link{}, err
	}
	return link, nil
}

func (r *Repository) RemoveLink(ctx context.Context, organizationID, fromID, toID uuid.UUID, linkType domain.LinkType, event application.EventRecord) error {
	return r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM work_item_links
			 WHERE organization_id = $1 AND from_id = $2 AND to_id = $3 AND link_type = $4`,
			organizationID, fromID, toID, string(linkType))
		if err != nil {
			return fmt.Errorf("delete link: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return apperr.NotFound("link not found")
		}
		return insertEvent(ctx, tx, organizationID, event)
	})
}

func (r *Repository) ListLinks(ctx context.Context, organizationID, workItemID uuid.UUID) ([]domain.Link, error) {
	var items []domain.Link
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, organization_id, from_id, to_id, link_type, created_by, created_at
			  FROM work_item_links
			 WHERE organization_id = $1 AND (from_id = $2 OR to_id = $2)
			 ORDER BY created_at`, organizationID, workItemID)
		if err != nil {
			return fmt.Errorf("list links: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				item      domain.Link
				linkType  string
				createdBy *string
			)
			if err := rows.Scan(&item.ID, &item.OrganizationID, &item.FromID, &item.ToID,
				&linkType, &createdBy, &item.CreatedAt); err != nil {
				return fmt.Errorf("scan link: %w", err)
			}
			item.Type = domain.LinkType(linkType)
			if createdBy != nil {
				item.CreatedBy = *createdBy
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.Link{}
	}
	return items, nil
}

func (r *Repository) ListEvents(ctx context.Context, organizationID, workItemID uuid.UUID) ([]application.EventRecord, error) {
	var items []application.EventRecord
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT work_item_id, event_type, from_status, to_status, actor_id, payload, occurred_at
			  FROM work_item_events
			 WHERE organization_id = $1 AND work_item_id = $2
			 ORDER BY id`, organizationID, workItemID)
		if err != nil {
			return fmt.Errorf("list work item events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				event      application.EventRecord
				fromStatus *string
				toStatus   *string
				actorID    *string
				payload    []byte
			)
			if err := rows.Scan(&event.WorkItemID, &event.EventType, &fromStatus, &toStatus,
				&actorID, &payload, &event.OccurredAt); err != nil {
				return fmt.Errorf("scan work item event: %w", err)
			}
			if fromStatus != nil {
				status := domain.Status(*fromStatus)
				event.FromStatus = &status
			}
			if toStatus != nil {
				status := domain.Status(*toStatus)
				event.ToStatus = &status
			}
			if actorID != nil {
				event.ActorID = *actorID
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &event.Payload); err != nil {
					return fmt.Errorf("decode event payload: %w", err)
				}
			}
			items = append(items, event)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []application.EventRecord{}
	}
	return items, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorkItem(row rowScanner) (domain.WorkItem, error) {
	var (
		item          domain.WorkItem
		itemType      string
		status        string
		resolution    *string
		acceptance    *string
		priority      int16
		severity      *string
		environment   *string
		assignee      *string
		parentID      *uuid.UUID
		sprintID      *uuid.UUID
		storyPoints   *int16
		estimateMin   *int32
		platform      *string
		buildNumber   *string
		reproSteps    *string
		expected      *string
		actual        *string
		versionID     *uuid.UUID
		affectsID     *uuid.UUID
		projectCode   *string
		wbsCode       *string
		externalRef   *string
		customFields  []byte
		blockedReason *string
		reopenedCount int16
	)
	if err := row.Scan(
		&item.ID, &item.OrganizationID, &item.Key, &item.ProductID, &itemType, &status,
		&resolution, &item.Title, &item.DescriptionMD, &acceptance, &priority, &severity,
		&environment, &item.ReporterID, &assignee, &parentID, &item.Rank, &projectCode,
		&wbsCode, &externalRef, &customFields, &blockedReason, &reopenedCount,
		&item.Version, &item.CreatedAt, &item.UpdatedAt, &item.ResolvedAt, &item.ClosedAt,
		&sprintID, &storyPoints, &estimateMin,
		&platform, &buildNumber, &reproSteps, &expected, &actual, &versionID, &affectsID,
	); err != nil {
		return domain.WorkItem{}, err
	}
	item.Type = domain.Type(itemType)
	item.Status = domain.Status(status)
	if resolution != nil {
		value := domain.Resolution(*resolution)
		item.Resolution = &value
	}
	if severity != nil {
		value := domain.Severity(*severity)
		item.Severity = &value
	}
	item.AcceptanceCriteria = acceptance
	item.Priority = domain.Priority(priority)
	item.Environment = environment
	item.AssigneeID = assignee
	item.ParentID = parentID
	item.SprintID = sprintID
	item.StoryPoints = storyPoints
	item.EstimateMinutes = estimateMin
	item.Platform = platform
	item.BuildNumber = buildNumber
	item.ReproSteps = reproSteps
	item.ExpectedBehavior = expected
	item.ActualBehavior = actual
	item.VersionID = versionID
	item.AffectsVersionID = affectsID
	item.ProjectCode = projectCode
	item.WBSCode = wbsCode
	item.ExternalRef = externalRef
	item.BlockedReason = blockedReason
	item.ReopenedCount = reopenedCount
	item.CustomFields = map[string]any{}
	if len(customFields) > 0 {
		if err := json.Unmarshal(customFields, &item.CustomFields); err != nil {
			return domain.WorkItem{}, fmt.Errorf("decode custom fields: %w", err)
		}
	}
	return item, nil
}

func derefResolution(value *domain.Resolution) *string {
	if value == nil {
		return nil
	}
	text := string(*value)
	return &text
}

func derefSeverity(value *domain.Severity) *string {
	if value == nil {
		return nil
	}
	text := string(*value)
	return &text
}

func derefStatus(value *domain.Status) *string {
	if value == nil {
		return nil
	}
	text := string(*value)
	return &text
}

func nullableString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// FindByExternalRef 按外部引用查工作项，供导入器做幂等。
// 不在 application.Repository 接口里：这是导入/对账专用读法，业务用例不需要。
func (r *Repository) FindByExternalRef(ctx context.Context, organizationID uuid.UUID, externalRef string) (domain.WorkItem, bool, error) {
	var item domain.WorkItem
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+workItemColumns+`
			FROM work_items WHERE organization_id = $1 AND external_ref = $2`,
			organizationID, externalRef)
		var err error
		item, err = scanWorkItem(row)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkItem{}, false, nil
	}
	if err != nil {
		return domain.WorkItem{}, false, err
	}
	return item, true, nil
}
