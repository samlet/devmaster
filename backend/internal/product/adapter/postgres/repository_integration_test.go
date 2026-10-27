package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/dbtest"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
)

func newProduct(t *testing.T, organizationID uuid.UUID, key string) (domain.Product, outbox.Event) {
	t.Helper()
	product, err := domain.NewProduct(domain.NewProductInput{
		OrganizationID: organizationID,
		Key:            key,
		Name:           "产品 " + key,
	})
	if err != nil {
		t.Fatalf("构造产品失败：%v", err)
	}
	event, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   organizationID,
		Producer:         "devmaster-server",
		AggregateType:    "product",
		AggregateID:      product.ID,
		AggregateVersion: 1,
		EventType:        "dev.v1.product.created",
		Payload:          map[string]any{"key": product.Key},
	})
	if err != nil {
		t.Fatalf("构造事件失败：%v", err)
	}
	return product, event
}

func TestCreateGetListWritesOutboxInSameTransaction(t *testing.T) {
	db := dbtest.DB(t)
	dbtest.Cleanup(t)
	repo := productpostgres.NewRepository(db)
	organizationID := uuid.New()

	product, event := newProduct(t, organizationID, "hosp")
	created, err := repo.Create(context.Background(), product, []outbox.Event{event})
	if err != nil {
		t.Fatalf("Create 报错：%v", err)
	}
	if created.Key != "HOSP" {
		t.Fatalf("key = %s，want HOSP", created.Key)
	}

	byKey, err := repo.GetByKey(context.Background(), organizationID, "hosp")
	if err != nil {
		t.Fatalf("GetByKey 报错：%v", err)
	}
	if byKey.ID != product.ID {
		t.Fatalf("按 key 读到的产品不对：%s", byKey.ID)
	}

	items, total, err := repo.List(context.Background(), organizationID, application.ListFilter{})
	if err != nil {
		t.Fatalf("List 报错：%v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("total=%d len=%d，want 1/1", total, len(items))
	}

	// 事件必须与产品同事务落库：读得到才说明"提交了业务但没写事件"没有发生。
	stored, found, err := outbox.NewRepository().FindByEventID(context.Background(), db, organizationID, event.EventID)
	if err != nil {
		t.Fatalf("读 outbox 报错：%v", err)
	}
	if !found {
		t.Fatal("产品已创建但 outbox 没有对应事件")
	}
	if stored.Subject != "dev.v1.product.created" {
		t.Fatalf("subject = %s", stored.Subject)
	}
}

func TestCreateRejectsDuplicateKey(t *testing.T) {
	db := dbtest.DB(t)
	dbtest.Cleanup(t)
	repo := productpostgres.NewRepository(db)
	organizationID := uuid.New()

	product, event := newProduct(t, organizationID, "FI")
	if _, err := repo.Create(context.Background(), product, []outbox.Event{event}); err != nil {
		t.Fatalf("第一次创建失败：%v", err)
	}
	duplicate, duplicateEvent := newProduct(t, organizationID, "FI")
	_, err := repo.Create(context.Background(), duplicate, []outbox.Event{duplicateEvent})
	if err == nil {
		t.Fatal("重复 key 应当被拒绝")
	}
	if kind, ok := apperr.KindOf(err); !ok || kind != apperr.KindConflict {
		t.Fatalf("应当是 Conflict，got %v", err)
	}
}

// TestOrganizationIsolation 用两个组织验证 RLS：跨组织读不到对方的数据。
func TestOrganizationIsolation(t *testing.T) {
	db := dbtest.DB(t)
	dbtest.Cleanup(t)
	repo := productpostgres.NewRepository(db)
	orgA, orgB := uuid.New(), uuid.New()

	product, event := newProduct(t, orgA, "PPM")
	if _, err := repo.Create(context.Background(), product, []outbox.Event{event}); err != nil {
		t.Fatalf("创建失败：%v", err)
	}
	_, err := repo.GetByID(context.Background(), orgB, product.ID)
	if err == nil {
		t.Fatal("跨组织读取应当返回 NotFound")
	}
	if kind, ok := apperr.KindOf(err); !ok || kind != apperr.KindNotFound {
		t.Fatalf("跨组织应当是 NotFound，got %v", err)
	}
}
