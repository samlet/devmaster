// Package application 是冲刺的用例层。
package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/sprint/domain"
)

type Actor struct {
	Subject        string
	OrganizationID uuid.UUID
	Roles          []string
}

type Repository interface {
	EnsureDefaultBoard(ctx context.Context, organizationID, productID uuid.UUID) (uuid.UUID, error)
	CreateSprint(ctx context.Context, sprint domain.Sprint, outboxEvents []outbox.Event) (domain.Sprint, error)
	GetSprint(ctx context.Context, organizationID, id uuid.UUID) (domain.Sprint, error)
	ListSprints(ctx context.Context, organizationID, productID uuid.UUID) ([]domain.Sprint, error)
	ActiveSprint(ctx context.Context, organizationID, productID uuid.UUID) (domain.Sprint, bool, error)
	UpdateSprint(ctx context.Context, sprint domain.Sprint, expectedVersion int) (domain.Sprint, error)
	StartSprint(ctx context.Context, sprint domain.Sprint, expectedVersion int, actorID string, outboxEvents []outbox.Event) (domain.Sprint, int16, error)
	CloseSprint(ctx context.Context, sprint domain.Sprint, expectedVersion int, moveTo *uuid.UUID, actorID string, outboxEvents []outbox.Event) (domain.Sprint, error)
	AddItems(ctx context.Context, sprint domain.Sprint, itemIDs []uuid.UUID, actorID string, outboxEvents []outbox.Event) (int, error)
	RemoveItems(ctx context.Context, sprint domain.Sprint, itemIDs []uuid.UUID, actorID string, outboxEvents []outbox.Event) (int, error)
	ScopeStats(ctx context.Context, organizationID, sprintID uuid.UUID) (domain.ScopeStats, error)
	PointTotals(ctx context.Context, organizationID, sprintID uuid.UUID) (scope, completed int16, err error)
	UpsertSnapshot(ctx context.Context, organizationID uuid.UUID, snapshot domain.Snapshot) error
	ListSnapshots(ctx context.Context, organizationID, sprintID uuid.UUID) ([]domain.Snapshot, error)
	ListActiveSprints(ctx context.Context, organizationID uuid.UUID) ([]domain.Sprint, error)
	// Enqueue 单独写一条对外通知（范围变更的"事实"已与成员变更同事务落库，
	// 这里只是通知，允许由对账补齐）。
	Enqueue(ctx context.Context, organizationID uuid.UUID, event outbox.Event) error
}

type CreateCommand struct {
	ProductID      uuid.UUID
	Name           string
	Goal           string
	StartAt        *time.Time
	EndAt          *time.Time
	CapacityPoints *int16
}

type UpdateCommand struct {
	SprintID        uuid.UUID
	ExpectedVersion int
	Fields          domain.UpdateFields
}

type CloseCommand struct {
	SprintID        uuid.UUID
	ExpectedVersion int
	MoveToSprintID  *uuid.UUID
}

type Service struct {
	repo     Repository
	producer string
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, producer: "devmaster-server"}
}

func (s *Service) Create(ctx context.Context, actor Actor, cmd CreateCommand) (domain.Sprint, error) {
	if cmd.ProductID == uuid.Nil {
		return domain.Sprint{}, apperr.Invalid("product_id", "must be a valid UUID")
	}
	boardID, err := s.repo.EnsureDefaultBoard(ctx, actor.OrganizationID, cmd.ProductID)
	if err != nil {
		return domain.Sprint{}, err
	}
	sprint, err := domain.NewSprint(domain.NewSprintInput{
		OrganizationID: actor.OrganizationID,
		BoardID:        boardID,
		Name:           cmd.Name,
		Goal:           cmd.Goal,
		StartAt:        cmd.StartAt,
		EndAt:          cmd.EndAt,
		CapacityPoints: cmd.CapacityPoints,
	})
	if err != nil {
		return domain.Sprint{}, err
	}
	event, err := s.emit(ctx, actor, sprint, "dev.v1.sprint.created", map[string]any{
		"sprint_id": sprint.ID.String(),
		"board_id":  sprint.BoardID.String(),
		"name":      sprint.Name,
	})
	if err != nil {
		return domain.Sprint{}, err
	}
	return s.repo.CreateSprint(ctx, sprint, []outbox.Event{event})
}

func (s *Service) Update(ctx context.Context, actor Actor, cmd UpdateCommand) (domain.Sprint, error) {
	sprint, err := s.repo.GetSprint(ctx, actor.OrganizationID, cmd.SprintID)
	if err != nil {
		return domain.Sprint{}, err
	}
	changed, err := sprint.ApplyUpdate(cmd.Fields)
	if err != nil {
		return domain.Sprint{}, err
	}
	if len(changed) == 0 {
		return sprint, nil
	}
	return s.repo.UpdateSprint(ctx, sprint, cmd.ExpectedVersion)
}

func (s *Service) Start(ctx context.Context, actor Actor, sprintID uuid.UUID, expectedVersion int) (domain.Sprint, int16, error) {
	sprint, err := s.repo.GetSprint(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return domain.Sprint{}, 0, err
	}
	if err := sprint.Start(0); err != nil {
		return domain.Sprint{}, 0, err
	}
	// 承诺点数 = 启动时在冲刺里的总点数。先算出来是为了让通知里带得上；
	// 仓储在事务内会再算一次，两者在行锁下一致。
	scopePoints, _, err := s.repo.PointTotals(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return domain.Sprint{}, 0, err
	}
	event, err := s.emit(ctx, actor, sprint, "dev.v1.sprint.started", map[string]any{
		"sprint_id":        sprint.ID.String(),
		"committed_points": scopePoints,
	})
	if err != nil {
		return domain.Sprint{}, 0, err
	}
	return s.repo.StartSprint(ctx, sprint, expectedVersion, actor.Subject, []outbox.Event{event})
}

func (s *Service) Close(ctx context.Context, actor Actor, cmd CloseCommand) (domain.Sprint, error) {
	sprint, err := s.repo.GetSprint(ctx, actor.OrganizationID, cmd.SprintID)
	if err != nil {
		return domain.Sprint{}, err
	}
	if err := sprint.Close(); err != nil {
		return domain.Sprint{}, err
	}
	event, err := s.emit(ctx, actor, sprint, "dev.v1.sprint.closed", map[string]any{
		"sprint_id":     sprint.ID.String(),
		"move_to":       derefUUID(cmd.MoveToSprintID),
		"committed_pts": sprint.CommittedPoints,
	})
	if err != nil {
		return domain.Sprint{}, err
	}
	return s.repo.CloseSprint(ctx, sprint, cmd.ExpectedVersion, cmd.MoveToSprintID, actor.Subject, []outbox.Event{event})
}

func (s *Service) AddItems(ctx context.Context, actor Actor, sprintID uuid.UUID, itemIDs []uuid.UUID) (int, error) {
	sprint, err := s.repo.GetSprint(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return 0, err
	}
	if sprint.State == domain.StateClosed {
		return 0, apperr.FailedPrecondition("state", "cannot add items to a closed sprint")
	}
	added, err := s.repo.AddItems(ctx, sprint, itemIDs, actor.Subject, nil)
	if err != nil {
		return 0, err
	}
	if added > 0 && sprint.State == domain.StateActive {
		if event, err := s.emit(ctx, actor, sprint, "dev.v1.sprint.scope_changed", map[string]any{
			"sprint_id": sprint.ID.String(),
			"action":    "ADDED",
			"count":     added,
		}); err == nil {
			_ = s.repo.Enqueue(ctx, actor.OrganizationID, event)
		}
	}
	return added, nil
}

func (s *Service) RemoveItems(ctx context.Context, actor Actor, sprintID uuid.UUID, itemIDs []uuid.UUID) (int, error) {
	sprint, err := s.repo.GetSprint(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return 0, err
	}
	if sprint.State == domain.StateClosed {
		return 0, apperr.FailedPrecondition("state", "cannot remove items from a closed sprint")
	}
	removed, err := s.repo.RemoveItems(ctx, sprint, itemIDs, actor.Subject, nil)
	if err != nil {
		return 0, err
	}
	if removed > 0 && sprint.State == domain.StateActive {
		if event, err := s.emit(ctx, actor, sprint, "dev.v1.sprint.scope_changed", map[string]any{
			"sprint_id": sprint.ID.String(),
			"action":    "REMOVED",
			"count":     removed,
		}); err == nil {
			_ = s.repo.Enqueue(ctx, actor.OrganizationID, event)
		}
	}
	return removed, nil
}

func (s *Service) Get(ctx context.Context, actor Actor, sprintID uuid.UUID) (domain.Sprint, error) {
	return s.repo.GetSprint(ctx, actor.OrganizationID, sprintID)
}

func (s *Service) List(ctx context.Context, actor Actor, productID uuid.UUID) ([]domain.Sprint, error) {
	return s.repo.ListSprints(ctx, actor.OrganizationID, productID)
}

func (s *Service) Active(ctx context.Context, actor Actor, productID uuid.UUID) (domain.Sprint, bool, error) {
	return s.repo.ActiveSprint(ctx, actor.OrganizationID, productID)
}

func (s *Service) Stats(ctx context.Context, actor Actor, sprintID uuid.UUID) (domain.SprintStats, error) {
	sprint, err := s.repo.GetSprint(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return domain.SprintStats{}, err
	}
	scope, err := s.repo.ScopeStats(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return domain.SprintStats{}, err
	}
	scopePoints, completed, err := s.repo.PointTotals(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return domain.SprintStats{}, err
	}
	return domain.BuildStats(sprint, scope, completed, scopePoints), nil
}

// Snapshot 写入今天的燃尽快照（覆盖当天已有值）。
func (s *Service) Snapshot(ctx context.Context, actor Actor, sprintID uuid.UUID) (domain.Snapshot, error) {
	sprint, err := s.repo.GetSprint(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return domain.Snapshot{}, err
	}
	if sprint.State == domain.StatePlanned {
		return domain.Snapshot{}, apperr.FailedPrecondition("state", "sprint has not started")
	}
	scopePoints, completed, err := s.repo.PointTotals(ctx, actor.OrganizationID, sprintID)
	if err != nil {
		return domain.Snapshot{}, err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	snapshot := domain.Snapshot{
		SprintID:        sprintID,
		Date:            today,
		ScopePoints:     int(scopePoints),
		CompletedPoints: int(completed),
		RemainingPoints: int(scopePoints - completed),
	}
	if err := s.repo.UpsertSnapshot(ctx, actor.OrganizationID, snapshot); err != nil {
		return domain.Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) Burndown(ctx context.Context, actor Actor, sprintID uuid.UUID) ([]domain.Snapshot, error) {
	if _, err := s.repo.GetSprint(ctx, actor.OrganizationID, sprintID); err != nil {
		return nil, err
	}
	return s.repo.ListSnapshots(ctx, actor.OrganizationID, sprintID)
}

// SnapshotActive 给定时任务用：为组织内所有 ACTIVE 冲刺写当天快照。
func (s *Service) SnapshotActive(ctx context.Context, actor Actor) ([]domain.Snapshot, error) {
	sprints, err := s.repo.ListActiveSprints(ctx, actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Snapshot, 0, len(sprints))
	for _, sprint := range sprints {
		snapshot, err := s.Snapshot(ctx, actor, sprint.ID)
		if err != nil {
			return out, err
		}
		out = append(out, snapshot)
	}
	return out, nil
}

func (s *Service) emit(ctx context.Context, actor Actor, sprint domain.Sprint, eventType string, payload map[string]any) (outbox.Event, error) {
	event, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "sprint",
		AggregateID:      sprint.ID,
		AggregateVersion: int64(max(sprint.Version, 1)),
		EventType:        eventType,
		Payload:          payload,
	})
	if err != nil {
		return outbox.Event{}, err
	}
	return event, nil
}

func derefUUID(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}
