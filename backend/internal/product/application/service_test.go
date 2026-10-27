package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
)

type fakeRepo struct {
	created domain.Product
	events  []outbox.Event
	err     error
}

func (f *fakeRepo) Create(_ context.Context, product domain.Product, events []outbox.Event) (domain.Product, error) {
	if f.err != nil {
		return domain.Product{}, f.err
	}
	f.created = product
	f.events = events
	return product, nil
}

func (f *fakeRepo) GetByID(context.Context, uuid.UUID, uuid.UUID) (domain.Product, error) {
	return f.created, nil
}

func (f *fakeRepo) GetByKey(context.Context, uuid.UUID, string) (domain.Product, error) {
	return f.created, nil
}

func (f *fakeRepo) List(context.Context, uuid.UUID, ListFilter) ([]domain.Product, int, error) {
	return []domain.Product{f.created}, 1, nil
}

func TestCreateEmitsProductCreatedEvent(t *testing.T) {
	repo := &fakeRepo{}
	service := NewService(repo)
	actor := Actor{Subject: "user-1", OrganizationID: uuid.New()}

	product, err := service.Create(context.Background(), actor, CreateCommand{
		Key:  "HOSP",
		Name: "酒店 PMS",
	})
	if err != nil {
		t.Fatalf("Create 报错：%v", err)
	}
	if repo.created.ID != product.ID {
		t.Fatal("仓储收到的产品与返回值不一致")
	}
	if len(repo.events) != 1 {
		t.Fatalf("应当写 1 条事件，got %d", len(repo.events))
	}
	event := repo.events[0]
	if event.Subject != "dev.v1.product.created" {
		t.Fatalf("subject = %s", event.Subject)
	}
	var envelope struct {
		EventType string `json:"event_type"`
		Payload   struct {
			Key string `json:"key"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(event.Payload, &envelope); err != nil {
		t.Fatalf("解析事件失败：%v", err)
	}
	if envelope.EventType != "dev.v1.product.created" || envelope.Payload.Key != "HOSP" {
		t.Fatalf("事件内容不对：%s", string(event.Payload))
	}
}

func TestCreateRejectsInvalidProductBeforeRepo(t *testing.T) {
	repo := &fakeRepo{}
	service := NewService(repo)
	_, err := service.Create(context.Background(),
		Actor{Subject: "user-1", OrganizationID: uuid.New()},
		CreateCommand{Key: "1HOSP", Name: "x"})
	if err == nil {
		t.Fatal("非法 key 应当被拒绝")
	}
	if repo.created.ID != uuid.Nil {
		t.Fatal("校验失败时不应调用仓储")
	}
}
