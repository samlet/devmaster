package domain

import (
	"testing"

	"github.com/google/uuid"
)

func newTask(t *testing.T) WorkItem {
	t.Helper()
	item, err := NewWorkItem(NewWorkItemInput{
		OrganizationID: uuid.New(),
		Key:            "HOSP-1",
		ProductID:      uuid.New(),
		Type:           TypeTask,
		Title:          "接入 Keycloak",
		Priority:       PriorityP1,
		ReporterID:     "reporter-1",
		Rank:           RankInitial(),
	})
	if err != nil {
		t.Fatalf("NewWorkItem 报错：%v", err)
	}
	return item
}

func TestNewWorkItemValidatesShapes(t *testing.T) {
	base := NewWorkItemInput{
		OrganizationID: uuid.New(),
		Key:            "HOSP-1",
		ProductID:      uuid.New(),
		Type:           TypeBug,
		Title:          "崩溃",
		ReporterID:     "reporter-1",
		Rank:           RankInitial(),
	}
	if _, err := NewWorkItem(base); err == nil {
		t.Fatal("BUG 没有 severity 应当被拒绝")
	}
	severity := SeverityCritical
	base.Severity = &severity
	if _, err := NewWorkItem(base); err != nil {
		t.Fatalf("带 severity 的 BUG 应当通过：%v", err)
	}

	subtask := base
	subtask.Type = TypeSubtask
	subtask.Severity = nil
	if _, err := NewWorkItem(subtask); err == nil {
		t.Fatal("SUBTASK 没有父项应当被拒绝")
	}

	emptyTitle := base
	emptyTitle.Title = "  "
	if _, err := NewWorkItem(emptyTitle); err == nil {
		t.Fatal("空标题应当被拒绝")
	}
}

func TestTransitionMaintainsTimestampsAndReopenCount(t *testing.T) {
	item := newTask(t)
	if item.Status != StatusBacklog {
		t.Fatalf("初始状态 = %s，want BACKLOG", item.Status)
	}
	if _, err := item.Transition(StatusTodo, TransitionContext{Actor: "u"}); err != nil {
		t.Fatalf("BACKLOG→TODO 报错：%v", err)
	}
	if _, err := item.Transition(StatusInProgress, TransitionContext{
		Actor: "u", AssigneeID: assignee(),
	}); err != nil {
		t.Fatalf("TODO→IN_PROGRESS 报错：%v", err)
	}
	if _, err := item.Transition(StatusInReview, TransitionContext{Actor: "u"}); err != nil {
		t.Fatalf("IN_PROGRESS→IN_REVIEW 报错：%v", err)
	}
	if _, err := item.Transition(StatusDone, TransitionContext{Actor: "u"}); err != nil {
		t.Fatalf("IN_REVIEW→DONE 报错：%v", err)
	}
	if !item.Status.Terminal() {
		t.Fatal("DONE 应当是终态")
	}
	if item.Version != 4 {
		t.Fatalf("version = %d，want 4", item.Version)
	}
}

func TestBugResolutionAndReopen(t *testing.T) {
	severity := SeverityMajor
	item, err := NewWorkItem(NewWorkItemInput{
		OrganizationID: uuid.New(),
		Key:            "HOSP-2",
		ProductID:      uuid.New(),
		Type:           TypeBug,
		Title:          "登录失败",
		Severity:       &severity,
		ReporterID:     "reporter-1",
		Rank:           RankInitial(),
	})
	if err != nil {
		t.Fatalf("NewWorkItem 报错：%v", err)
	}
	item.AssigneeID = assignee()
	if _, err := item.Transition(StatusInProgress, TransitionContext{
		Actor: "reporter-1", AssigneeID: assignee(),
	}); err != nil {
		t.Fatalf("OPEN→IN_PROGRESS 报错：%v", err)
	}
	if _, err := item.Transition(StatusResolved, TransitionContext{
		Actor: "reporter-1", Resolution: resolution(ResolutionFixed),
	}); err != nil {
		t.Fatalf("IN_PROGRESS→RESOLVED 报错：%v", err)
	}
	if item.Resolution == nil || *item.Resolution != ResolutionFixed || item.ResolvedAt == nil {
		t.Fatal("修复后应当带 resolution 与 resolved_at")
	}
	if _, err := item.Transition(StatusReopened, TransitionContext{Actor: "reporter-1"}); err != nil {
		t.Fatalf("RESOLVED→REOPENED 报错：%v", err)
	}
	if item.ReopenedCount != 1 || item.Resolution != nil || item.ResolvedAt != nil {
		t.Fatalf("重新打开后应清空结论并计数：%+v", item)
	}
}

func TestApplyUpdateReturnsChangedFields(t *testing.T) {
	item := newTask(t)
	title := "接入 Keycloak（OIDC）"
	priority := PriorityP0
	changed, err := item.ApplyUpdate(UpdateFields{Title: &title, Priority: &priority})
	if err != nil {
		t.Fatalf("ApplyUpdate 报错：%v", err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %v，want 2 项", changed)
	}
	// 同样的值再写一次：不应产生变更，也不应涨版本。
	version := item.Version
	changed, err = item.ApplyUpdate(UpdateFields{Title: &title})
	if err != nil {
		t.Fatalf("ApplyUpdate 报错：%v", err)
	}
	if len(changed) != 0 || item.Version != version {
		t.Fatalf("无变化时不应写事件：changed=%v version=%d", changed, item.Version)
	}
}
