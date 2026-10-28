// Package domain 是版本（Version）的领域模型。
//
// Version 是软件的可发布身份（2.4.1），不是 Sprint，也不是 PLM 的工程 Release。
// 发布记录在 release.go：一次版本可以有多次发布尝试，失败保留证据。
package domain

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

type Status string

const (
	StatusPlanned    Status = "PLANNED"
	StatusInProgress Status = "IN_PROGRESS"
	StatusReleased   Status = "RELEASED"
	StatusArchived   Status = "ARCHIVED"
	StatusCancelled  Status = "CANCELLED"
)

// versionPattern 允许 2.4 / 2.4.1 / 2.4.1-rc.1。
var versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}(-[0-9A-Za-z.-]+)?$`)

type Version struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ProductID      uuid.UUID
	Name           string
	Status         Status
	MilestoneLabel *string
	PlannedDate    *time.Time
	ReleasedAt     *time.Time
	NotesRef       *string
	ScopeFrozenAt  *time.Time
	Version        int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type NewVersionInput struct {
	OrganizationID uuid.UUID
	ProductID      uuid.UUID
	Name           string
	MilestoneLabel *string
	PlannedDate    *time.Time
	NotesRef       *string
}

func NewVersion(input NewVersionInput) (Version, error) {
	if input.OrganizationID == uuid.Nil || input.ProductID == uuid.Nil {
		return Version{}, apperr.Invalid("product_id", "must be a valid UUID")
	}
	name := strings.TrimSpace(input.Name)
	if !versionPattern.MatchString(name) {
		return Version{}, apperr.Invalid("name", "must look like 2.4.1 or 2.4.1-rc.1")
	}
	now := time.Now().UTC()
	return Version{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		ProductID:      input.ProductID,
		Name:           name,
		Status:         StatusPlanned,
		MilestoneLabel: trimPtr(input.MilestoneLabel),
		PlannedDate:    input.PlannedDate,
		NotesRef:       trimPtr(input.NotesRef),
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

type UpdateFields struct {
	Name           *string
	MilestoneLabel *string
	PlannedDate    *time.Time
	NotesRef       *string
}

func (v *Version) ApplyUpdate(fields UpdateFields) ([]string, error) {
	if v.Status == StatusReleased || v.Status == StatusArchived || v.Status == StatusCancelled {
		return nil, apperr.FailedPrecondition("status", "cannot update a released/archived/cancelled version")
	}
	var changed []string
	if fields.Name != nil {
		name := strings.TrimSpace(*fields.Name)
		if !versionPattern.MatchString(name) {
			return nil, apperr.Invalid("name", "must look like 2.4.1 or 2.4.1-rc.1")
		}
		if name != v.Name {
			v.Name = name
			changed = append(changed, "name")
		}
	}
	if fields.MilestoneLabel != nil && !samePtr(v.MilestoneLabel, fields.MilestoneLabel) {
		v.MilestoneLabel = trimPtr(fields.MilestoneLabel)
		changed = append(changed, "milestone_label")
	}
	if fields.PlannedDate != nil {
		v.PlannedDate = fields.PlannedDate
		changed = append(changed, "planned_date")
	}
	if fields.NotesRef != nil && !samePtr(v.NotesRef, fields.NotesRef) {
		v.NotesRef = trimPtr(fields.NotesRef)
		changed = append(changed, "notes_ref")
	}
	if len(changed) > 0 {
		v.UpdatedAt = time.Now().UTC()
		v.Version++
	}
	return changed, nil
}

// Freeze 冻结范围：提交发布后不允许再改版本成员。
func (v *Version) Freeze() error {
	if v.Status == StatusReleased {
		return apperr.FailedPrecondition("status", "version is already released")
	}
	if v.ScopeFrozenAt != nil {
		return apperr.FailedPrecondition("scope", "version scope is already frozen")
	}
	now := time.Now().UTC()
	v.ScopeFrozenAt = &now
	v.UpdatedAt = now
	v.Version++
	return nil
}

func (v *Version) EnsureScopeMutable() error {
	if v.ScopeFrozenAt != nil {
		return apperr.FailedPrecondition("scope", "version scope is frozen after release submission")
	}
	if v.Status == StatusReleased || v.Status == StatusArchived || v.Status == StatusCancelled {
		return apperr.FailedPrecondition("status", "version is not mutable")
	}
	return nil
}

func (v *Version) MarkReleased(at time.Time) error {
	if v.Status == StatusReleased {
		return apperr.FailedPrecondition("status", "version is already released")
	}
	if v.Status == StatusCancelled || v.Status == StatusArchived {
		return apperr.FailedPrecondition("status", "cancelled/archived version cannot be released")
	}
	v.Status = StatusReleased
	v.ReleasedAt = &at
	v.UpdatedAt = at
	v.Version++
	return nil
}

func (v *Version) Archive() error {
	if v.Status == StatusReleased {
		return apperr.FailedPrecondition("status", "released version cannot be archived")
	}
	if v.Status == StatusArchived {
		return apperr.FailedPrecondition("status", "version is already archived")
	}
	v.Status = StatusArchived
	v.UpdatedAt = time.Now().UTC()
	v.Version++
	return nil
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
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return strings.TrimSpace(*a) == strings.TrimSpace(*b)
}
