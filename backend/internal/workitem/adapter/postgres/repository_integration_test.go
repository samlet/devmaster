package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/dbtest"
	productdomain "github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	workitempostgres "github.com/xiaofeiwu/devmaster/backend/internal/workitem/adapter/postgres"
	workitemapplication "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type fixture struct {
	organizationID uuid.UUID
	productID      uuid.UUID
	service        *workitemapplication.Service
	actor          workitemapplication.Actor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := dbtest.DB(t)
	dbtest.Cleanup(t)
	organizationID := uuid.New()

	product, err := productdomain.NewProduct(productdomain.NewProductInput{
		OrganizationID: organizationID,
		Key:            "HOSP",
		Name:           "酒店 PMS",
	})
	if err != nil {
		t.Fatalf("构造产品失败：%v", err)
	}
	if _, err := productpostgres.NewRepository(db).Create(context.Background(), product, nil); err != nil {
		t.Fatalf("创建产品失败：%v", err)
	}
	return &fixture{
		organizationID: organizationID,
		productID:      product.ID,
		service:        workitemapplication.NewService(workitempostgres.NewRepository(db)),
		actor: workitemapplication.Actor{
			Subject:        "reporter-1",
			OrganizationID: organizationID,
			Roles:          []string{"DEV_MEMBER", "DEV_LEAD"},
		},
	}
}

func (f *fixture) createTask(t *testing.T, title string) workitemdomain.WorkItem {
	t.Helper()
	item, err := f.service.Create(context.Background(), f.actor, workitemapplication.CreateCommand{
		ProductID: f.productID,
		Type:      workitemdomain.TypeTask,
		Title:     title,
		Priority:  workitemdomain.PriorityP2,
	})
	if err != nil {
		t.Fatalf("创建工作项失败：%v", err)
	}
	return item
}

func TestCreateAllocatesMonotonicKeys(t *testing.T) {
	f := newFixture(t)
	first := f.createTask(t, "接入 Keycloak")
	second := f.createTask(t, "实现看板")
	if first.Key != "HOSP-1" || second.Key != "HOSP-2" {
		t.Fatalf("编号不单调：%s / %s", first.Key, second.Key)
	}
	if first.Rank >= second.Rank {
		t.Fatalf("rank 应随编号递增：%s >= %s", first.Rank, second.Rank)
	}
	if first.Status != workitemdomain.StatusBacklog {
		t.Fatalf("初始状态 = %s，want BACKLOG", first.Status)
	}
}

func TestTransitionWritesEventsAndEnforcesCAS(t *testing.T) {
	f := newFixture(t)
	item := f.createTask(t, "接入 Keycloak")

	assigned, err := f.service.Assign(context.Background(), f.actor, workitemapplication.AssignCommand{
		WorkItemID: item.ID, AssigneeID: stringPointer("user-1"), ExpectedVersion: item.Version,
	})
	if err != nil {
		t.Fatalf("指派失败：%v", err)
	}
	// 用过期版本再写一次：必须冲突，不能覆盖别人的修改。
	_, err = f.service.Assign(context.Background(), f.actor, workitemapplication.AssignCommand{
		WorkItemID: item.ID, AssigneeID: stringPointer("user-2"), ExpectedVersion: item.Version,
	})
	if kind, ok := apperr.KindOf(err); !ok || kind != apperr.KindConflict {
		t.Fatalf("过期版本应当冲突，got %v", err)
	}

	current := assigned
	for _, target := range []workitemdomain.Status{
		workitemdomain.StatusTodo,
		workitemdomain.StatusInProgress,
		workitemdomain.StatusInReview,
		workitemdomain.StatusDone,
	} {
		current, err = f.service.Transition(context.Background(), f.actor, workitemapplication.TransitionCommand{
			WorkItemID: current.ID, To: target, ExpectedVersion: current.Version,
		})
		if err != nil {
			t.Fatalf("迁移到 %s 失败：%v", target, err)
		}
	}
	if current.Status != workitemdomain.StatusDone {
		t.Fatalf("最终状态 = %s，want DONE", current.Status)
	}

	events, err := f.service.ListEvents(context.Background(), f.actor, item.ID)
	if err != nil {
		t.Fatalf("读事件失败：%v", err)
	}
	// created + assigned + 4 次迁移
	if len(events) != 6 {
		t.Fatalf("事件数 = %d，want 6", len(events))
	}
	// 事件折叠出的状态必须等于当前态。
	folded := events[0].ToStatus
	for _, event := range events[1:] {
		if event.ToStatus != nil {
			folded = event.ToStatus
		}
	}
	if folded == nil || *folded != current.Status {
		t.Fatalf("事件折叠状态 = %v，当前态 = %s", folded, current.Status)
	}
}

func TestBugLifecycleRequiresResolutionAndReporterVerification(t *testing.T) {
	f := newFixture(t)
	severity := workitemdomain.SeverityMajor
	bug, err := f.service.Create(context.Background(), f.actor, workitemapplication.CreateCommand{
		ProductID: f.productID,
		Type:      workitemdomain.TypeBug,
		Title:     "登录页崩溃",
		Severity:  &severity,
	})
	if err != nil {
		t.Fatalf("创建缺陷失败：%v", err)
	}
	bug, err = f.service.Assign(context.Background(), f.actor, workitemapplication.AssignCommand{
		WorkItemID: bug.ID, AssigneeID: stringPointer("user-1"), ExpectedVersion: bug.Version,
	})
	if err != nil {
		t.Fatalf("指派失败：%v", err)
	}
	bug, err = f.service.Transition(context.Background(), f.actor, workitemapplication.TransitionCommand{
		WorkItemID: bug.ID, To: workitemdomain.StatusInProgress, ExpectedVersion: bug.Version,
	})
	if err != nil {
		t.Fatalf("开始修复失败：%v", err)
	}
	// 没有 resolution 不能标修复。
	if _, err := f.service.Transition(context.Background(), f.actor, workitemapplication.TransitionCommand{
		WorkItemID: bug.ID, To: workitemdomain.StatusResolved, ExpectedVersion: bug.Version,
	}); err == nil {
		t.Fatal("没有 resolution 的修复应当被拒绝")
	}
	resolution := workitemdomain.ResolutionFixed
	bug, err = f.service.Transition(context.Background(), f.actor, workitemapplication.TransitionCommand{
		WorkItemID: bug.ID, To: workitemdomain.StatusResolved,
		Resolution: &resolution, ExpectedVersion: bug.Version,
	})
	if err != nil {
		t.Fatalf("标修复失败：%v", err)
	}
	// 非报告人验证：用另一个 actor。
	other := f.actor
	other.Subject = "someone-else"
	other.Roles = []string{"DEV_MEMBER"}
	if _, err := f.service.Transition(context.Background(), other, workitemapplication.TransitionCommand{
		WorkItemID: bug.ID, To: workitemdomain.StatusVerified, ExpectedVersion: bug.Version,
	}); err == nil {
		t.Fatal("非报告人验证应当被拒绝")
	}
	// 报告人验证通过。
	if _, err := f.service.Transition(context.Background(), f.actor, workitemapplication.TransitionCommand{
		WorkItemID: bug.ID, To: workitemdomain.StatusVerified, ExpectedVersion: bug.Version,
	}); err != nil {
		t.Fatalf("报告人验证失败：%v", err)
	}
}

func TestSearchAndFilters(t *testing.T) {
	f := newFixture(t)
	f.createTask(t, "接入 Keycloak 登录")
	f.createTask(t, "实现看板拖拽")

	items, total, err := f.service.List(context.Background(), f.actor, workitemapplication.ListFilter{
		Query: "登录",
	})
	if err != nil {
		t.Fatalf("搜索失败：%v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("搜索命中 %d 条，want 1", total)
	}
	if items[0].Title != "接入 Keycloak 登录" {
		t.Fatalf("命中了错误的工作项：%s", items[0].Title)
	}
}

func TestCommentsAndLinks(t *testing.T) {
	f := newFixture(t)
	first := f.createTask(t, "接入 Keycloak")
	second := f.createTask(t, "实现看板")

	comment, err := f.service.AddComment(context.Background(), f.actor, first.ID, "先做 OIDC 发现")
	if err != nil {
		t.Fatalf("加评论失败：%v", err)
	}
	if comment.BodyMD != "先做 OIDC 发现" {
		t.Fatalf("评论内容不对：%s", comment.BodyMD)
	}
	comments, err := f.service.ListComments(context.Background(), f.actor, first.ID)
	if err != nil || len(comments) != 1 {
		t.Fatalf("评论数 = %d err=%v", len(comments), err)
	}

	link, err := f.service.AddLink(context.Background(), f.actor, first.ID, second.ID, workitemdomain.LinkBlocks)
	if err != nil {
		t.Fatalf("加链接失败：%v", err)
	}
	if link.Type != workitemdomain.LinkBlocks {
		t.Fatalf("链接类型 = %s", link.Type)
	}
	// 重复链接应当冲突。
	if _, err := f.service.AddLink(context.Background(), f.actor, first.ID, second.ID, workitemdomain.LinkBlocks); err == nil {
		t.Fatal("重复链接应当被拒绝")
	}
	links, err := f.service.ListLinks(context.Background(), f.actor, second.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("反查链接数 = %d err=%v", len(links), err)
	}
	if err := f.service.RemoveLink(context.Background(), f.actor, first.ID, second.ID, workitemdomain.LinkBlocks); err != nil {
		t.Fatalf("解链失败：%v", err)
	}
}

func stringPointer(value string) *string { return &value }
