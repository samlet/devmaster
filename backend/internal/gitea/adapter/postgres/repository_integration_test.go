package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/gitea/adapter/postgres"
	"github.com/xiaofeiwu/devmaster/backend/internal/gitea/application"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/dbtest"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	productdomain "github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	workitempostgres "github.com/xiaofeiwu/devmaster/backend/internal/workitem/adapter/postgres"
	workitemapp "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

func TestGiteaPushLinkingIsIdempotent(t *testing.T) {
	db := dbtest.DB(t)
	dbtest.Cleanup(t)
	org := uuid.New()
	product, err := productdomain.NewProduct(productdomain.NewProductInput{
		OrganizationID: org, Key: "HOSP", Name: "酒店 PMS",
	})
	if err != nil {
		t.Fatalf("构造产品失败：%v", err)
	}
	if _, err := productpostgres.NewRepository(db).Create(context.Background(), product, nil); err != nil {
		t.Fatalf("创建产品失败：%v", err)
	}
	item, err := workitemapp.NewService(workitempostgres.NewRepository(db)).Create(
		context.Background(),
		workitemapp.Actor{Subject: "alice", OrganizationID: org, Roles: []string{"DEV_MEMBER"}},
		workitemapp.CreateCommand{ProductID: product.ID, Type: workitemdomain.TypeTask, Title: "登录"},
	)
	if err != nil {
		t.Fatalf("创建工作项失败：%v", err)
	}

	service := application.NewService(postgres.NewRepository(db))
	commit := application.Commit{
		SHA:         "abc123",
		Message:     item.Key + " fix login",
		Branch:      "main",
		CommittedAt: time.Now().UTC(),
		AuthorID:    "alice",
	}
	linked, err := service.HandlePush(context.Background(), org, "meshadmin/hosp", "main", []application.Commit{commit})
	if err != nil || linked != 1 {
		t.Fatalf("首次关联 = %d err=%v，want 1", linked, err)
	}
	// 重复投递不新增。
	linked, err = service.HandlePush(context.Background(), org, "meshadmin/hosp", "main", []application.Commit{commit})
	if err != nil || linked != 0 {
		t.Fatalf("重复关联 = %d err=%v，want 0", linked, err)
	}
	// 未知 key 忽略。
	linked, err = service.HandlePush(context.Background(), org, "meshadmin/hosp", "main", []application.Commit{{
		SHA: "def456", Message: "NOPE-9 nothing",
	}})
	if err != nil || linked != 0 {
		t.Fatalf("未知 key 关联 = %d err=%v，want 0", linked, err)
	}

	// PR 合并回填状态。
	updated, err := service.HandlePullRequest(context.Background(), org, "meshadmin/hosp", application.PullRequest{
		Number: 7, State: "MERGED", HeadBranch: "main",
	})
	if err != nil || updated != 1 {
		t.Fatalf("PR 回填 = %d err=%v，want 1", updated, err)
	}
	rate, err := service.LinkRate(context.Background(), org)
	if err != nil || rate != 1 {
		t.Fatalf("关联率 = %v err=%v，want 1", rate, err)
	}

	// delivery 幂等。
	first, err := service.RecordDelivery(context.Background(), org, "delivery-1", "push", []byte(`{"ref":"refs/heads/main"}`))
	if err != nil || !first {
		t.Fatalf("首次 delivery = %v err=%v", first, err)
	}
	second, err := service.RecordDelivery(context.Background(), org, "delivery-1", "push", []byte(`{"ref":"refs/heads/main"}`))
	if err != nil || second {
		t.Fatalf("重复 delivery = %v err=%v，want false", second, err)
	}
}
