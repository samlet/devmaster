package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/dbtest"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	productdomain "github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	versionpostgres "github.com/xiaofeiwu/devmaster/backend/internal/version/adapter/postgres"
	versionapp "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
	workitempostgres "github.com/xiaofeiwu/devmaster/backend/internal/workitem/adapter/postgres"
	workitemapp "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type fixture struct {
	org     uuid.UUID
	product uuid.UUID
	work    *workitemapp.Service
	version *versionapp.Service
	actor   versionapp.Actor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
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
	return &fixture{
		org:     org,
		product: product.ID,
		work:    workitemapp.NewService(workitempostgres.NewRepository(db)),
		version: versionapp.NewService(versionpostgres.NewRepository(db)),
		actor:   versionapp.Actor{Subject: "alice", OrganizationID: org, Roles: []string{"DEV_LEAD"}},
	}
}

func (f *fixture) createItem(t *testing.T, title string) workitemdomain.WorkItem {
	t.Helper()
	item, err := f.work.Create(context.Background(), workitemapp.Actor{
		Subject: "alice", OrganizationID: f.org, Roles: []string{"DEV_LEAD"},
	}, workitemapp.CreateCommand{
		ProductID: f.product, Type: workitemdomain.TypeTask, Title: title,
		Priority: workitemdomain.PriorityP2,
	})
	if err != nil {
		t.Fatalf("创建工作项失败：%v", err)
	}
	return item
}

func TestReleaseLifecycleAndScopeFreeze(t *testing.T) {
	f := newFixture(t)
	version, err := f.version.Create(context.Background(), f.actor, versionapp.CreateCommand{
		ProductID: f.product, Name: "1.0.0",
	})
	if err != nil {
		t.Fatalf("创建版本失败：%v", err)
	}
	a := f.createItem(t, "A")
	b := f.createItem(t, "B")
	assigned, err := f.version.AssignItems(context.Background(), f.actor, version.ID, []uuid.UUID{a.ID, b.ID})
	if err != nil || assigned != 2 {
		t.Fatalf("挂载失败：assigned=%d err=%v", assigned, err)
	}

	release, err := f.version.SubmitRelease(context.Background(), f.actor, versionapp.SubmitReleaseCommand{
		VersionID: version.ID, CommitFrom: ptr("main"), CommitTo: ptr("main"),
	})
	if err != nil {
		t.Fatalf("提交发布失败：%v", err)
	}
	if release.Status != "PENDING_APPROVAL" {
		t.Fatalf("release status = %s", release.Status)
	}

	// 冻结后不能再改成员。
	c := f.createItem(t, "C")
	if _, err := f.version.AssignItems(context.Background(), f.actor, version.ID, []uuid.UUID{c.ID}); err == nil {
		t.Fatal("冻结后挂载新工作项应当被拒绝")
	}

	// 四眼：提交人自己审批要被拒。
	if _, err := f.version.Approve(context.Background(), f.actor, release.ID, "alice"); err == nil {
		t.Fatal("自审批应当被拒绝")
	}
	started, err := f.version.StartApproval(context.Background(), f.actor, release.ID, "instance-1")
	if err != nil {
		t.Fatalf("记录审批实例失败：%v", err)
	}
	if started.ApprovalInstanceID == nil || *started.ApprovalInstanceID != "instance-1" {
		t.Fatalf("审批实例 ID 未写入：%+v", started)
	}
	if _, err := f.version.Approve(context.Background(), f.actor, release.ID, "bob"); err != nil {
		t.Fatalf("他人审批失败：%v", err)
	}
	if _, err := f.version.MarkTagged(context.Background(), f.actor, release.ID, "v1.0.0"); err != nil {
		t.Fatalf("打 tag 记录失败：%v", err)
	}
	published, err := f.version.Publish(context.Background(), f.actor, release.ID, "notes:1")
	if err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if published.Status != "PUBLISHED" || published.Tag == nil || *published.Tag != "v1.0.0" {
		t.Fatalf("发布记录不对：%+v", published)
	}
	released, err := f.version.Get(context.Background(), f.actor, version.ID)
	if err != nil || released.Status != "RELEASED" {
		t.Fatalf("版本状态 = %s err=%v", released.Status, err)
	}
	if released.ScopeFrozenAt == nil {
		t.Fatal("发布后版本范围应当是冻结的")
	}

	// E9 与 E6 都要落 outbox。
	db := dbtest.DB(t)
	for _, subject := range []string{"dev.v1.release.submitted", "dev.v1.version.released"} {
		var count int
		if err := db.SQL.QueryRow(
			`SELECT count(*) FROM outbox_events WHERE subject = $1`, subject).Scan(&count); err != nil {
			t.Fatalf("查 outbox 失败：%v", err)
		}
		if count != 1 {
			t.Fatalf("%s 事件数 = %d，want 1", subject, count)
		}
	}

	// 已发布版本不能再提交发布。
	if _, err := f.version.SubmitRelease(context.Background(), f.actor, versionapp.SubmitReleaseCommand{
		VersionID: version.ID, CommitTo: ptr("main"),
	}); err == nil {
		t.Fatal("已发布版本再次提交应当被拒绝")
	}
}

func ptr(value string) *string { return &value }

func TestCheckVersionSemantics(t *testing.T) {
	f := newFixture(t)
	version, err := f.version.Create(context.Background(), f.actor, versionapp.CreateCommand{
		ProductID: f.product, Name: "2.0.0",
	})
	if err != nil {
		t.Fatalf("创建版本失败：%v", err)
	}
	// 未发布：exists=true, released=false。
	result, err := f.version.CheckVersion(context.Background(), f.actor, "HOSP", "2.0.0")
	if err != nil {
		t.Fatalf("CheckVersion 报错：%v", err)
	}
	if !result.Exists || result.Released || result.VersionID != version.ID {
		t.Fatalf("未发布结果不对：%+v", result)
	}
	// 不存在：exists=false，且不报错。
	result, err = f.version.CheckVersion(context.Background(), f.actor, "HOSP", "9.9.9")
	if err != nil || result.Exists {
		t.Fatalf("不存在版本结果 = %+v err=%v", result, err)
	}
	// 产品不存在：NotFound。
	if _, err := f.version.CheckVersion(context.Background(), f.actor, "NOPE", "1.0.0"); err == nil {
		t.Fatal("产品不存在应当报错")
	} else if kind, ok := apperr.KindOf(err); !ok || kind != apperr.KindNotFound {
		t.Fatalf("应当是 NotFound，got %v", err)
	}
}
