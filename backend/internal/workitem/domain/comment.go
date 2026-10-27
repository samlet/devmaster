package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

type Comment struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	WorkItemID     uuid.UUID
	AuthorID       string
	BodyMD         string
	CreatedAt      time.Time
	EditedAt       *time.Time
}

type NewCommentInput struct {
	OrganizationID uuid.UUID
	WorkItemID     uuid.UUID
	AuthorID       string
	BodyMD         string
}

func NewComment(input NewCommentInput) (Comment, error) {
	if input.OrganizationID == uuid.Nil || input.WorkItemID == uuid.Nil {
		return Comment{}, apperr.Invalid("work_item_id", "must be a valid UUID")
	}
	if strings.TrimSpace(input.AuthorID) == "" {
		return Comment{}, apperr.Invalid("author_id", "must not be empty")
	}
	body := strings.TrimSpace(input.BodyMD)
	if body == "" {
		return Comment{}, apperr.Invalid("body_md", "must not be empty")
	}
	if len([]rune(body)) > 20000 {
		return Comment{}, apperr.Invalid("body_md", "must be at most 20000 characters")
	}
	return Comment{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		WorkItemID:     input.WorkItemID,
		AuthorID:       strings.TrimSpace(input.AuthorID),
		BodyMD:         body,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

type Link struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	FromID         uuid.UUID
	ToID           uuid.UUID
	Type           LinkType
	CreatedBy      string
	CreatedAt      time.Time
}

type NewLinkInput struct {
	OrganizationID uuid.UUID
	FromID         uuid.UUID
	ToID           uuid.UUID
	Type           LinkType
	CreatedBy      string
}

func NewLink(input NewLinkInput) (Link, error) {
	if input.OrganizationID == uuid.Nil {
		return Link{}, apperr.Invalid("organization_id", "must be a valid UUID")
	}
	if input.FromID == uuid.Nil || input.ToID == uuid.Nil {
		return Link{}, apperr.Invalid("work_item_id", "must be a valid UUID")
	}
	if input.FromID == input.ToID {
		return Link{}, apperr.Invalid("to_id", "cannot link a work item to itself")
	}
	if _, err := ParseLinkType(string(input.Type)); err != nil {
		return Link{}, err
	}
	return Link{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		FromID:         input.FromID,
		ToID:           input.ToID,
		Type:           input.Type,
		CreatedBy:      strings.TrimSpace(input.CreatedBy),
		CreatedAt:      time.Now().UTC(),
	}, nil
}
