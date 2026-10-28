// Package domain 是工时（WorkLog）的领域模型。
//
// 工时只增不改：写错了用负向记录更正，原记录永远保留。
// 这不是洁癖——成本核算需要能回答"这条工时什么时候被谁改过"，
// 而 UPDATE 会把证据抹掉。
package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

type Source string

const (
	SourceManual Source = "MANUAL"
	SourceImport Source = "IMPORT"
)

type WorkLog struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	WorkItemID     uuid.UUID

	WorkItemKey string
	AuthorID    string
	Minutes     int
	WorkDate    time.Time
	Note        *string
	ProjectCode *string
	WBSCode     *string
	GradeCode   *string
	Source      Source
	ReversalOf  *uuid.UUID
	CreatedAt   time.Time
}

type NewWorkLogInput struct {
	OrganizationID uuid.UUID
	WorkItemID     uuid.UUID

	AuthorID    string
	Minutes     int
	WorkDate    time.Time
	Note        *string
	ProjectCode *string
	WBSCode     *string
	GradeCode   *string
	Source      Source
}

func NewWorkLog(input NewWorkLogInput) (WorkLog, error) {
	if input.OrganizationID == uuid.Nil || input.WorkItemID == uuid.Nil {
		return WorkLog{}, apperr.Invalid("work_item_id", "must be a valid UUID")
	}
	if strings.TrimSpace(input.AuthorID) == "" {
		return WorkLog{}, apperr.Invalid("author_id", "must not be empty")
	}
	if input.Minutes <= 0 || input.Minutes > 24*60 {
		return WorkLog{}, apperr.Invalid("minutes", "must be between 1 and 1440")
	}
	workDate := truncateDate(input.WorkDate)
	if workDate.After(truncateDate(time.Now().UTC()).AddDate(0, 0, 1)) {
		return WorkLog{}, apperr.Invalid("work_date", "cannot be in the future")
	}
	source := input.Source
	if source == "" {
		source = SourceManual
	}
	if source != SourceManual && source != SourceImport {
		return WorkLog{}, apperr.Invalid("source", "must be MANUAL or IMPORT")
	}
	return WorkLog{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		WorkItemID:     input.WorkItemID,
		AuthorID:       strings.TrimSpace(input.AuthorID),
		Minutes:        input.Minutes,
		WorkDate:       workDate,
		Note:           trimPtr(input.Note),
		ProjectCode:    trimPtr(input.ProjectCode),
		WBSCode:        trimPtr(input.WBSCode),
		GradeCode:      trimPtr(input.GradeCode),
		Source:         source,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

// NewReversal 生成一条负向更正，原记录保持不变。
func NewReversal(original WorkLog, authorID string, note *string) (WorkLog, error) {
	if original.ReversalOf != nil {
		return WorkLog{}, apperr.FailedPrecondition("reversal_of", "a reversal cannot be reversed")
	}
	if strings.TrimSpace(authorID) == "" {
		return WorkLog{}, apperr.Invalid("author_id", "must not be empty")
	}
	reversalNote := note
	if reversalNote == nil {
		value := "reversal of " + original.ID.String()
		reversalNote = &value
	}
	originalID := original.ID
	return WorkLog{
		ID:             uuid.New(),
		OrganizationID: original.OrganizationID,
		WorkItemID:     original.WorkItemID,
		AuthorID:       strings.TrimSpace(authorID),
		Minutes:        -original.Minutes,
		WorkDate:       original.WorkDate,
		Note:           trimPtr(reversalNote),
		ProjectCode:    original.ProjectCode,
		WBSCode:        original.WBSCode,
		GradeCode:      original.GradeCode,
		Source:         original.Source,
		ReversalOf:     &originalID,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

func truncateDate(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
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
