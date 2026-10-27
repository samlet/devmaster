// Package domain 是冲刺（Sprint）的领域模型。
//
// 冲刺是时间盒 + 承诺：PLANNED 时排定范围，ACTIVE 时范围变更记事件，CLOSED 时结算。
// "中途改成员"不是冲刺操作，是范围事件 —— 这是范围蔓延率能算出来的前提。
package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

type State string

const (
	StatePlanned State = "PLANNED"
	StateActive  State = "ACTIVE"
	StateClosed  State = "CLOSED"
)

type Sprint struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	BoardID         uuid.UUID
	Name            string
	Goal            string
	State           State
	StartAt         *time.Time
	EndAt           *time.Time
	CapacityPoints  *int16
	CommittedPoints int16
	Version         int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ClosedAt        *time.Time
}

type NewSprintInput struct {
	OrganizationID uuid.UUID
	BoardID        uuid.UUID
	Name           string
	Goal           string
	StartAt        *time.Time
	EndAt          *time.Time
	CapacityPoints *int16
}

func NewSprint(input NewSprintInput) (Sprint, error) {
	if input.OrganizationID == uuid.Nil || input.BoardID == uuid.Nil {
		return Sprint{}, apperr.Invalid("board_id", "must be a valid UUID")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Sprint{}, apperr.Invalid("name", "must not be empty")
	}
	if len([]rune(name)) > 100 {
		return Sprint{}, apperr.Invalid("name", "must be at most 100 characters")
	}
	if err := validateWindow(input.StartAt, input.EndAt); err != nil {
		return Sprint{}, err
	}
	if input.CapacityPoints != nil && *input.CapacityPoints < 0 {
		return Sprint{}, apperr.Invalid("capacity_points", "must not be negative")
	}
	now := time.Now().UTC()
	return Sprint{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		BoardID:        input.BoardID,
		Name:           name,
		Goal:           strings.TrimSpace(input.Goal),
		State:          StatePlanned,
		StartAt:        input.StartAt,
		EndAt:          input.EndAt,
		CapacityPoints: input.CapacityPoints,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

type UpdateFields struct {
	Name           *string
	Goal           *string
	StartAt        *time.Time
	EndAt          *time.Time
	CapacityPoints *int16
}

func (s *Sprint) ApplyUpdate(fields UpdateFields) ([]string, error) {
	if s.State == StateClosed {
		return nil, apperr.FailedPrecondition("state", "cannot update a closed sprint")
	}
	var changed []string
	if fields.Name != nil {
		name := strings.TrimSpace(*fields.Name)
		if name == "" {
			return nil, apperr.Invalid("name", "must not be empty")
		}
		if name != s.Name {
			s.Name = name
			changed = append(changed, "name")
		}
	}
	if fields.Goal != nil && strings.TrimSpace(*fields.Goal) != s.Goal {
		s.Goal = strings.TrimSpace(*fields.Goal)
		changed = append(changed, "goal")
	}
	if fields.StartAt != nil {
		s.StartAt = fields.StartAt
		changed = append(changed, "start_at")
	}
	if fields.EndAt != nil {
		s.EndAt = fields.EndAt
		changed = append(changed, "end_at")
	}
	if fields.CapacityPoints != nil {
		if *fields.CapacityPoints < 0 {
			return nil, apperr.Invalid("capacity_points", "must not be negative")
		}
		s.CapacityPoints = fields.CapacityPoints
		changed = append(changed, "capacity_points")
	}
	if err := validateWindow(s.StartAt, s.EndAt); err != nil {
		return nil, err
	}
	if len(changed) > 0 {
		s.UpdatedAt = time.Now().UTC()
		s.Version++
	}
	return changed, nil
}

// Start 把冲刺置为 ACTIVE，并冻结承诺点数。
//
// 承诺点数在这里写死，之后的新增只记 ADDED 事件，不再改 committed_points。
// 如果每次加项都改承诺，"范围蔓延"就永远算不出来。
func (s *Sprint) Start(committedPoints int16) error {
	if s.State != StatePlanned {
		return apperr.FailedPrecondition("state", "only a planned sprint can start")
	}
	if committedPoints < 0 {
		return apperr.Invalid("committed_points", "must not be negative")
	}
	now := time.Now().UTC()
	if s.StartAt == nil {
		s.StartAt = &now
	}
	if s.EndAt == nil {
		end := now.AddDate(0, 0, 14)
		s.EndAt = &end
	}
	s.State = StateActive
	s.CommittedPoints = committedPoints
	s.UpdatedAt = now
	s.Version++
	return nil
}

func (s *Sprint) Close() error {
	if s.State != StateActive {
		return apperr.FailedPrecondition("state", "only an active sprint can close")
	}
	now := time.Now().UTC()
	s.State = StateClosed
	s.ClosedAt = &now
	s.UpdatedAt = now
	s.Version++
	return nil
}

// ScopeStats 是范围事件的汇总。
type ScopeStats struct {
	Committed int16
	Added     int16
	Removed   int16
	Completed int16
}

// NetAdded 是净增范围。Added 与 Removed 都要看：只加不减会让蔓延率虚高。
func (s ScopeStats) NetAdded() int16 { return s.Added - s.Removed }

// CreepRate 是范围蔓延率：净增范围 / 承诺范围。
// 承诺为 0 时返回 0（而不是无穷大）：空冲刺谈蔓延率没有意义。
func (s ScopeStats) CreepRate() float64 {
	if s.Committed <= 0 {
		return 0
	}
	return float64(s.NetAdded()) / float64(s.Committed)
}

func validateWindow(start, end *time.Time) error {
	if start != nil && end != nil && !end.After(*start) {
		return apperr.Invalid("end_at", "must be after start_at")
	}
	return nil
}

// ScopeAction 是范围事件的类型。COMPLETED 预留给将来的实时事件，
// 当前"已完成"由工作项当前态计算（见 SprintStats）。
type ScopeAction string

const (
	ScopeCommitted ScopeAction = "COMMITTED"
	ScopeAdded     ScopeAction = "ADDED"
	ScopeRemoved   ScopeAction = "REMOVED"
	ScopeCompleted ScopeAction = "COMPLETED"
)

type ScopeEvent struct {
	ID             int64
	OrganizationID uuid.UUID
	SprintID       uuid.UUID
	WorkItemID     uuid.UUID
	Action         ScopeAction
	Points         int16
	ActorID        string
	Note           string
	OccurredAt     time.Time
}

// Snapshot 是某一天的燃尽快照。remaining = scope - completed。
type Snapshot struct {
	SprintID        uuid.UUID
	Date            time.Time
	ScopePoints     int
	CompletedPoints int
	RemainingPoints int
}

// SprintStats 是冲刺的实时统计。
type SprintStats struct {
	SprintID        uuid.UUID
	CommittedPoints int16
	AddedPoints     int16
	RemovedPoints   int16
	CompletedPoints int16
	ScopePoints     int16
	RemainingPoints int16
	CreepRate       float64
}

// BuildStats 把承诺、范围事件与当前成员汇总成统计。
// completed 由调用方按当前态算好传进来：完成是状态事实，不是范围事件。
func BuildStats(sprint Sprint, scope ScopeStats, completed, scopePoints int16) SprintStats {
	return SprintStats{
		SprintID:        sprint.ID,
		CommittedPoints: sprint.CommittedPoints,
		AddedPoints:     scope.Added,
		RemovedPoints:   scope.Removed,
		CompletedPoints: completed,
		ScopePoints:     scopePoints,
		RemainingPoints: scopePoints - completed,
		CreepRate:       scope.CreepRate(),
	}
}
