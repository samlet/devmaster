// Package postgres 是 Gitea 入站的持久化实现。
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/gitea/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
)

type Repository struct {
	db *database.DB
}

var _ application.Repository = (*Repository)(nil)

func NewRepository(db *database.DB) *Repository { return &Repository{db: db} }

func (r *Repository) RecordDelivery(ctx context.Context, organizationID uuid.UUID, deliveryID, eventType string, payload []byte) (bool, error) {
	hash := sha256.Sum256(payload)
	var inserted bool
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO inbox_events
				(organization_id, source_system, event_id, event_type, event_version, subject, payload, payload_sha256, status)
			VALUES ($1,'gitea',$2,$3,1,$3,$4,$5,'PROCESSED')
			ON CONFLICT (source_system, event_id) DO NOTHING`,
			organizationID, deliveryID, eventType, payload, hash[:])
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		inserted = affected > 0
		return nil
	})
	return inserted, err
}

func (r *Repository) ProductKeys(ctx context.Context, organizationID uuid.UUID) ([]string, error) {
	var keys []string
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT key FROM products WHERE organization_id = $1`, organizationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				return err
			}
			keys = append(keys, key)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("load product keys: %w", err)
	}
	return keys, nil
}

func (r *Repository) ResolveWorkItem(ctx context.Context, organizationID uuid.UUID, key string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT id FROM work_items WHERE organization_id = $1 AND key = $2`,
			organizationID, key).Scan(&id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("resolve work item %s: %w", key, err)
	}
	return id, true, nil
}

func (r *Repository) UpsertCommit(ctx context.Context, organizationID, workItemID uuid.UUID, repo, branch, sha string, committedAt time.Time, authorID string) (bool, error) {
	var inserted bool
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO work_item_commits
				(organization_id, work_item_id, repo, branch, commit_sha, committed_at, author_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (work_item_id, repo, commit_sha) DO NOTHING`,
			organizationID, workItemID, repo, branch, sha, committedAt, authorID)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		inserted = affected > 0
		return nil
	})
	return inserted, err
}

func (r *Repository) UpdatePullRequest(ctx context.Context, organizationID uuid.UUID, repo, branch string, prNo int, state string) (int, error) {
	updated := 0
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE work_item_commits SET pr_no = $1, pr_state = $2
			 WHERE organization_id = $3 AND repo = $4 AND branch = $5`,
			prNo, state, organizationID, repo, branch)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		updated = int(affected)
		return nil
	})
	return updated, err
}

func (r *Repository) Cursor(ctx context.Context, organizationID uuid.UUID, repo string) (time.Time, string, error) {
	var (
		at  *time.Time
		sha *string
	)
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT last_commit_at, last_sha FROM repo_sync_cursors
			 WHERE organization_id = $1 AND repo = $2`, organizationID, repo).Scan(&at, &sha)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, "", nil
	}
	if err != nil {
		return time.Time{}, "", fmt.Errorf("load repo cursor: %w", err)
	}
	var atValue time.Time
	if at != nil {
		atValue = *at
	}
	var shaValue string
	if sha != nil {
		shaValue = *sha
	}
	return atValue, shaValue, nil
}

func (r *Repository) AdvanceCursor(ctx context.Context, organizationID uuid.UUID, repo string, at time.Time, sha string) error {
	return r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO repo_sync_cursors (organization_id, repo, last_commit_at, last_sha, updated_at)
			VALUES ($1,$2,$3,$4,now())
			ON CONFLICT (organization_id, repo) DO UPDATE
			   SET last_commit_at = EXCLUDED.last_commit_at,
			       last_sha = EXCLUDED.last_sha,
			       updated_at = now()`,
			organizationID, repo, at, sha)
		return err
	})
}

func (r *Repository) LinkStats(ctx context.Context, organizationID uuid.UUID) (int, int, error) {
	var linked, denominator int
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		// 分子必须限定在"应有提交"的集合里，否则在研项也会计入分子，
		// 关联率会算出 200% 这种没有意义的数。
		return tx.QueryRowContext(ctx, `
			WITH expected AS (
				SELECT w.id FROM work_items w
				 WHERE w.organization_id = $1
				   AND (w.status IN ('DONE', 'CLOSED', 'VERIFIED')
				        OR EXISTS (SELECT 1 FROM work_item_commits c
				                    WHERE c.work_item_id = w.id AND c.pr_state = 'MERGED'))
			)
			SELECT
				(SELECT count(*) FROM expected e
				  WHERE EXISTS (SELECT 1 FROM work_item_commits c WHERE c.work_item_id = e.id)),
				(SELECT count(*) FROM expected)`,
			organizationID).Scan(&linked, &denominator)
	})
	if err != nil {
		return 0, 0, fmt.Errorf("load link stats: %w", err)
	}
	return linked, denominator, nil
}
