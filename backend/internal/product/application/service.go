// Package application 是产品的用例层。
package application

import (
	"context"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
)

type Actor struct {
	Subject        string
	OrganizationID uuid.UUID
}

type Repository interface {
	// Create 必须把产品与事件写进同一个事务。
	Create(ctx context.Context, product domain.Product, events []outbox.Event) (domain.Product, error)
	GetByID(ctx context.Context, organizationID, id uuid.UUID) (domain.Product, error)
	GetByKey(ctx context.Context, organizationID uuid.UUID, key string) (domain.Product, error)
	List(ctx context.Context, organizationID uuid.UUID, filter ListFilter) ([]domain.Product, int, error)
}

type ListFilter struct {
	Status domain.Status
	Limit  int
	Offset int
}

type CreateCommand struct {
	Key         string
	Name        string
	Description string
}

type Service struct {
	repo     Repository
	producer string
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, producer: "devmaster-server"}
}

func (s *Service) Create(ctx context.Context, actor Actor, cmd CreateCommand) (domain.Product, error) {
	product, err := domain.NewProduct(domain.NewProductInput{
		OrganizationID: actor.OrganizationID,
		Key:            cmd.Key,
		Name:           cmd.Name,
		Description:    cmd.Description,
	})
	if err != nil {
		return domain.Product{}, err
	}
	event, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   actor.OrganizationID,
		Producer:         s.producer,
		AggregateType:    "product",
		AggregateID:      product.ID,
		AggregateVersion: 1,
		EventType:        "dev.v1.product.created",
		Payload: map[string]any{
			"product_id":      product.ID.String(),
			"organization_id": product.OrganizationID.String(),
			"key":             product.Key,
			"name":            product.Name,
			"status":          string(product.Status),
		},
	})
	if err != nil {
		return domain.Product{}, err
	}
	return s.repo.Create(ctx, product, []outbox.Event{event})
}

func (s *Service) Get(ctx context.Context, actor Actor, id uuid.UUID) (domain.Product, error) {
	return s.repo.GetByID(ctx, actor.OrganizationID, id)
}

func (s *Service) GetByKey(ctx context.Context, actor Actor, key string) (domain.Product, error) {
	return s.repo.GetByKey(ctx, actor.OrganizationID, key)
}

func (s *Service) List(ctx context.Context, actor Actor, filter ListFilter) ([]domain.Product, int, error) {
	return s.repo.List(ctx, actor.OrganizationID, filter)
}
