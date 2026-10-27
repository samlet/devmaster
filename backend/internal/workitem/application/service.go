// Package application 是工作项的用例层：创建、迁移、评论、链接、搜索。
//
// 状态机的合法性在 domain；这里负责"取当前态 → 应用变更 → 同事务写事件与 outbox"。
package application

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type Actor struct {
	Subject        string
	OrganizationID uuid.UUID
	Roles          []string
}

// EventRecord 是 work_item_events 的一行。
type EventRecord struct {
	WorkItemID uuid.UUID
	EventType  string
	FromStatus *domain.Status
	ToStatus   *domain.Status
	ActorID    string
	Payload    map[string]any
	OccurredAt time.Time
}

type Repository interface {
	// ReserveIdentity 分配编号与 rank（product_counters 行锁）。
	ReserveIdentity(ctx context.Context, organizationID, productID uuid.UUID) (key, rank string, err error)
	Create(ctx context.Context, item domain.WorkItem, event EventRecord, outboxEvents []outbox.Event) (domain.WorkItem, error)
	GetByID(ctx context.Context, organizationID, id uuid.UUID) (domain.WorkItem, error)
	GetByKey(ctx context.Context, organizationID uuid.UUID, key string) (domain.WorkItem, error)
	List(ctx context.Context, organizationID uuid.UUID, filter ListFilter) ([]domain.WorkItem, int, error)
	Update(ctx context.Context, item domain.WorkItem, expectedVersion int, events []EventRecord, outboxEvents []outbox.Event) (domain.WorkItem, error)
	Move(ctx context.Context, item domain.WorkItem, expectedVersion int, prevID, nextID *uuid.UUID, event EventRecord, outboxEvents []outbox.Event) (domain.WorkItem, error)
	AddComment(ctx context.Context, comment domain.Comment, event EventRecord) (domain.Comment, error)
	ListComments(ctx context.Context, organizationID, workItemID uuid.UUID) ([]domain.Comment, error)
	AddLink(ctx context.Context, link domain.Link, event EventRecord) (domain.Link, error)
	RemoveLink(ctx context.Context, organizationID, fromID, toID uuid.UUID, linkType domain.LinkType, event EventRecord) error
	ListLinks(ctx context.Context, organizationID, workItemID uuid.UUID) ([]domain.Link, error)
	ListEvents(ctx context.Context, organizationID, workItemID uuid.UUID) ([]EventRecord, error)
}

type ListFilter struct {
	ProductID  *uuid.UUID
	Type       domain.Type
	Statuses   []domain.Status
	AssigneeID *string
	SprintID   *uuid.UUID
	Query      string
	Limit      int
	Offset     int
}

type CreateCommand struct {
	ProductID          uuid.UUID
	Type               domain.Type
	Title              string
	DescriptionMD      string
	AcceptanceCriteria *string
	Priority           domain.Priority
	Severity           *domain.Severity
	Environment        *string
	AssigneeID         *string
	ParentID           *uuid.UUID
	SprintID           *uuid.UUID
	StoryPoints        *int16
	EstimateMinutes    *int32
	ProjectCode        *string
	WBSCode            *string
	ExternalRef        *string
}

type UpdateCommand struct {
	WorkItemID      uuid.UUID
	ExpectedVersion int
	Fields          domain.UpdateFields
}

type TransitionCommand struct {
	WorkItemID      uuid.UUID
	To              domain.Status
	Resolution      *domain.Resolution
	Comment         string
	ExpectedVersion int
}

type MoveCommand struct {
	WorkItemID      uuid.UUID
	To              domain.Status
	PrevID          *uuid.UUID
	NextID          *uuid.UUID
	ExpectedVersion int
	Comment         string
}

type AssignCommand struct {
	WorkItemID      uuid.UUID
	AssigneeID      *string
	ExpectedVersion int
}

type Service struct {
	repo     Repository
	producer string
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, producer: "devmaster-server"}
}

func (s *Service) Create(ctx context.Context, actor Actor, cmd CreateCommand) (domain.WorkItem, error) {
	if cmd.ProductID == uuid.Nil {
		return domain.WorkItem{}, apperr.Invalid("product_id", "must be a valid UUID")
	}
	key, rank, err := s.repo.ReserveIdentity(ctx, actor.OrganizationID, cmd.ProductID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	item, err := domain.NewWorkItem(domain.NewWorkItemInput{
		OrganizationID:     actor.OrganizationID,
		Key:                key,
		ProductID:          cmd.ProductID,
		Type:               cmd.Type,
		Title:              cmd.Title,
		DescriptionMD:      cmd.DescriptionMD,
		AcceptanceCriteria: cmd.AcceptanceCriteria,
		Priority:           cmd.Priority,
		Severity:           cmd.Severity,
		Environment:        cmd.Environment,
		ReporterID:         actor.Subject,
		AssigneeID:         cmd.AssigneeID,
		ParentID:           cmd.ParentID,
		SprintID:           cmd.SprintID,
		StoryPoints:        cmd.StoryPoints,
		EstimateMinutes:    cmd.EstimateMinutes,
		Rank:               rank,
		ProjectCode:        cmd.ProjectCode,
		WBSCode:            cmd.WBSCode,
		ExternalRef:        cmd.ExternalRef,
	})
	if err != nil {
		return domain.WorkItem{}, err
	}

	event := EventRecord{
		WorkItemID: item.ID,
		EventType:  "created",
		ToStatus:   &item.Status,
		ActorID:    actor.Subject,
		Payload:    map[string]any{"key": item.Key, "type": string(item.Type)},
		OccurredAt: item.CreatedAt,
	}
	outboxEvent, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "work_item",
		AggregateID:      item.ID,
		AggregateVersion: 1,
		EventType:        "dev.v1.workitem.created",
		Payload: map[string]any{
			"work_item_id": item.ID.String(),
			"key":          item.Key,
			"product_id":   item.ProductID.String(),
			"type":         string(item.Type),
			"reporter_id":  item.ReporterID,
		},
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.repo.Create(ctx, item, event, []outbox.Event{outboxEvent})
}

func (s *Service) Get(ctx context.Context, actor Actor, id uuid.UUID) (domain.WorkItem, error) {
	return s.repo.GetByID(ctx, actor.OrganizationID, id)
}

func (s *Service) GetByKey(ctx context.Context, actor Actor, key string) (domain.WorkItem, error) {
	return s.repo.GetByKey(ctx, actor.OrganizationID, key)
}

func (s *Service) List(ctx context.Context, actor Actor, filter ListFilter) ([]domain.WorkItem, int, error) {
	return s.repo.List(ctx, actor.OrganizationID, filter)
}

func (s *Service) Update(ctx context.Context, actor Actor, cmd UpdateCommand) (domain.WorkItem, error) {
	item, err := s.repo.GetByID(ctx, actor.OrganizationID, cmd.WorkItemID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	changed, err := item.ApplyUpdate(cmd.Fields)
	if err != nil {
		return domain.WorkItem{}, err
	}
	if len(changed) == 0 {
		return item, nil
	}
	event := EventRecord{
		WorkItemID: item.ID,
		EventType:  "updated",
		ActorID:    actor.Subject,
		Payload:    map[string]any{"fields": changed},
		OccurredAt: item.UpdatedAt,
	}
	return s.repo.Update(ctx, item, cmd.ExpectedVersion, []EventRecord{event}, nil)
}

func (s *Service) Transition(ctx context.Context, actor Actor, cmd TransitionCommand) (domain.WorkItem, error) {
	item, err := s.repo.GetByID(ctx, actor.OrganizationID, cmd.WorkItemID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	from, err := item.Transition(cmd.To, domain.TransitionContext{
		Actor:      actor.Subject,
		Roles:      actor.Roles,
		ReporterID: item.ReporterID,
		AssigneeID: item.AssigneeID,
		Resolution: cmd.Resolution,
		Comment:    cmd.Comment,
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	event := EventRecord{
		WorkItemID: item.ID,
		EventType:  "transitioned",
		FromStatus: &from,
		ToStatus:   &item.Status,
		ActorID:    actor.Subject,
		Payload: map[string]any{
			"resolution": derefResolution(item.Resolution),
			"comment":    strings.TrimSpace(cmd.Comment),
		},
		OccurredAt: item.UpdatedAt,
	}
	outboxEvent, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "work_item",
		AggregateID:      item.ID,
		AggregateVersion: int64(item.Version),
		EventType:        "dev.v1.workitem.transitioned",
		Payload: map[string]any{
			"work_item_id": item.ID.String(),
			"key":          item.Key,
			"from":         string(from),
			"to":           string(item.Status),
			"resolution":   derefResolution(item.Resolution),
			"actor_id":     actor.Subject,
		},
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.repo.Update(ctx, item, cmd.ExpectedVersion, []EventRecord{event}, []outbox.Event{outboxEvent})
}

// Move 是看板拖拽的唯一入口：状态迁移走同一套守卫，rank 由仓储在行锁内计算。
func (s *Service) Move(ctx context.Context, actor Actor, cmd MoveCommand) (domain.WorkItem, error) {
	item, err := s.repo.GetByID(ctx, actor.OrganizationID, cmd.WorkItemID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	from := item.Status
	var outboxEvents []outbox.Event
	if cmd.To != "" && cmd.To != item.Status {
		from, err = item.Transition(cmd.To, domain.TransitionContext{
			Actor:      actor.Subject,
			Roles:      actor.Roles,
			ReporterID: item.ReporterID,
			AssigneeID: item.AssigneeID,
			Comment:    cmd.Comment,
		})
		if err != nil {
			return domain.WorkItem{}, err
		}
		outboxEvent, err := outbox.NewEvent(outbox.NewEventParams{
			OrganizationID:   actor.OrganizationID,
			Producer:         s.producer,
			AggregateType:    "work_item",
			AggregateID:      item.ID,
			AggregateVersion: int64(item.Version),
			EventType:        "dev.v1.workitem.transitioned",
			Payload: map[string]any{
				"work_item_id": item.ID.String(),
				"key":          item.Key,
				"from":         string(from),
				"to":           string(item.Status),
				"actor_id":     actor.Subject,
				"via":          "board",
			},
		})
		if err != nil {
			return domain.WorkItem{}, err
		}
		outboxEvents = append(outboxEvents, outboxEvent)
	} else {
		item.MarkMoved()
	}
	event := EventRecord{
		WorkItemID: item.ID,
		EventType:  "moved",
		FromStatus: &from,
		ToStatus:   &item.Status,
		ActorID:    actor.Subject,
		Payload: map[string]any{
			"prev_id": derefUUID(cmd.PrevID),
			"next_id": derefUUID(cmd.NextID),
		},
		OccurredAt: item.UpdatedAt,
	}
	return s.repo.Move(ctx, item, cmd.ExpectedVersion, cmd.PrevID, cmd.NextID, event, outboxEvents)
}

func (s *Service) Assign(ctx context.Context, actor Actor, cmd AssignCommand) (domain.WorkItem, error) {
	item, err := s.repo.GetByID(ctx, actor.OrganizationID, cmd.WorkItemID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	previous := item.AssigneeID
	if err := item.Assign(cmd.AssigneeID); err != nil {
		return domain.WorkItem{}, err
	}
	event := EventRecord{
		WorkItemID: item.ID,
		EventType:  "assigned",
		ActorID:    actor.Subject,
		Payload: map[string]any{
			"assignee_id":          derefString(item.AssigneeID),
			"previous_assignee_id": derefString(previous),
		},
		OccurredAt: item.UpdatedAt,
	}
	outboxEvent, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "work_item",
		AggregateID:      item.ID,
		AggregateVersion: int64(item.Version),
		EventType:        "dev.v1.workitem.assigned",
		Payload: map[string]any{
			"work_item_id":          item.ID.String(),
			"key":                   item.Key,
			"assignee_id":           derefString(item.AssigneeID),
			"previous_assignee_id": derefString(previous),
		},
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.repo.Update(ctx, item, cmd.ExpectedVersion, []EventRecord{event}, []outbox.Event{outboxEvent})
}

func (s *Service) AddComment(ctx context.Context, actor Actor, workItemID uuid.UUID, body string) (domain.Comment, error) {
	comment, err := domain.NewComment(domain.NewCommentInput{
		OrganizationID: actor.OrganizationID,
		WorkItemID:     workItemID,
		AuthorID:       actor.Subject,
		BodyMD:         body,
	})
	if err != nil {
		return domain.Comment{}, err
	}
	event := EventRecord{
		WorkItemID: workItemID,
		EventType:  "comment.added",
		ActorID:    actor.Subject,
		Payload:    map[string]any{"comment_id": comment.ID.String()},
		OccurredAt: comment.CreatedAt,
	}
	return s.repo.AddComment(ctx, comment, event)
}

func (s *Service) ListComments(ctx context.Context, actor Actor, workItemID uuid.UUID) ([]domain.Comment, error) {
	return s.repo.ListComments(ctx, actor.OrganizationID, workItemID)
}

func (s *Service) AddLink(ctx context.Context, actor Actor, fromID, toID uuid.UUID, linkType domain.LinkType) (domain.Link, error) {
	link, err := domain.NewLink(domain.NewLinkInput{
		OrganizationID: actor.OrganizationID,
		FromID:         fromID,
		ToID:           toID,
		Type:           linkType,
		CreatedBy:      actor.Subject,
	})
	if err != nil {
		return domain.Link{}, err
	}
	event := EventRecord{
		WorkItemID: fromID,
		EventType:  "linked",
		ActorID:    actor.Subject,
		Payload:    map[string]any{"to_id": toID.String(), "link_type": string(linkType)},
		OccurredAt: link.CreatedAt,
	}
	return s.repo.AddLink(ctx, link, event)
}

func (s *Service) RemoveLink(ctx context.Context, actor Actor, fromID, toID uuid.UUID, linkType domain.LinkType) error {
	if fromID == toID {
		return apperr.Invalid("to_id", "cannot link a work item to itself")
	}
	event := EventRecord{
		WorkItemID: fromID,
		EventType:  "unlinked",
		ActorID:    actor.Subject,
		Payload:    map[string]any{"to_id": toID.String(), "link_type": string(linkType)},
		OccurredAt: time.Now().UTC(),
	}
	return s.repo.RemoveLink(ctx, actor.OrganizationID, fromID, toID, linkType, event)
}

func (s *Service) ListLinks(ctx context.Context, actor Actor, workItemID uuid.UUID) ([]domain.Link, error) {
	return s.repo.ListLinks(ctx, actor.OrganizationID, workItemID)
}

func (s *Service) ListEvents(ctx context.Context, actor Actor, workItemID uuid.UUID) ([]EventRecord, error) {
	return s.repo.ListEvents(ctx, actor.OrganizationID, workItemID)
}

func derefUUID(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefResolution(value *domain.Resolution) string {
	if value == nil {
		return ""
	}
	return string(*value)
}
