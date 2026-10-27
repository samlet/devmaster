package domain

import (
	"testing"
)

func assignee() *string {
	value := "user-1"
	return &value
}

func resolution(value Resolution) *Resolution { return &value }

func contextFor(itemType Type, from, to Status) TransitionContext {
	ctx := TransitionContext{Actor: "reporter-1", ReporterID: "reporter-1", AssigneeID: assignee()}
	if itemType == TypeBug {
		switch to {
		case StatusResolved, StatusClosed:
			ctx.Resolution = resolution(ResolutionFixed)
		case StatusWontfix:
			ctx.Resolution = resolution(ResolutionWontfix)
		}
	}
	if to == StatusCancelled {
		ctx.Roles = []string{"DEV_LEAD"}
		ctx.Comment = "不再需要"
	}
	return ctx
}

// TestEveryLegalTransitionPasses 穷举状态机里的每一条边。
// 新增迁移如果忘了补守卫，这里会先红。
func TestEveryLegalTransitionPasses(t *testing.T) {
	for itemType, byStatus := range transitions {
		for from, targets := range byStatus {
			for _, to := range targets {
				ctx := contextFor(itemType, from, to)
				if err := CanTransition(itemType, from, to, ctx); err != nil {
					t.Fatalf("%s %s→%s 应当合法：%v", itemType, from, to, err)
				}
			}
		}
	}
}

func TestTerminalStatusesHaveNoTransitions(t *testing.T) {
	for _, status := range []Status{StatusDone, StatusClosed, StatusWontfix, StatusCancelled} {
		if got := AllowedTransitions(TypeBug, status); len(got) != 0 {
			t.Fatalf("%s 是终态，不应有出边：%v", status, got)
		}
	}
}

func TestIllegalTransitionsAreRejected(t *testing.T) {
	cases := []struct {
		itemType Type
		from     Status
		to       Status
	}{
		{TypeEpic, StatusOpen, StatusDone},
		{TypeStory, StatusBacklog, StatusDone},
		{TypeStory, StatusTodo, StatusInReview},
		{TypeBug, StatusOpen, StatusClosed},
		{TypeBug, StatusInProgress, StatusVerified},
		{TypeBug, StatusClosed, StatusReopened},
	}
	for _, item := range cases {
		ctx := contextFor(item.itemType, item.from, item.to)
		if err := CanTransition(item.itemType, item.from, item.to, ctx); err == nil {
			t.Fatalf("%s %s→%s 应当被拒绝", item.itemType, item.from, item.to)
		}
	}
}

func TestGuards(t *testing.T) {
	// 没有经办人不能开始。
	if err := CanTransition(TypeTask, StatusTodo, StatusInProgress, TransitionContext{Actor: "u"}); err == nil {
		t.Fatal("没有经办人时开始应当被拒绝")
	}
	// 修复缺陷必须给结论。
	if err := CanTransition(TypeBug, StatusInProgress, StatusResolved,
		TransitionContext{Actor: "u", AssigneeID: assignee()}); err == nil {
		t.Fatal("没有 resolution 的修复应当被拒绝")
	}
	// 只有报告人或 lead 能验证。
	if err := CanTransition(TypeBug, StatusResolved, StatusVerified, TransitionContext{
		Actor: "someone-else", ReporterID: "reporter-1", AssigneeID: assignee(),
	}); err == nil {
		t.Fatal("非报告人验证应当被拒绝")
	}
	if err := CanTransition(TypeBug, StatusResolved, StatusVerified, TransitionContext{
		Actor: "lead-1", ReporterID: "reporter-1", Roles: []string{"DEV_LEAD"},
	}); err != nil {
		t.Fatalf("DEV_LEAD 应当可以代验：%v", err)
	}
	// 取消需要 lead + 原因。
	if err := CanTransition(TypeTask, StatusTodo, StatusCancelled, TransitionContext{Actor: "u", Comment: "x"}); err == nil {
		t.Fatal("非 lead 取消应当被拒绝")
	}
	if err := CanTransition(TypeTask, StatusTodo, StatusCancelled, TransitionContext{
		Actor: "lead", Roles: []string{"DEV_LEAD"},
	}); err == nil {
		t.Fatal("取消没有原因应当被拒绝")
	}
	// WONTFIX 必须带 WONTFIX 结论。
	if err := CanTransition(TypeBug, StatusOpen, StatusWontfix, TransitionContext{
		Actor: "u", Resolution: resolution(ResolutionFixed),
	}); err == nil {
		t.Fatal("WONTFIX 迁移带 FIXED 结论应当被拒绝")
	}
}
