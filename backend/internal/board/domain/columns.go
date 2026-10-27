// Package domain 定义看板列与工作项状态的固定映射。
//
// 列只有四列，映射写死在代码里（D3：不做自定义列）。理由：列一旦可配置，
// "完成"的定义就有多个版本，燃尽与吞吐立刻失去可比性。
package domain

import (
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type Column string

const (
	ColumnTodo       Column = "TODO"
	ColumnInProgress Column = "IN_PROGRESS"
	ColumnInReview   Column = "IN_REVIEW"
	ColumnDone       Column = "DONE"
)

// Order 是列的展示顺序。
var Order = []Column{ColumnTodo, ColumnInProgress, ColumnInReview, ColumnDone}

var columnStatuses = map[Column][]workitemdomain.Status{
	ColumnTodo: {
		workitemdomain.StatusTodo,
		workitemdomain.StatusOpen,
		workitemdomain.StatusReopened,
	},
	ColumnInProgress: {
		workitemdomain.StatusInProgress,
	},
	ColumnInReview: {
		workitemdomain.StatusInReview,
		workitemdomain.StatusResolved,
	},
	ColumnDone: {
		workitemdomain.StatusDone,
		workitemdomain.StatusVerified,
		workitemdomain.StatusClosed,
		workitemdomain.StatusWontfix,
	},
}

// ColumnFor 返回状态所在的列。BACKLOG 与 CANCELLED 不在看板上。
func ColumnFor(status workitemdomain.Status) (Column, bool) {
	for column, statuses := range columnStatuses {
		for _, candidate := range statuses {
			if candidate == status {
				return column, true
			}
		}
	}
	return "", false
}

// StatusesFor 返回一列包含的所有状态。
func StatusesFor(column Column) []workitemdomain.Status {
	return columnStatuses[column]
}

// CountsTowardBurn 表示该状态是否计入燃尽剩余。
// 完成的列与取消都不计；BACKLOG 本来就不在冲刺里。
func CountsTowardBurn(status workitemdomain.Status) bool {
	column, ok := ColumnFor(status)
	if !ok {
		return false
	}
	return column != ColumnDone
}
