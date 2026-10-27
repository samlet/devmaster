package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

type WorkItem struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	Key                string
	ProductID          uuid.UUID
	Type               Type
	Status             Status
	Resolution         *Resolution
	Title              string
	DescriptionMD      string
	AcceptanceCriteria *string
	Priority           Priority
	Severity           *Severity
	Environment        *string
	ReporterID         string
	AssigneeID         *string
	ParentID           *uuid.UUID
	SprintID           *uuid.UUID
	StoryPoints        *int16
	EstimateMinutes    *int32
	Rank               string
	ProjectCode        *string
	WBSCode            *string
	ExternalRef        *string
	CustomFields       map[string]any
	BlockedReason      *string
	ReopenedCount      int16
	Version            int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ResolvedAt         *time.Time
	ClosedAt           *time.Time
}

type NewWorkItemInput struct {
	OrganizationID     uuid.UUID
	Key                string
	ProductID          uuid.UUID
	Type               Type
	Title              string
	DescriptionMD      string
	AcceptanceCriteria *string
	Priority           Priority
	Severity           *Severity
	Environment        *string
	ReporterID         string
	AssigneeID         *string
	ParentID           *uuid.UUID
	SprintID           *uuid.UUID
	StoryPoints        *int16
	EstimateMinutes    *int32
	Rank               string
	ProjectCode        *string
	WBSCode            *string
	ExternalRef        *string
}

func NewWorkItem(input NewWorkItemInput) (WorkItem, error) {
	if input.OrganizationID == uuid.Nil {
		return WorkItem{}, apperr.Invalid("organization_id", "must be a valid UUID")
	}
	if input.ProductID == uuid.Nil {
		return WorkItem{}, apperr.Invalid("product_id", "must be a valid UUID")
	}
	if strings.TrimSpace(input.Key) == "" {
		return WorkItem{}, apperr.Invalid("key", "must not be empty")
	}
	if strings.TrimSpace(input.ReporterID) == "" {
		return WorkItem{}, apperr.Invalid("reporter_id", "must not be empty")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return WorkItem{}, apperr.Invalid("title", "must not be empty")
	}
	if len([]rune(title)) > 500 {
		return WorkItem{}, apperr.Invalid("title", "must be at most 500 characters")
	}
	if _, err := ParsePriority(int16(input.Priority)); err != nil {
		return WorkItem{}, err
	}
	if input.Type == TypeBug && input.Severity == nil {
		return WorkItem{}, apperr.Invalid("severity", "bugs require a severity")
	}
	if input.Type != TypeBug && input.Severity != nil {
		return WorkItem{}, apperr.Invalid("severity", "only bugs can carry a severity")
	}
	if input.Type == TypeSubtask && input.ParentID == nil {
		return WorkItem{}, apperr.Invalid("parent_id", "subtasks require a parent work item")
	}
	if strings.TrimSpace(input.Rank) == "" {
		return WorkItem{}, apperr.Invalid("rank", "must not be empty")
	}
	if input.StoryPoints != nil && (*input.StoryPoints < 0 || *input.StoryPoints > 100) {
		return WorkItem{}, apperr.Invalid("story_points", "must be between 0 and 100")
	}
	if input.EstimateMinutes != nil && *input.EstimateMinutes < 0 {
		return WorkItem{}, apperr.Invalid("estimate_minutes", "must not be negative")
	}
	now := time.Now().UTC()
	return WorkItem{
		ID:                 uuid.New(),
		OrganizationID:     input.OrganizationID,
		Key:                strings.TrimSpace(input.Key),
		ProductID:          input.ProductID,
		Type:               input.Type,
		Status:             InitialStatus(input.Type),
		Title:              title,
		DescriptionMD:      strings.TrimSpace(input.DescriptionMD),
		AcceptanceCriteria: trimPtr(input.AcceptanceCriteria),
		Priority:           input.Priority,
		Severity:           input.Severity,
		Environment:        trimPtr(input.Environment),
		ReporterID:         strings.TrimSpace(input.ReporterID),
		AssigneeID:         trimPtr(input.AssigneeID),
		ParentID:           input.ParentID,
		SprintID:           input.SprintID,
		StoryPoints:        input.StoryPoints,
		EstimateMinutes:    input.EstimateMinutes,
		Rank:               strings.TrimSpace(input.Rank),
		ProjectCode:        trimPtr(input.ProjectCode),
		WBSCode:            trimPtr(input.WBSCode),
		ExternalRef:        trimPtr(input.ExternalRef),
		CustomFields:       map[string]any{},
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

// Transition 执行一次状态迁移并维护时间戳。
//
// 返回 from 供调用方写事件：事件必须记录"从哪到哪"，只记"到了哪"无法还原历史。
func (w *WorkItem) Transition(to Status, ctx TransitionContext) (Status, error) {
	if err := CanTransition(w.Type, w.Status, to, ctx); err != nil {
		return w.Status, err
	}
	from := w.Status
	now := time.Now().UTC()
	w.Status = to
	switch to {
	case StatusResolved:
		w.Resolution = ctx.Resolution
		w.ResolvedAt = &now
	case StatusVerified:
		// 验证不改 resolution：它仍然是修复时给出的结论。
	case StatusClosed:
		if ctx.Resolution != nil {
			w.Resolution = ctx.Resolution
		}
		w.ClosedAt = &now
	case StatusWontfix:
		w.Resolution = ctx.Resolution
		w.ClosedAt = &now
	case StatusCancelled:
		w.ClosedAt = &now
	case StatusReopened:
		w.ReopenedCount++
		w.Resolution = nil
		w.ResolvedAt = nil
		w.ClosedAt = nil
	}
	w.UpdatedAt = now
	w.Version++
	return from, nil
}

// MarkMoved 是同列内重排：状态不变，只更新排序与版本。
func (w *WorkItem) MarkMoved() {
	w.UpdatedAt = time.Now().UTC()
	w.Version++
}

// Assign 变更经办人。终态不允许再指派：那只会制造"已完成但有人在改"的错觉。
func (w *WorkItem) Assign(assigneeID *string) error {
	if w.Status.Terminal() {
		return apperr.FailedPrecondition("assignee_id", "cannot assign a terminal work item")
	}
	w.AssigneeID = trimPtr(assigneeID)
	w.UpdatedAt = time.Now().UTC()
	w.Version++
	return nil
}

type UpdateFields struct {
	Title              *string
	DescriptionMD      *string
	AcceptanceCriteria *string
	Priority           *Priority
	Severity           *Severity
	Environment        *string
	ProjectCode        *string
	WBSCode            *string
	ExternalRef        *string
	BlockedReason      *string
	StoryPoints        *int16
	EstimateMinutes    *int32
}

// ApplyUpdate 只应用显式给出的字段，返回真正变化的字段名。
//
// 返回变化字段是为了写审计：全量快照会把"没改的字段"也记一遍，
// 事件日志很快就不可读。
func (w *WorkItem) ApplyUpdate(fields UpdateFields) ([]string, error) {
	if w.Status.Terminal() {
		return nil, apperr.FailedPrecondition("status", "cannot update a terminal work item")
	}
	var changed []string
	if fields.Title != nil {
		title := strings.TrimSpace(*fields.Title)
		if title == "" {
			return nil, apperr.Invalid("title", "must not be empty")
		}
		if len([]rune(title)) > 500 {
			return nil, apperr.Invalid("title", "must be at most 500 characters")
		}
		if title != w.Title {
			w.Title = title
			changed = append(changed, "title")
		}
	}
	if fields.DescriptionMD != nil && strings.TrimSpace(*fields.DescriptionMD) != w.DescriptionMD {
		w.DescriptionMD = strings.TrimSpace(*fields.DescriptionMD)
		changed = append(changed, "description_md")
	}
	if fields.AcceptanceCriteria != nil && !samePtr(w.AcceptanceCriteria, fields.AcceptanceCriteria) {
		w.AcceptanceCriteria = trimPtr(fields.AcceptanceCriteria)
		changed = append(changed, "acceptance_criteria")
	}
	if fields.Priority != nil {
		if _, err := ParsePriority(int16(*fields.Priority)); err != nil {
			return nil, err
		}
		if *fields.Priority != w.Priority {
			w.Priority = *fields.Priority
			changed = append(changed, "priority")
		}
	}
	if fields.Severity != nil {
		if w.Type != TypeBug {
			return nil, apperr.Invalid("severity", "only bugs can carry a severity")
		}
		if w.Severity == nil || *fields.Severity != *w.Severity {
			value := *fields.Severity
			w.Severity = &value
			changed = append(changed, "severity")
		}
	}
	if fields.Environment != nil && !samePtr(w.Environment, fields.Environment) {
		w.Environment = trimPtr(fields.Environment)
		changed = append(changed, "environment")
	}
	if fields.ProjectCode != nil && !samePtr(w.ProjectCode, fields.ProjectCode) {
		w.ProjectCode = trimPtr(fields.ProjectCode)
		changed = append(changed, "project_code")
	}
	if fields.WBSCode != nil && !samePtr(w.WBSCode, fields.WBSCode) {
		w.WBSCode = trimPtr(fields.WBSCode)
		changed = append(changed, "wbs_code")
	}
	if fields.ExternalRef != nil && !samePtr(w.ExternalRef, fields.ExternalRef) {
		w.ExternalRef = trimPtr(fields.ExternalRef)
		changed = append(changed, "external_ref")
	}
	if fields.BlockedReason != nil && !samePtr(w.BlockedReason, fields.BlockedReason) {
		w.BlockedReason = trimPtr(fields.BlockedReason)
		changed = append(changed, "blocked_reason")
	}
	if fields.StoryPoints != nil {
		if *fields.StoryPoints < 0 || *fields.StoryPoints > 100 {
			return nil, apperr.Invalid("story_points", "must be between 0 and 100")
		}
		if w.StoryPoints == nil || *fields.StoryPoints != *w.StoryPoints {
			value := *fields.StoryPoints
			w.StoryPoints = &value
			changed = append(changed, "story_points")
		}
	}
	if fields.EstimateMinutes != nil {
		if *fields.EstimateMinutes < 0 {
			return nil, apperr.Invalid("estimate_minutes", "must not be negative")
		}
		if w.EstimateMinutes == nil || *fields.EstimateMinutes != *w.EstimateMinutes {
			value := *fields.EstimateMinutes
			w.EstimateMinutes = &value
			changed = append(changed, "estimate_minutes")
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}
	w.UpdatedAt = time.Now().UTC()
	w.Version++
	return changed, nil
}

func trimPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func samePtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.TrimSpace(*a) == strings.TrimSpace(*b)
}
