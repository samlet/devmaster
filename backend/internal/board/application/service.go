// Package application 是看板的读模型与拖拽用例。
//
// 看板不是新的事实源：列是状态类别的投影，排序是 work_items.rank。
// 这里只负责"把哪张卡放在哪两张卡之间"，落库仍走 workitem 的状态机与 CAS。
package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/board/domain"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	sprintapp "github.com/xiaofeiwu/devmaster/backend/internal/sprint/application"
	sprintdomain "github.com/xiaofeiwu/devmaster/backend/internal/sprint/domain"
	workitemapp "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type Actor struct {
	Subject        string
	OrganizationID uuid.UUID
	Roles          []string
}

type WorkItemPort interface {
	Get(ctx context.Context, actor workitemapp.Actor, id uuid.UUID) (workitemdomain.WorkItem, error)
	List(ctx context.Context, actor workitemapp.Actor, filter workitemapp.ListFilter) ([]workitemdomain.WorkItem, int, error)
	Move(ctx context.Context, actor workitemapp.Actor, cmd workitemapp.MoveCommand) (workitemdomain.WorkItem, error)
}

type SprintPort interface {
	Get(ctx context.Context, actor sprintapp.Actor, sprintID uuid.UUID) (sprintdomain.Sprint, error)
	Active(ctx context.Context, actor sprintapp.Actor, productID uuid.UUID) (sprintdomain.Sprint, bool, error)
}

type ColumnView struct {
	Column   domain.Column
	Statuses []string
	Items    []workitemdomain.WorkItem
}

type BoardView struct {
	ProductID uuid.UUID
	Sprint    *sprintdomain.Sprint
	Columns   []ColumnView
}

type Service struct {
	workItems WorkItemPort
	sprints   SprintPort
}

func NewService(workItems WorkItemPort, sprints SprintPort) *Service {
	return &Service{workItems: workItems, sprints: sprints}
}

func (s *Service) Get(ctx context.Context, actor Actor, productID uuid.UUID, sprintID *uuid.UUID) (BoardView, error) {
	workActor := workitemapp.Actor{Subject: actor.Subject, OrganizationID: actor.OrganizationID, Roles: actor.Roles}
	sprintActor := sprintapp.Actor{Subject: actor.Subject, OrganizationID: actor.OrganizationID, Roles: actor.Roles}

	var (
		sprint *sprintdomain.Sprint
	)
	if sprintID != nil {
		found, err := s.sprints.Get(ctx, sprintActor, *sprintID)
		if err != nil {
			return BoardView{}, err
		}
		sprint = &found
	} else {
		active, ok, err := s.sprints.Active(ctx, sprintActor, productID)
		if err != nil {
			return BoardView{}, err
		}
		if ok {
			sprint = &active
		}
	}

	view := BoardView{ProductID: productID, Sprint: sprint}
	var items []workitemdomain.WorkItem
	if sprint != nil {
		var err error
		items, err = s.listAll(ctx, workActor, workitemapp.ListFilter{SprintID: &sprint.ID})
		if err != nil {
			return BoardView{}, err
		}
	}
	for _, column := range domain.Order {
		columnView := ColumnView{Column: column, Items: []workitemdomain.WorkItem{}}
		for _, status := range domain.StatusesFor(column) {
			columnView.Statuses = append(columnView.Statuses, string(status))
		}
		for _, item := range items {
			if itemColumn, ok := domain.ColumnFor(item.Status); ok && itemColumn == column {
				columnView.Items = append(columnView.Items, item)
			}
		}
		view.Columns = append(view.Columns, columnView)
	}
	return view, nil
}

func (s *Service) listAll(ctx context.Context, actor workitemapp.Actor, filter workitemapp.ListFilter) ([]workitemdomain.WorkItem, error) {
	const pageSize = 200
	var all []workitemdomain.WorkItem
	for offset := 0; ; offset += pageSize {
		filter.Limit, filter.Offset = pageSize, offset
		items, total, err := s.workItems.List(ctx, actor, filter)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if offset+len(items) >= total || len(items) == 0 {
			return all, nil
		}
	}
}

type MoveCommand struct {
	WorkItemID      uuid.UUID
	ToStatus        workitemdomain.Status
	BeforeID        *uuid.UUID
	ExpectedVersion int
	Comment         string
}

// Move 把工作项放到目标列中 beforeID 之前（nil 表示追加到末尾）。
func (s *Service) Move(ctx context.Context, actor Actor, cmd MoveCommand) (workitemdomain.WorkItem, error) {
	workActor := workitemapp.Actor{Subject: actor.Subject, OrganizationID: actor.OrganizationID, Roles: actor.Roles}
	item, err := s.workItems.Get(ctx, workActor, cmd.WorkItemID)
	if err != nil {
		return workitemdomain.WorkItem{}, err
	}
	if item.SprintID == nil {
		return workitemdomain.WorkItem{}, apperr.FailedPrecondition("sprint_id", "work item is not in a sprint")
	}
	targetStatus := cmd.ToStatus
	if targetStatus == "" {
		targetStatus = item.Status
	}
	column, ok := domain.ColumnFor(targetStatus)
	if !ok {
		return workitemdomain.WorkItem{}, apperr.Invalid("to_status", fmt.Sprintf("%s is not on the board", targetStatus))
	}
	columnStatuses := domain.StatusesFor(column)

	filter := workitemapp.ListFilter{SprintID: item.SprintID, Statuses: columnStatuses}
	columnItems, err := s.listAll(ctx, workActor, filter)
	if err != nil {
		return workitemdomain.WorkItem{}, err
	}

	var prevID, nextID *uuid.UUID
	if cmd.BeforeID != nil {
		index := -1
		for i, candidate := range columnItems {
			if candidate.ID == *cmd.BeforeID {
				index = i
				break
			}
		}
		if index < 0 {
			return workitemdomain.WorkItem{}, apperr.Invalid("before_id", "target work item is not in the target column")
		}
		nextID = cmd.BeforeID
		for i := index - 1; i >= 0; i-- {
			if columnItems[i].ID != item.ID {
				prevID = &columnItems[i].ID
				break
			}
		}
	} else {
		for i := len(columnItems) - 1; i >= 0; i-- {
			if columnItems[i].ID != item.ID {
				prevID = &columnItems[i].ID
				break
			}
		}
	}

	return s.workItems.Move(ctx, workActor, workitemapp.MoveCommand{
		WorkItemID:      item.ID,
		To:              targetStatus,
		PrevID:          prevID,
		NextID:          nextID,
		ExpectedVersion: cmd.ExpectedVersion,
		Comment:         cmd.Comment,
	})
}
