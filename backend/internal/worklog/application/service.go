// Package application 是工时的用例层。
package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/worklog/domain"
)

type Actor struct {
	Subject        string
	OrganizationID uuid.UUID
}

type WorkItemRef struct {
	ID          uuid.UUID
	Key         string
	ProductCode string
	ProjectCode *string
	WBSCode     *string
}

type ListFilter struct {
	WorkItemID *uuid.UUID
	AuthorID   *string
	From       *time.Time
	To         *time.Time
	Limit      int
	Offset     int
}

type Repository interface {
	ResolveWorkItem(ctx context.Context, organizationID uuid.UUID, workItemID *uuid.UUID, key string) (WorkItemRef, error)
	Insert(ctx context.Context, log domain.WorkLog, outboxEvents []outbox.Event) (domain.WorkLog, error)
	Get(ctx context.Context, organizationID, id uuid.UUID) (domain.WorkLog, error)
	List(ctx context.Context, organizationID uuid.UUID, filter ListFilter) ([]domain.WorkLog, int, error)
	FindReversal(ctx context.Context, organizationID, originalID uuid.UUID) (domain.WorkLog, bool, error)
	Sum(ctx context.Context, organizationID uuid.UUID, filter ListFilter) (int, error)
}

type LogCommand struct {
	WorkItemID  *uuid.UUID
	WorkItemKey string
	Minutes     int
	WorkDate    time.Time
	Note        *string
	ProjectCode *string
	WBSCode     *string
	GradeCode   *string
	Source      domain.Source
}

type Service struct {
	repo     Repository
	producer string
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, producer: "devmaster-server"}
}

func (s *Service) Log(ctx context.Context, actor Actor, cmd LogCommand) (domain.WorkLog, error) {
	ref, err := s.repo.ResolveWorkItem(ctx, actor.OrganizationID, cmd.WorkItemID, cmd.WorkItemKey)
	if err != nil {
		return domain.WorkLog{}, err
	}
	projectCode := cmd.ProjectCode
	if projectCode == nil {
		projectCode = ref.ProjectCode
	}
	wbsCode := cmd.WBSCode
	if wbsCode == nil {
		wbsCode = ref.WBSCode
	}
	log, err := domain.NewWorkLog(domain.NewWorkLogInput{
		OrganizationID: actor.OrganizationID,
		WorkItemID:     ref.ID,
		AuthorID:       actor.Subject,
		Minutes:        cmd.Minutes,
		WorkDate:       cmd.WorkDate,
		Note:           cmd.Note,
		ProjectCode:    projectCode,
		WBSCode:        wbsCode,
		GradeCode:      cmd.GradeCode,
		Source:         cmd.Source,
	})
	if err != nil {
		return domain.WorkLog{}, err
	}
	log.WorkItemKey = ref.Key

	event, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "work_log",
		AggregateID:      log.ID,
		AggregateVersion: 1,
		EventType:        "dev.v1.work.logged",
		Payload: map[string]any{
			"work_log_id":   log.ID.String(),
			"work_item_key": ref.Key,
			"product_code":  ref.ProductCode,
			"minutes":       log.Minutes,
			"work_date":     log.WorkDate.Format("2006-01-02"),
			"project_code":  deref(log.ProjectCode),
			"wbs_code":      deref(log.WBSCode),
			"author_id":     log.AuthorID,
			"grade_code":    deref(log.GradeCode),
			"source":        string(log.Source),
		},
	})
	if err != nil {
		return domain.WorkLog{}, err
	}
	return s.repo.Insert(ctx, log, []outbox.Event{event})
}

func (s *Service) Get(ctx context.Context, actor Actor, id uuid.UUID) (domain.WorkLog, error) {
	return s.repo.Get(ctx, actor.OrganizationID, id)
}

func (s *Service) List(ctx context.Context, actor Actor, filter ListFilter) ([]domain.WorkLog, int, error) {
	return s.repo.List(ctx, actor.OrganizationID, filter)
}

func (s *Service) Sum(ctx context.Context, actor Actor, filter ListFilter) (int, error) {
	return s.repo.Sum(ctx, actor.OrganizationID, filter)
}

// Reverse 对一条工时做全额更正：生成负向记录，原记录不动。
func (s *Service) Reverse(ctx context.Context, actor Actor, workLogID uuid.UUID, note *string) (domain.WorkLog, error) {
	original, err := s.repo.Get(ctx, actor.OrganizationID, workLogID)
	if err != nil {
		return domain.WorkLog{}, err
	}
	if original.ReversalOf != nil {
		return domain.WorkLog{}, apperr.FailedPrecondition("reversal_of", "a reversal cannot be reversed")
	}
	if _, found, err := s.repo.FindReversal(ctx, actor.OrganizationID, workLogID); err != nil {
		return domain.WorkLog{}, err
	} else if found {
		return domain.WorkLog{}, apperr.Conflict("reversal_of", "work log was already reversed")
	}
	reversal, err := domain.NewReversal(original, actor.Subject, note)
	if err != nil {
		return domain.WorkLog{}, err
	}
	reversal.WorkItemKey = original.WorkItemKey

	event, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "work_log",
		AggregateID:      reversal.ID,
		AggregateVersion: 1,
		EventType:        "dev.v1.work.reversed",
		Payload: map[string]any{
			"work_log_id":  reversal.ID.String(),
			"reversal_of":  workLogID.String(),
			"minutes":      reversal.Minutes,
			"project_code": deref(reversal.ProjectCode),
			"wbs_code":     deref(reversal.WBSCode),
			"author_id":    reversal.AuthorID,
		},
	})
	if err != nil {
		return domain.WorkLog{}, err
	}
	return s.repo.Insert(ctx, reversal, []outbox.Event{event})
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
