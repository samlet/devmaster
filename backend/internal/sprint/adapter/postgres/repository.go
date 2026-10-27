// Package postgres 是冲刺与看板的持久化实现。
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/sprint/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/sprint/domain"
)

const uniqueViolation = "23505"

const sprintColumns = `id, organization_id, board_id, name, goal, state, start_at, end_at,
	capacity_points, committed_points, version, created_at, updated_at, closed_at`

// sprintColumnsQualified 用于带 join 的查询：id 在 sprints 与 boards 都存在，不限定就歧义。
const sprintColumnsQualified = `s.id, s.organization_id, s.board_id, s.name, s.goal, s.state,
	s.start_at, s.end_at, s.capacity_points, s.committed_points, s.version,
	s.created_at, s.updated_at, s.closed_at`

const doneStatuses = `('DONE','VERIFIED','CLOSED','WONTFIX')`

type Repository struct {
	db *database.DB
}

var _ application.Repository = (*Repository)(nil)

func NewRepository(db *database.DB) *Repository { return &Repository{db: db} }

func (r *Repository) EnsureDefaultBoard(ctx context.Context, organizationID, productID uuid.UUID) (uuid.UUID, error) {
	var boardID uuid.UUID
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM products WHERE organization_id = $1 AND id = $2)`,
			organizationID, productID).Scan(&exists); err != nil {
			return fmt.Errorf("check product: %w", err)
		}
		if !exists {
			return apperr.NotFound("product not found")
		}
		return tx.QueryRowContext(ctx, `
			INSERT INTO boards (organization_id, product_id, name) VALUES ($1, $2, '默认看板')
			ON CONFLICT (product_id, name) DO UPDATE SET name = EXCLUDED.name
			RETURNING id`, organizationID, productID).Scan(&boardID)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return boardID, nil
}

func (r *Repository) CreateSprint(ctx context.Context, sprint domain.Sprint, outboxEvents []outbox.Event) (domain.Sprint, error) {
	err := r.db.InOrgTx(ctx, sprint.OrganizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO sprints (id, organization_id, board_id, name, goal, state, start_at, end_at,
				capacity_points, committed_points, version, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			sprint.ID, sprint.OrganizationID, sprint.BoardID, sprint.Name, sprint.Goal,
			string(sprint.State), sprint.StartAt, sprint.EndAt, sprint.CapacityPoints,
			sprint.CommittedPoints, sprint.Version, sprint.CreatedAt, sprint.UpdatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return apperr.Conflict("name", fmt.Sprintf("sprint %q already exists on this board", sprint.Name))
			}
			return fmt.Errorf("insert sprint: %w", err)
		}
		return insertOutbox(ctx, tx, outboxEvents)
	})
	if err != nil {
		return domain.Sprint{}, err
	}
	return sprint, nil
}

func (r *Repository) GetSprint(ctx context.Context, organizationID, id uuid.UUID) (domain.Sprint, error) {
	var sprint domain.Sprint
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var err error
		sprint, err = scanSprint(tx.QueryRowContext(ctx,
			`SELECT `+sprintColumns+` FROM sprints WHERE organization_id = $1 AND id = $2`,
			organizationID, id))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Sprint{}, apperr.NotFound("sprint not found")
	}
	if err != nil {
		return domain.Sprint{}, err
	}
	return sprint, nil
}

func (r *Repository) ListSprints(ctx context.Context, organizationID, productID uuid.UUID) ([]domain.Sprint, error) {
	var items []domain.Sprint
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT `+sprintColumnsQualified+`
			  FROM sprints s
			  JOIN boards b ON b.id = s.board_id
			 WHERE s.organization_id = $1 AND b.product_id = $2
			 ORDER BY s.created_at DESC`, organizationID, productID)
		if err != nil {
			return fmt.Errorf("list sprints: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			sprint, err := scanSprint(rows)
			if err != nil {
				return err
			}
			items = append(items, sprint)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.Sprint{}
	}
	return items, nil
}

func (r *Repository) ActiveSprint(ctx context.Context, organizationID, productID uuid.UUID) (domain.Sprint, bool, error) {
	var sprint domain.Sprint
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		var err error
		sprint, err = scanSprint(tx.QueryRowContext(ctx, `
			SELECT `+sprintColumnsQualified+`
			  FROM sprints s
			  JOIN boards b ON b.id = s.board_id
			 WHERE s.organization_id = $1 AND b.product_id = $2 AND s.state = 'ACTIVE'`,
			organizationID, productID))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Sprint{}, false, nil
	}
	if err != nil {
		return domain.Sprint{}, false, err
	}
	return sprint, true, nil
}

func (r *Repository) UpdateSprint(ctx context.Context, sprint domain.Sprint, expectedVersion int) (domain.Sprint, error) {
	err := r.db.InOrgTx(ctx, sprint.OrganizationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE sprints SET name = $1, goal = $2, start_at = $3, end_at = $4,
				capacity_points = $5, state = $6, committed_points = $7, version = $8,
				updated_at = $9, closed_at = $10
			 WHERE organization_id = $11 AND id = $12 AND version = $13`,
			sprint.Name, sprint.Goal, sprint.StartAt, sprint.EndAt, sprint.CapacityPoints,
			string(sprint.State), sprint.CommittedPoints, sprint.Version, sprint.UpdatedAt,
			sprint.ClosedAt, sprint.OrganizationID, sprint.ID, expectedVersion)
		if err != nil {
			return fmt.Errorf("update sprint: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return apperr.Conflict("version", "sprint was modified by someone else; reload and retry")
		}
		return nil
	})
	if err != nil {
		return domain.Sprint{}, err
	}
	return sprint, nil
}

// StartSprint 冻结承诺：把当前成员的点数求和写进 committed_points，并逐条记 COMMITTED。
func (r *Repository) StartSprint(ctx context.Context, sprint domain.Sprint, expectedVersion int, actorID string, outboxEvents []outbox.Event) (domain.Sprint, int16, error) {
	var committed int16
	err := r.db.InOrgTx(ctx, sprint.OrganizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, coalesce(story_points, 0) FROM work_items
			 WHERE organization_id = $1 AND sprint_id = $2`, sprint.OrganizationID, sprint.ID)
		if err != nil {
			return fmt.Errorf("list sprint items: %w", err)
		}
		type item struct {
			id     uuid.UUID
			points int16
		}
		var items []item
		for rows.Next() {
			var current item
			if err := rows.Scan(&current.id, &current.points); err != nil {
				rows.Close()
				return fmt.Errorf("scan sprint item: %w", err)
			}
			items = append(items, current)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, current := range items {
			committed += current.points
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO sprint_scope_events
					(organization_id, sprint_id, work_item_id, action, points_at_event, actor_id)
				VALUES ($1,$2,$3,'COMMITTED',$4,$5)
				ON CONFLICT DO NOTHING`,
				sprint.OrganizationID, sprint.ID, current.id, current.points, actorID); err != nil {
				return fmt.Errorf("insert committed event: %w", err)
			}
		}
		sprint.CommittedPoints = committed
		result, err := tx.ExecContext(ctx, `
			UPDATE sprints SET state = 'ACTIVE', committed_points = $1, start_at = $2,
				end_at = $3, version = $4, updated_at = $5
			 WHERE organization_id = $6 AND id = $7 AND version = $8`,
			committed, sprint.StartAt, sprint.EndAt, sprint.Version, sprint.UpdatedAt,
			sprint.OrganizationID, sprint.ID, expectedVersion)
		if err != nil {
			return fmt.Errorf("activate sprint: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return apperr.Conflict("version", "sprint was modified by someone else; reload and retry")
		}
		return insertOutbox(ctx, tx, outboxEvents)
	})
	if err != nil {
		return domain.Sprint{}, 0, err
	}
	return sprint, committed, nil
}

// CloseSprint 关闭冲刺：未完成项按 moveTo 决定去留（nil 表示回 backlog）。
func (r *Repository) CloseSprint(ctx context.Context, sprint domain.Sprint, expectedVersion int, moveTo *uuid.UUID, actorID string, outboxEvents []outbox.Event) (domain.Sprint, error) {
	err := r.db.InOrgTx(ctx, sprint.OrganizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, coalesce(story_points, 0) FROM work_items
			 WHERE organization_id = $1 AND sprint_id = $2
			   AND status NOT IN `+doneStatuses, sprint.OrganizationID, sprint.ID)
		if err != nil {
			return fmt.Errorf("list incomplete sprint items: %w", err)
		}
		type item struct {
			id     uuid.UUID
			points int16
		}
		var items []item
		for rows.Next() {
			var current item
			if err := rows.Scan(&current.id, &current.points); err != nil {
				rows.Close()
				return fmt.Errorf("scan incomplete item: %w", err)
			}
			items = append(items, current)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, current := range items {
			if _, err := tx.ExecContext(ctx, `
				UPDATE work_items SET sprint_id = $2, updated_at = now(), version = version + 1
				 WHERE organization_id = $1 AND id = $3`,
				sprint.OrganizationID, moveTo, current.id); err != nil {
				return fmt.Errorf("move incomplete item: %w", err)
			}
			// 结转不写 REMOVED：结转不是"范围被砍"，写进去会让蔓延率变成负数。
			// 它的事实留在 work_item_events 里（sprint_id 变更），燃尽快照记录剩余量。
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO work_item_events
					(organization_id, work_item_id, event_type, actor_id, payload)
				VALUES ($1,$2,'updated',$3,'{"fields":["sprint_id"],"reason":"sprint_closed"}')`,
				sprint.OrganizationID, current.id, actorID); err != nil {
				return fmt.Errorf("insert carry-over audit event: %w", err)
			}
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE sprints SET state = 'CLOSED', closed_at = $1, version = $2, updated_at = $1
			 WHERE organization_id = $3 AND id = $4 AND version = $5`,
			sprint.UpdatedAt, sprint.Version, sprint.OrganizationID, sprint.ID, expectedVersion)
		if err != nil {
			return fmt.Errorf("close sprint: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return apperr.Conflict("version", "sprint was modified by someone else; reload and retry")
		}
		return nil
	})
	if err != nil {
		return domain.Sprint{}, err
	}
	return sprint, nil
}

func (r *Repository) AddItems(ctx context.Context, sprint domain.Sprint, itemIDs []uuid.UUID, actorID string, outboxEvents []outbox.Event) (int, error) {
	added := 0
	err := r.db.InOrgTx(ctx, sprint.OrganizationID, func(tx *sql.Tx) error {
		for _, itemID := range itemIDs {
			var (
				points    int16
				oldStatus string
			)
			// 进入冲刺即进入待办：BACKLOG 不在看板上，留在 BACKLOG 会让卡片"消失"。
			err := tx.QueryRowContext(ctx, `
				WITH target AS (
					SELECT id, status FROM work_items
					 WHERE organization_id = $1 AND id = $3
					   AND (sprint_id IS NULL OR sprint_id <> $2)
					 FOR UPDATE
				), updated AS (
					UPDATE work_items w
					   SET sprint_id = $2,
					       status = CASE WHEN w.status = 'BACKLOG' THEN 'TODO' ELSE w.status END,
					       updated_at = now(), version = w.version + 1
					  FROM target t
					 WHERE w.id = t.id
					RETURNING w.id, coalesce(w.story_points, 0) AS points
				)
				SELECT t.status, u.points FROM updated u JOIN target t ON t.id = u.id`,
				sprint.OrganizationID, sprint.ID, itemID).Scan(&oldStatus, &points)
			if errors.Is(err, sql.ErrNoRows) {
				continue // 已在该冲刺或不存在：幂等跳过
			}
			if err != nil {
				return fmt.Errorf("add item to sprint: %w", err)
			}
			if oldStatus == "BACKLOG" {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO work_item_events
						(organization_id, work_item_id, event_type, from_status, to_status, actor_id, payload)
					VALUES ($1,$2,'transitioned','BACKLOG','TODO',$3,'{"reason":"sprint_added"}')`,
					sprint.OrganizationID, itemID, actorID); err != nil {
					return fmt.Errorf("insert sprint-added transition event: %w", err)
				}
			}
			if sprint.State == domain.StateActive {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO sprint_scope_events
						(organization_id, sprint_id, work_item_id, action, points_at_event, actor_id)
					VALUES ($1,$2,$3,'ADDED',$4,$5)`,
					sprint.OrganizationID, sprint.ID, itemID, points, actorID); err != nil {
					return fmt.Errorf("insert added event: %w", err)
				}
			}
			added++
		}
		return insertOutbox(ctx, tx, outboxEvents)
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

func (r *Repository) RemoveItems(ctx context.Context, sprint domain.Sprint, itemIDs []uuid.UUID, actorID string, outboxEvents []outbox.Event) (int, error) {
	removed := 0
	err := r.db.InOrgTx(ctx, sprint.OrganizationID, func(tx *sql.Tx) error {
		for _, itemID := range itemIDs {
			var (
				points    int16
				oldStatus string
			)
			// 从冲刺移除即回 backlog：TODO 状态的卡片回到 BACKLOG，其他状态保持原样
			// （进行中/已完成是历史事实，不因为离开冲刺而回退）。
			err := tx.QueryRowContext(ctx, `
				WITH target AS (
					SELECT id, status FROM work_items
					 WHERE organization_id = $1 AND sprint_id = $2 AND id = $3
					 FOR UPDATE
				), updated AS (
					UPDATE work_items w
					   SET sprint_id = NULL,
					       status = CASE WHEN w.status = 'TODO' THEN 'BACKLOG' ELSE w.status END,
					       updated_at = now(), version = w.version + 1
					  FROM target t
					 WHERE w.id = t.id
					RETURNING w.id, coalesce(w.story_points, 0) AS points
				)
				SELECT t.status, u.points FROM updated u JOIN target t ON t.id = u.id`,
				sprint.OrganizationID, sprint.ID, itemID).Scan(&oldStatus, &points)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("remove item from sprint: %w", err)
			}
			if oldStatus == "TODO" {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO work_item_events
						(organization_id, work_item_id, event_type, from_status, to_status, actor_id, payload)
					VALUES ($1,$2,'transitioned','TODO','BACKLOG',$3,'{"reason":"sprint_removed"}')`,
					sprint.OrganizationID, itemID, actorID); err != nil {
					return fmt.Errorf("insert sprint-removed transition event: %w", err)
				}
			}
			if sprint.State == domain.StateActive {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO sprint_scope_events
						(organization_id, sprint_id, work_item_id, action, points_at_event, actor_id)
					VALUES ($1,$2,$3,'REMOVED',$4,$5)`,
					sprint.OrganizationID, sprint.ID, itemID, points, actorID); err != nil {
					return fmt.Errorf("insert removed event: %w", err)
				}
			}
			removed++
		}
		return insertOutbox(ctx, tx, outboxEvents)
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

func (r *Repository) ScopeStats(ctx context.Context, organizationID, sprintID uuid.UUID) (domain.ScopeStats, error) {
	var stats domain.ScopeStats
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT
				coalesce(sum(points_at_event) FILTER (WHERE action = 'COMMITTED'), 0),
				coalesce(sum(points_at_event) FILTER (WHERE action = 'ADDED'), 0),
				coalesce(sum(points_at_event) FILTER (WHERE action = 'REMOVED'), 0),
				coalesce(sum(points_at_event) FILTER (WHERE action = 'COMPLETED'), 0)
			  FROM sprint_scope_events
			 WHERE organization_id = $1 AND sprint_id = $2`,
			organizationID, sprintID).Scan(&stats.Committed, &stats.Added, &stats.Removed, &stats.Completed)
	})
	if err != nil {
		return domain.ScopeStats{}, fmt.Errorf("load scope stats: %w", err)
	}
	return stats, nil
}

// PointTotals 返回冲刺当前成员的总点数与已完成点数。
func (r *Repository) PointTotals(ctx context.Context, organizationID, sprintID uuid.UUID) (int16, int16, error) {
	var scope, completed int16
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT coalesce(sum(story_points), 0),
			       coalesce(sum(story_points) FILTER (WHERE status IN `+doneStatuses+`), 0)
			  FROM work_items
			 WHERE organization_id = $1 AND sprint_id = $2`,
			organizationID, sprintID).Scan(&scope, &completed)
	})
	if err != nil {
		return 0, 0, fmt.Errorf("load point totals: %w", err)
	}
	return scope, completed, nil
}

func (r *Repository) UpsertSnapshot(ctx context.Context, organizationID uuid.UUID, snapshot domain.Snapshot) error {
	return r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO sprint_snapshots
				(sprint_id, organization_id, snapshot_date, scope_points, completed_points, remaining_points)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (sprint_id, snapshot_date) DO UPDATE
			   SET scope_points = EXCLUDED.scope_points,
			       completed_points = EXCLUDED.completed_points,
			       remaining_points = EXCLUDED.remaining_points`,
			snapshot.SprintID, organizationID, snapshot.Date, snapshot.ScopePoints,
			snapshot.CompletedPoints, snapshot.RemainingPoints)
		if err != nil {
			return fmt.Errorf("upsert sprint snapshot: %w", err)
		}
		return nil
	})
}

func (r *Repository) ListSnapshots(ctx context.Context, organizationID, sprintID uuid.UUID) ([]domain.Snapshot, error) {
	var items []domain.Snapshot
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT sprint_id, snapshot_date, scope_points, completed_points, remaining_points
			  FROM sprint_snapshots WHERE sprint_id = $1 ORDER BY snapshot_date`, sprintID)
		if err != nil {
			return fmt.Errorf("list snapshots: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var snapshot domain.Snapshot
			if err := rows.Scan(&snapshot.SprintID, &snapshot.Date, &snapshot.ScopePoints,
				&snapshot.CompletedPoints, &snapshot.RemainingPoints); err != nil {
				return fmt.Errorf("scan snapshot: %w", err)
			}
			items = append(items, snapshot)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.Snapshot{}
	}
	return items, nil
}

func (r *Repository) ListActiveSprints(ctx context.Context, organizationID uuid.UUID) ([]domain.Sprint, error) {
	var items []domain.Sprint
	err := r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT `+sprintColumns+` FROM sprints
			 WHERE organization_id = $1 AND state = 'ACTIVE' ORDER BY created_at`, organizationID)
		if err != nil {
			return fmt.Errorf("list active sprints: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			sprint, err := scanSprint(rows)
			if err != nil {
				return err
			}
			items = append(items, sprint)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func insertOutbox(ctx context.Context, tx *sql.Tx, events []outbox.Event) error {
	repository := outbox.NewRepository()
	for _, event := range events {
		if err := repository.Insert(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSprint(row rowScanner) (domain.Sprint, error) {
	var (
		sprint domain.Sprint
		state  string
	)
	if err := row.Scan(&sprint.ID, &sprint.OrganizationID, &sprint.BoardID, &sprint.Name,
		&sprint.Goal, &state, &sprint.StartAt, &sprint.EndAt, &sprint.CapacityPoints,
		&sprint.CommittedPoints, &sprint.Version, &sprint.CreatedAt, &sprint.UpdatedAt,
		&sprint.ClosedAt); err != nil {
		return domain.Sprint{}, err
	}
	sprint.State = domain.State(state)
	return sprint, nil
}

// Enqueue 把一条通知写进 outbox（独立事务）。
// 用于"事实已落库、通知可补偿"的场景（范围变更的 count 要等更新结果才知道）。
func (r *Repository) Enqueue(ctx context.Context, organizationID uuid.UUID, event outbox.Event) error {
	return r.db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return insertOutbox(ctx, tx, []outbox.Event{event})
	})
}
