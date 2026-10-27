package domain

import (
	"testing"

	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

func TestColumnMapping(t *testing.T) {
	cases := map[workitemdomain.Status]Column{
		workitemdomain.StatusTodo:       ColumnTodo,
		workitemdomain.StatusOpen:       ColumnTodo,
		workitemdomain.StatusReopened:   ColumnTodo,
		workitemdomain.StatusInProgress: ColumnInProgress,
		workitemdomain.StatusInReview:   ColumnInReview,
		workitemdomain.StatusResolved:   ColumnInReview,
		workitemdomain.StatusDone:       ColumnDone,
		workitemdomain.StatusVerified:   ColumnDone,
		workitemdomain.StatusClosed:     ColumnDone,
		workitemdomain.StatusWontfix:    ColumnDone,
	}
	for status, want := range cases {
		got, ok := ColumnFor(status)
		if !ok || got != want {
			t.Fatalf("%s → %s，want %s", status, got, want)
		}
	}
	for _, status := range []workitemdomain.Status{workitemdomain.StatusBacklog, workitemdomain.StatusCancelled} {
		if _, ok := ColumnFor(status); ok {
			t.Fatalf("%s 不应出现在看板上", status)
		}
	}
}

func TestBurnCounts(t *testing.T) {
	for _, status := range []workitemdomain.Status{
		workitemdomain.StatusTodo, workitemdomain.StatusInProgress, workitemdomain.StatusInReview,
	} {
		if !CountsTowardBurn(status) {
			t.Fatalf("%s 应当计入燃尽", status)
		}
	}
	for _, status := range []workitemdomain.Status{
		workitemdomain.StatusDone, workitemdomain.StatusClosed, workitemdomain.StatusBacklog,
	} {
		if CountsTowardBurn(status) {
			t.Fatalf("%s 不应计入燃尽", status)
		}
	}
}
