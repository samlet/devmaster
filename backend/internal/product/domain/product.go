// Package domain 是产品（被开发的软件）的领域模型。
//
// Product 不是"项目"：项目在 PPM，Product 是软件的稳定身份（HOSP / FI / PPM）。
// 这个区分是整个边界的地基 —— 一旦这里长出 project_id，两套项目主数据就开始漂移。
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
	StatusActive   Status = "ACTIVE"
	StatusArchived Status = "ARCHIVED"
)

// keyPattern 与迁移里的 CHECK 一致：2-10 位，首字母开头，大写字母数字。
// 它是工作项编号的前缀（HOSP-1234），所以必须稳定、可读、可正则匹配。
var keyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

type Product struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Key            string
	Name           string
	Description    string
	Status         Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type NewProductInput struct {
	OrganizationID uuid.UUID
	Key            string
	Name           string
	Description    string
}

func NewProduct(input NewProductInput) (Product, error) {
	if input.OrganizationID == uuid.Nil {
		return Product{}, apperr.Invalid("organization_id", "must be a valid UUID")
	}
	key := strings.ToUpper(strings.TrimSpace(input.Key))
	if !keyPattern.MatchString(key) {
		return Product{}, apperr.Invalid("key", "must match ^[A-Z][A-Z0-9]{1,9}$")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Product{}, apperr.Invalid("name", "must not be empty")
	}
	if len([]rune(name)) > 200 {
		return Product{}, apperr.Invalid("name", "must be at most 200 characters")
	}
	description := strings.TrimSpace(input.Description)
	if len([]rune(description)) > 2000 {
		return Product{}, apperr.Invalid("description", "must be at most 2000 characters")
	}
	now := time.Now().UTC()
	return Product{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		Key:            key,
		Name:           name,
		Description:    description,
		Status:         StatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// Archive 是唯一允许的状态迁移：产品下线但历史工作项仍可读。
func (p *Product) Archive() error {
	if p.Status == StatusArchived {
		return apperr.FailedPrecondition("status", "product is already archived")
	}
	p.Status = StatusArchived
	p.UpdatedAt = time.Now().UTC()
	return nil
}
