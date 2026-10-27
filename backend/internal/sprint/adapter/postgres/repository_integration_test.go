package postgres_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/dbtest"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	productdomain "github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	sprintpostgres "github.com/xiaofeiwu/devmaster/backend/internal/sprint/adapter/postgres"
	sprintapp "github.com/xiaofeiwu/devmaster/backend/internal/sprint/application"
	sprintdomain "github.com/xiaofeiwu/devmaster/backend/internal/sprint/domain"
	workitempostgres "github.com/xiaofeiwu/devmaster/backend/internal/workitem/adapter/postgres"
	workitemapp "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type fixture struct {
	db          *database.DB
	org         uuid.UUID
	product     uuid.UUID
	work        *workitemapp.Service
	sprint      *sprintapp.Service
	workActor   workitemapp.Actor
	sprintActor sprintapp.Actor
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
		db:      db,
		org:     org,
		product: product.ID,
		work:    workitemapp.NewService(workitempostgres.NewRepository(db)),
		sprint:  sprintapp.NewService(sprintpostgres.NewRepository(db)),
		workActor: workitemapp.Actor{
			Subject: "user-1", OrganizationID: org, Roles: []string{"DEV_MEMBER", "DEV_LEAD"},
		},
		sprintActor: sprintapp.Actor{
			Subject: "user-1", OrganizationID: org, Roles: []string{"DEV_MEMBER", "DEV_LEAD"},
		},
	}
}

func (f *fixture) createItem(t *testing.T, title string, points int16) workitemdomain.WorkItem {
	t.Helper()
	assignee := "user-1"
	item, err := f.work.Create(context.Background(), f.workActor, workitemapp.CreateCommand{
		ProductID:   f.product,
		Type:        workitemdomain.TypeTask,
		Title:       title,
		Priority:    workitemdomain.PriorityP2,
		AssigneeID:  &assignee,
		StoryPoints: &points,
	})
	if err != nil {
		t.Fatalf("创建工作项失败：%v", err)
	}
	return item
}

func (f *fixture) createSprint(t *testing.T, name string) sprintdomain.Sprint {
	t.Helper()
	sprint, err := f.sprint.Create(context.Background(), f.sprintActor, sprintapp.CreateCommand{
		ProductID: f.product, Name: name,
	})
	if err != nil {
		t.Fatalf("创建冲刺失败：%v", err)
	}
	return sprint
}

func (f *fixture) addItems(t *testing.T, sprintID uuid.UUID, items ...workitemdomain.WorkItem) {
	t.Helper()
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if _, err := f.sprint.AddItems(context.Background(), f.sprintActor, sprintID, ids); err != nil {
		t.Fatalf("加入冲刺失败：%v", err)
	}
}

func (f *fixture) startSprint(t *testing.T, sprint sprintdomain.Sprint) (sprintdomain.Sprint, int16) {
	t.Helper()
	started, committed, err := f.sprint.Start(context.Background(), f.sprintActor, sprint.ID, sprint.Version)
	if err != nil {
		t.Fatalf("启动冲刺失败：%v", err)
	}
	return started, committed
}

func (f *fixture) complete(t *testing.T, item workitemdomain.WorkItem) workitemdomain.WorkItem {
	t.Helper()
	// 加入冲刺会把 DB 里的 version +1，内存里的旧对象不能直接拿来 CAS。
	current, err := f.work.Get(context.Background(), f.workActor, item.ID)
	if err != nil {
		t.Fatalf("读取工作项失败：%v", err)
	}
	item = current
	for _, target := range []workitemdomain.Status{
		workitemdomain.StatusTodo,
		workitemdomain.StatusInProgress,
		workitemdomain.StatusInReview,
		workitemdomain.StatusDone,
	} {
		if item.Status == target {
			continue
		}
		next, err := f.work.Transition(context.Background(), f.workActor, workitemapp.TransitionCommand{
			WorkItemID: item.ID, To: target, ExpectedVersion: item.Version,
		})
		if err != nil {
			t.Fatalf("迁移到 %s 失败：%v", target, err)
		}
		item = next
	}
	return item
}

func TestSprintLifecycleAndScopeCreep(t *testing.T) {
	f := newFixture(t)
	sprint := f.createSprint(t, "Sprint 24")
	a := f.createItem(t, "A", 3)
	b := f.createItem(t, "B", 5)
	c := f.createItem(t, "C", 2)
	f.addItems(t, sprint.ID, a, b, c)

	started, committed := f.startSprint(t, sprint)
	if committed != 10 {
		t.Fatalf("承诺点数 = %d，want 10", committed)
	}
	// 看板按产品查 ACTIVE 冲刺：带 join 的查询必须能跑（id 歧义会在这里暴露）。
	active, ok, err := f.sprint.Active(context.Background(), f.sprintActor, f.product)
	if err != nil || !ok || active.ID != started.ID {
		t.Fatalf("ACTIVE 冲刺查询失败：ok=%v err=%v", ok, err)
	}
	sprints, err := f.sprint.List(context.Background(), f.sprintActor, f.product)
	if err != nil || len(sprints) != 1 {
		t.Fatalf("冲刺列表 = %d err=%v", len(sprints), err)
	}

	// 启动后加一项：记 ADDED，不改承诺。
	d := f.createItem(t, "D", 2)
	f.addItems(t, started.ID, d)
	// 启动后移除一项：记 REMOVED。
	if _, err := f.sprint.RemoveItems(context.Background(), f.sprintActor, started.ID, []uuid.UUID{c.ID}); err != nil {
		t.Fatalf("移除冲刺项失败：%v", err)
	}

	stats, err := f.sprint.Stats(context.Background(), f.sprintActor, started.ID)
	if err != nil {
		t.Fatalf("读统计失败：%v", err)
	}
	if stats.CommittedPoints != 10 || stats.AddedPoints != 2 || stats.RemovedPoints != 2 {
		t.Fatalf("范围统计不对：%+v", stats)
	}
	// 净增 0：蔓延率必须是 0，而不是 0.2。
	if stats.CreepRate != 0 {
		t.Fatalf("蔓延率 = %v，want 0", stats.CreepRate)
	}
	if stats.ScopePoints != 10 || stats.RemainingPoints != 10 {
		t.Fatalf("范围/剩余 = %d/%d，want 10/10", stats.ScopePoints, stats.RemainingPoints)
	}

	f.complete(t, a)
	stats, err = f.sprint.Stats(context.Background(), f.sprintActor, started.ID)
	if err != nil {
		t.Fatalf("读统计失败：%v", err)
	}
	if stats.CompletedPoints != 3 || stats.RemainingPoints != 7 {
		t.Fatalf("完成/剩余 = %d/%d，want 3/7", stats.CompletedPoints, stats.RemainingPoints)
	}

	point, err := f.sprint.Snapshot(context.Background(), f.sprintActor, started.ID)
	if err != nil {
		t.Fatalf("写快照失败：%v", err)
	}
	if point.ScopePoints != 10 || point.CompletedPoints != 3 || point.RemainingPoints != 7 {
		t.Fatalf("快照不对：%+v", point)
	}
	// 同一天重跑快照是覆盖，不是新增。
	if _, err := f.sprint.Snapshot(context.Background(), f.sprintActor, started.ID); err != nil {
		t.Fatalf("重跑快照失败：%v", err)
	}
	points, err := f.sprint.Burndown(context.Background(), f.sprintActor, started.ID)
	if err != nil || len(points) != 1 {
		t.Fatalf("燃尽点数 = %d err=%v，want 1", len(points), err)
	}
}

func TestCloseCarriesOverIncompleteItems(t *testing.T) {
	f := newFixture(t)
	sprint := f.createSprint(t, "Sprint 25")
	a := f.createItem(t, "A", 3)
	b := f.createItem(t, "B", 5)
	f.addItems(t, sprint.ID, a, b)
	started, _ := f.startSprint(t, sprint)
	f.complete(t, a)

	closed, err := f.sprint.Close(context.Background(), f.sprintActor, sprintapp.CloseCommand{
		SprintID: started.ID, ExpectedVersion: started.Version,
	})
	if err != nil {
		t.Fatalf("关闭冲刺失败：%v", err)
	}
	if closed.State != sprintdomain.StateClosed {
		t.Fatalf("状态 = %s，want CLOSED", closed.State)
	}
	reloaded, err := f.work.Get(context.Background(), f.workActor, b.ID)
	if err != nil {
		t.Fatalf("读工作项失败：%v", err)
	}
	if reloaded.SprintID != nil {
		t.Fatalf("未完成项应回 backlog，got sprint=%v", reloaded.SprintID)
	}
	stats, err := f.sprint.Stats(context.Background(), f.sprintActor, started.ID)
	if err != nil {
		t.Fatalf("读统计失败：%v", err)
	}
	// 结转不写 REMOVED，蔓延率保持 0。
	if stats.CreepRate != 0 {
		t.Fatalf("结转后蔓延率 = %v，want 0", stats.CreepRate)
	}
}

// TestConcurrentBoardMovesProduceUniqueRanks 是 M2 的 race 验收。
func TestConcurrentBoardMovesProduceUniqueRanks(t *testing.T) {
	f := newFixture(t)
	sprint := f.createSprint(t, "Sprint 26")
	items := make([]workitemdomain.WorkItem, 0, 12)
	for i := 0; i < 12; i++ {
		items = append(items, f.createItem(t, "item", 1))
	}
	f.addItems(t, sprint.ID, items...)
	f.startSprint(t, sprint)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, item := range items {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.work.Move(context.Background(), f.workActor, workitemapp.MoveCommand{
				WorkItemID:      item.ID,
				To:              workitemdomain.StatusTodo,
				ExpectedVersion: 1,
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("并发移动失败 %d 次，首个：%v", len(errs), errs[0])
	}

	listed, _, err := f.work.List(context.Background(), f.workActor, workitemapp.ListFilter{
		SprintID: &sprint.ID, Limit: 200,
	})
	if err != nil {
		t.Fatalf("读冲刺项失败：%v", err)
	}
	if len(listed) != len(items) {
		t.Fatalf("冲刺项 = %d，want %d", len(listed), len(items))
	}
	seen := map[string]bool{}
	for _, item := range listed {
		if seen[item.Rank] {
			t.Fatalf("rank 重复：%s", item.Rank)
		}
		seen[item.Rank] = true
	}
}

func TestRankRebalanceWhenSpaceExhausted(t *testing.T) {
	f := newFixture(t)
	a := f.createItem(t, "A", 1)
	b := f.createItem(t, "B", 1)
	c := f.createItem(t, "C", 1)

	// 人为制造"相邻 rank 无空隙"，触发重平衡。
	err := f.db.InOrgTx(context.Background(), f.org, func(tx *sql.Tx) error {
		for id, rank := range map[uuid.UUID]string{
			a.ID: "0000000001",
			b.ID: "0000000002",
			c.ID: "0000000003",
		} {
			if _, err := tx.ExecContext(context.Background(),
				`UPDATE work_items SET rank = $2 WHERE id = $1`, id, rank); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("设置 rank 失败：%v", err)
	}

	moved, err := f.work.Move(context.Background(), f.workActor, workitemapp.MoveCommand{
		WorkItemID:      c.ID,
		PrevID:          &a.ID,
		NextID:          &b.ID,
		ExpectedVersion: 0,
	})
	if err != nil {
		t.Fatalf("重平衡移动失败：%v", err)
	}
	reloadedA, _ := f.work.Get(context.Background(), f.workActor, a.ID)
	reloadedB, _ := f.work.Get(context.Background(), f.workActor, b.ID)
	if !(reloadedA.Rank < moved.Rank && moved.Rank < reloadedB.Rank) {
		t.Fatalf("重平衡后顺序不对：%s < %s < %s", reloadedA.Rank, moved.Rank, reloadedB.Rank)
	}
}
