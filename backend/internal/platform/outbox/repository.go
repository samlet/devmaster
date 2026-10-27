package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
)

type Repository struct{}

func NewRepository() *Repository { return &Repository{} }

// Insert 在调用方的事务里写一条事件。必须与业务写入同事务，
// 否则"提交了业务但没写事件"会静默丢事实。
func (r *Repository) Insert(ctx context.Context, tx *sql.Tx, event Event) error {
	headers, err := json.Marshal(event.Headers)
	if err != nil {
		return fmt.Errorf("marshal outbox headers: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox_events (organization_id, event_id, subject, payload, headers)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (event_id) DO NOTHING`,
		event.OrganizationID, event.EventID, event.Subject, event.Payload, headers)
	if err != nil {
		return fmt.Errorf("insert outbox event %s: %w", event.Subject, err)
	}
	return nil
}

// ClaimBatch 取一批待投递事件，并把它们的下次尝试时间推后一个租约。
//
// 租约的作用是防止同一个事件被两个 relay 同时投递：拿到的事件在租约内
// 不会再被别的进程选中。投递成功后 MarkSent；失败时 MarkFailed 会按指数退避
// 重设 next_attempt_at（通常比租约更长）。
func (r *Repository) ClaimBatch(ctx context.Context, db *database.DB, limit int, lease time.Duration, maxAttempts int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.SQL.QueryContext(ctx, `
		WITH picked AS (
			SELECT id
			  FROM outbox_events
			 WHERE sent_at IS NULL
			   AND next_attempt_at <= now()
			   AND attempts < $3
			 ORDER BY id
			 LIMIT $1
			 FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox_events o
		   SET attempts = o.attempts + 1,
		       next_attempt_at = now() + make_interval(secs => $2)
		  FROM picked
		 WHERE o.id = picked.id
		RETURNING o.id, o.organization_id, o.event_id, o.subject, o.payload, o.headers,
		          o.attempts, o.next_attempt_at, o.created_at`,
		limit, lease.Seconds(), maxAttempts)
	if err != nil {
		return nil, fmt.Errorf("claim outbox batch: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var (
			event   Event
			headers []byte
		)
		if err := rows.Scan(&event.ID, &event.OrganizationID, &event.EventID, &event.Subject,
			&event.Payload, &headers, &event.Attempts, &event.NextAttemptAt, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		if len(headers) > 0 {
			if err := json.Unmarshal(headers, &event.Headers); err != nil {
				return nil, fmt.Errorf("decode outbox headers: %w", err)
			}
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *Repository) MarkSent(ctx context.Context, db *database.DB, id int64) error {
	_, err := db.SQL.ExecContext(ctx,
		`UPDATE outbox_events SET sent_at = now(), last_error = NULL WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark outbox event sent: %w", err)
	}
	return nil
}

func (r *Repository) MarkFailed(ctx context.Context, db *database.DB, id int64, cause error, backoff time.Duration) error {
	_, err := db.SQL.ExecContext(ctx, `
		UPDATE outbox_events
		   SET last_error = $2,
		       next_attempt_at = now() + make_interval(secs => $3)
		 WHERE id = $1`,
		id, cause.Error(), backoff.Seconds())
	if err != nil {
		return fmt.Errorf("mark outbox event failed: %w", err)
	}
	return nil
}

// PendingCount 供 /metrics 与告警使用。
func (r *Repository) PendingCount(ctx context.Context, db *database.DB) (int, error) {
	var count int
	err := db.SQL.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox_events WHERE sent_at IS NULL`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending outbox events: %w", err)
	}
	return count, nil
}

// FindByEventID 供测试与排障使用。
func (r *Repository) FindByEventID(ctx context.Context, db *database.DB, organizationID uuid.UUID, eventID uuid.UUID) (Event, bool, error) {
	var (
		event   Event
		headers []byte
	)
	err := db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT id, organization_id, event_id, subject, payload, headers,
			       attempts, next_attempt_at, sent_at, created_at
			  FROM outbox_events
			 WHERE organization_id = $1 AND event_id = $2`,
			organizationID, eventID).Scan(&event.ID, &event.OrganizationID, &event.EventID,
			&event.Subject, &event.Payload, &headers, &event.Attempts, &event.NextAttemptAt,
			&event.SentAt, &event.CreatedAt)
	})
	if err == sql.ErrNoRows {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, err
	}
	if len(headers) > 0 {
		_ = json.Unmarshal(headers, &event.Headers)
	}
	return event, true, nil
}
