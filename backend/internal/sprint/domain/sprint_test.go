package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func newSprint(t *testing.T) Sprint {
	t.Helper()
	start := time.Now().UTC()
	end := start.AddDate(0, 0, 14)
	sprint, err := NewSprint(NewSprintInput{
		OrganizationID: uuid.New(),
		BoardID:        uuid.New(),
		Name:           "Sprint 24",
		Goal:           "打通登录",
		StartAt:        &start,
		EndAt:          &end,
	})
	if err != nil {
		t.Fatalf("NewSprint 报错：%v", err)
	}
	return sprint
}

func TestNewSprintValidates(t *testing.T) {
	start := time.Now().UTC()
	end := start.AddDate(0, 0, -1)
	if _, err := NewSprint(NewSprintInput{
		OrganizationID: uuid.New(), BoardID: uuid.New(), Name: "x",
		StartAt: &start, EndAt: &end,
	}); err == nil {
		t.Fatal("结束早于开始应当被拒绝")
	}
	if _, err := NewSprint(NewSprintInput{
		OrganizationID: uuid.New(), BoardID: uuid.New(), Name: "  ",
	}); err == nil {
		t.Fatal("空名称应当被拒绝")
	}
}

func TestStartAndClose(t *testing.T) {
	sprint := newSprint(t)
	if err := sprint.Start(21); err != nil {
		t.Fatalf("Start 报错：%v", err)
	}
	if sprint.State != StateActive || sprint.CommittedPoints != 21 {
		t.Fatalf("启动后状态不对：%+v", sprint)
	}
	if err := sprint.Start(21); err == nil {
		t.Fatal("重复启动应当被拒绝")
	}
	if err := sprint.Close(); err != nil {
		t.Fatalf("Close 报错：%v", err)
	}
	if sprint.State != StateClosed || sprint.ClosedAt == nil {
		t.Fatalf("关闭后状态不对：%+v", sprint)
	}
	if err := sprint.Close(); err == nil {
		t.Fatal("重复关闭应当被拒绝")
	}
}

func TestCreepRateUsesNetAdded(t *testing.T) {
	stats := ScopeStats{Committed: 20, Added: 6, Removed: 2}
	if got := stats.NetAdded(); got != 4 {
		t.Fatalf("净增 = %d，want 4", got)
	}
	if got := stats.CreepRate(); got != 0.2 {
		t.Fatalf("蔓延率 = %v，want 0.2", got)
	}
	// 承诺为 0：返回 0 而不是 +Inf。
	empty := ScopeStats{}
	if got := empty.CreepRate(); got != 0 {
		t.Fatalf("空冲刺蔓延率 = %v，want 0", got)
	}
}

func TestBuildStats(t *testing.T) {
	sprint := newSprint(t)
	sprint.CommittedPoints = 20
	stats := BuildStats(sprint, ScopeStats{Committed: 20, Added: 6, Removed: 2}, 12, 24)
	if stats.RemainingPoints != 12 {
		t.Fatalf("剩余 = %d，want 12", stats.RemainingPoints)
	}
	if stats.CreepRate != 0.2 {
		t.Fatalf("蔓延率 = %v，want 0.2", stats.CreepRate)
	}
}
