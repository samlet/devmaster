package domain

import (
	"fmt"
	"strings"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

// TransitionContext 是守卫需要的上下文。
//
// 守卫只看"这件事能不能做"，不看"谁来写库"：actor 与 roles 由传输层从
// OIDC 主体带进来，领域层不解析 token。
type TransitionContext struct {
	Actor      string
	Roles      []string
	ReporterID string
	AssigneeID *string
	Resolution *Resolution
	Comment    string
}

func (c TransitionContext) hasRole(role string) bool {
	for _, item := range c.Roles {
		if item == role {
			return true
		}
	}
	return false
}

// transitions 是固定状态机。新增迁移必须改这张表 —— 它没有配置入口。
var transitions = map[Type]map[Status][]Status{
	TypeEpic: {
		StatusOpen:       {StatusInProgress},
		StatusInProgress: {StatusDone},
	},
	TypeStory: {
		StatusBacklog:    {StatusTodo},
		StatusTodo:       {StatusInProgress},
		StatusInProgress: {StatusInReview},
		StatusInReview:   {StatusInProgress, StatusDone},
	},
	TypeTask: {
		StatusBacklog:    {StatusTodo},
		StatusTodo:       {StatusInProgress},
		StatusInProgress: {StatusInReview},
		StatusInReview:   {StatusInProgress, StatusDone},
	},
	TypeSubtask: {
		StatusBacklog:    {StatusTodo},
		StatusTodo:       {StatusInProgress},
		StatusInProgress: {StatusInReview},
		StatusInReview:   {StatusInProgress, StatusDone},
	},
	TypeBug: {
		StatusOpen:       {StatusInProgress, StatusWontfix},
		StatusInProgress: {StatusResolved},
		StatusResolved:   {StatusVerified, StatusReopened},
		StatusVerified:   {StatusClosed, StatusReopened},
		StatusReopened:   {StatusInProgress},
	},
}

// InitialStatus 是新工作项的初始状态。按类型分开：缺陷从 OPEN 开始，
// 需求/任务从 BACKLOG 开始（未承诺），史诗从 OPEN 开始。
func InitialStatus(itemType Type) Status {
	switch itemType {
	case TypeEpic:
		return StatusOpen
	case TypeBug:
		return StatusOpen
	default:
		return StatusBacklog
	}
}

// AllowedTransitions 返回当前可去的状态（含"任意非终态 → CANCELLED"）。
func AllowedTransitions(itemType Type, from Status) []Status {
	if from.Terminal() {
		return nil
	}
	allowed := append([]Status{}, transitions[itemType][from]...)
	if from != StatusCancelled {
		allowed = append(allowed, StatusCancelled)
	}
	return allowed
}

// CanTransition 校验迁移是否合法，并执行守卫。
func CanTransition(itemType Type, from, to Status, ctx TransitionContext) error {
	if from == to {
		return apperr.FailedPrecondition("status", fmt.Sprintf("already in %s", from))
	}
	if from.Terminal() {
		return apperr.FailedPrecondition("status", fmt.Sprintf("%s is terminal", from))
	}
	if to == StatusCancelled {
		// 取消是管理动作：普通成员不能把别人的工作项从板上拿掉。
		if !ctx.hasRole("DEV_LEAD") {
			return apperr.FailedPrecondition("status", "only DEV_LEAD can cancel a work item")
		}
		if strings.TrimSpace(ctx.Comment) == "" {
			return apperr.Invalid("comment", "cancel requires a reason in comment")
		}
		return nil
	}
	if !containsStatus(transitions[itemType][from], to) {
		return apperr.FailedPrecondition("status", fmt.Sprintf(
			"%s cannot move from %s to %s (allowed: %s)",
			itemType, from, to, joinStatuses(AllowedTransitions(itemType, from))))
	}

	switch {
	case to == StatusInProgress:
		// 进入执行前必须有人负责：没有经办人的"进行中"只是自欺。
		if ctx.AssigneeID == nil || strings.TrimSpace(*ctx.AssigneeID) == "" {
			return apperr.FailedPrecondition("assignee_id", "assign the work item before starting it")
		}
	case itemType == TypeBug && to == StatusResolved:
		if ctx.Resolution == nil {
			return apperr.FailedPrecondition("resolution", "resolving a bug requires a resolution")
		}
	case itemType == TypeBug && to == StatusVerified:
		// 谁报的谁验，或者 lead 代验：防止"自己修自己验"。
		if ctx.Actor != ctx.ReporterID && !ctx.hasRole("DEV_LEAD") {
			return apperr.FailedPrecondition("actor", "only the reporter or DEV_LEAD can verify a bug")
		}
	case itemType == TypeBug && to == StatusClosed:
		if ctx.Resolution == nil {
			return apperr.FailedPrecondition("resolution", "closing a bug requires a resolution")
		}
	case itemType == TypeBug && to == StatusWontfix:
		if ctx.Resolution == nil || *ctx.Resolution != ResolutionWontfix {
			return apperr.FailedPrecondition("resolution", "WONTFIX transition requires resolution WONTFIX")
		}
	}
	return nil
}

func containsStatus(values []Status, target Status) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func joinStatuses(values []Status) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, string(value))
	}
	return strings.Join(parts, ", ")
}
