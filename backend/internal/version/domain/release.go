package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

type ReleaseStatus string

const (
	ReleasePendingApproval ReleaseStatus = "PENDING_APPROVAL"
	ReleaseApproved        ReleaseStatus = "APPROVED"
	ReleaseTagged          ReleaseStatus = "TAGGED"
	ReleasePublished       ReleaseStatus = "PUBLISHED"
	ReleaseFailed          ReleaseStatus = "FAILED"
)

type ReleaseRecord struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	VersionID          uuid.UUID
	Status             ReleaseStatus
	Tag                *string
	CommitFrom         *string
	CommitTo           *string
	SubmittedBy        string
	ApprovalInstanceID *string
	ApproverID         *string
	EvidenceRef        *string
	FailureReason      *string
	ReleasedBy         *string
	ReleasedAt         *time.Time
	Version            int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type NewReleaseInput struct {
	OrganizationID uuid.UUID
	VersionID      uuid.UUID
	CommitFrom     *string
	CommitTo       *string
	SubmittedBy    string
}

func NewReleaseRecord(input NewReleaseInput) (ReleaseRecord, error) {
	if input.OrganizationID == uuid.Nil || input.VersionID == uuid.Nil {
		return ReleaseRecord{}, apperr.Invalid("version_id", "must be a valid UUID")
	}
	if strings.TrimSpace(input.SubmittedBy) == "" {
		return ReleaseRecord{}, apperr.Invalid("submitted_by", "must not be empty")
	}
	commitTo := strings.TrimSpace(deref(input.CommitTo))
	if commitTo == "" {
		return ReleaseRecord{}, apperr.Invalid("commit_to", "is required to create a tag")
	}
	now := time.Now().UTC()
	return ReleaseRecord{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		VersionID:      input.VersionID,
		Status:         ReleasePendingApproval,
		CommitFrom:     trimPtr(input.CommitFrom),
		CommitTo:       &commitTo,
		SubmittedBy:    strings.TrimSpace(input.SubmittedBy),
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

func (r *ReleaseRecord) StartApproval(instanceID string) error {
	if r.Status != ReleasePendingApproval {
		return apperr.FailedPrecondition("status", "release is not pending approval")
	}
	trimmed := strings.TrimSpace(instanceID)
	if trimmed == "" {
		return apperr.Invalid("approval_instance_id", "must not be empty")
	}
	r.ApprovalInstanceID = &trimmed
	r.UpdatedAt = time.Now().UTC()
	r.Version++
	return nil
}

// Approve 记录审批结论，并做四眼校验：审批人不能是提交人。
func (r *ReleaseRecord) Approve(approverID string) error {
	if r.Status != ReleasePendingApproval {
		return apperr.FailedPrecondition("status", "release is not pending approval")
	}
	approver := strings.TrimSpace(approverID)
	if approver == "" {
		return apperr.Invalid("approver_id", "must not be empty")
	}
	if approver == r.SubmittedBy {
		return apperr.FailedPrecondition("approver_id", "four-eyes: approver cannot be the submitter")
	}
	r.Status = ReleaseApproved
	r.ApproverID = &approver
	r.UpdatedAt = time.Now().UTC()
	r.Version++
	return nil
}

func (r *ReleaseRecord) MarkTagged(tag string) error {
	if r.Status != ReleaseApproved {
		return apperr.FailedPrecondition("status", "release must be approved before tagging")
	}
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return apperr.Invalid("tag", "must not be empty")
	}
	r.Status = ReleaseTagged
	r.Tag = &trimmed
	r.UpdatedAt = time.Now().UTC()
	r.Version++
	return nil
}

func (r *ReleaseRecord) Publish(releasedBy string, at time.Time) error {
	if r.Status != ReleaseTagged {
		return apperr.FailedPrecondition("status", "release must be tagged before publishing")
	}
	r.Status = ReleasePublished
	if strings.TrimSpace(releasedBy) != "" {
		value := strings.TrimSpace(releasedBy)
		r.ReleasedBy = &value
	}
	r.ReleasedAt = &at
	r.UpdatedAt = at
	r.Version++
	return nil
}

func (r *ReleaseRecord) Fail(reason string) error {
	if r.Status == ReleasePublished {
		return apperr.FailedPrecondition("status", "published release cannot be marked failed")
	}
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		trimmed = "unspecified failure"
	}
	r.Status = ReleaseFailed
	r.FailureReason = &trimmed
	r.UpdatedAt = time.Now().UTC()
	r.Version++
	return nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
