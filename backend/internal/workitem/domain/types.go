// Package domain 是工作项的领域模型：类型、状态机、排序与校验。
//
// 这一层不碰数据库、不碰 gRPC：状态机可以穷举测试，排序算法可以属性测试。
// 迁移表是代码，不是数据 —— 这是 D3 的落点（不做 Jira 式配置器）。
package domain

import (
	"fmt"
	"strings"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

type Type string

const (
	TypeEpic    Type = "EPIC"
	TypeStory   Type = "STORY"
	TypeTask    Type = "TASK"
	TypeBug     Type = "BUG"
	TypeSubtask Type = "SUBTASK"
)

func ParseType(value string) (Type, error) {
	switch Type(strings.ToUpper(strings.TrimSpace(value))) {
	case TypeEpic:
		return TypeEpic, nil
	case TypeStory:
		return TypeStory, nil
	case TypeTask:
		return TypeTask, nil
	case TypeBug:
		return TypeBug, nil
	case TypeSubtask:
		return TypeSubtask, nil
	default:
		return "", apperr.Invalid("type", fmt.Sprintf("unknown work item type %q", value))
	}
}

type Status string

const (
	StatusBacklog    Status = "BACKLOG"
	StatusTodo       Status = "TODO"
	StatusOpen       Status = "OPEN"
	StatusInProgress Status = "IN_PROGRESS"
	StatusInReview   Status = "IN_REVIEW"
	StatusResolved   Status = "RESOLVED"
	StatusVerified   Status = "VERIFIED"
	StatusDone       Status = "DONE"
	StatusClosed     Status = "CLOSED"
	StatusReopened   Status = "REOPENED"
	StatusWontfix    Status = "WONTFIX"
	StatusCancelled  Status = "CANCELLED"
)

func ParseStatus(value string) (Status, error) {
	switch Status(strings.ToUpper(strings.TrimSpace(value))) {
	case StatusBacklog:
		return StatusBacklog, nil
	case StatusTodo:
		return StatusTodo, nil
	case StatusOpen:
		return StatusOpen, nil
	case StatusInProgress:
		return StatusInProgress, nil
	case StatusInReview:
		return StatusInReview, nil
	case StatusResolved:
		return StatusResolved, nil
	case StatusVerified:
		return StatusVerified, nil
	case StatusDone:
		return StatusDone, nil
	case StatusClosed:
		return StatusClosed, nil
	case StatusReopened:
		return StatusReopened, nil
	case StatusWontfix:
		return StatusWontfix, nil
	case StatusCancelled:
		return StatusCancelled, nil
	default:
		return "", apperr.Invalid("status", fmt.Sprintf("unknown status %q", value))
	}
}

// Terminal 是终态。终态不可回退：BUG 的 REOPENED 从 RESOLVED/VERIFIED 出发，
// 不是从 CLOSED 出发。
func (s Status) Terminal() bool {
	switch s {
	case StatusDone, StatusClosed, StatusWontfix, StatusCancelled:
		return true
	default:
		return false
	}
}

type Resolution string

const (
	ResolutionFixed           Resolution = "FIXED"
	ResolutionWontfix         Resolution = "WONTFIX"
	ResolutionDuplicate       Resolution = "DUPLICATE"
	ResolutionCannotReproduce Resolution = "CANNOT_REPRODUCE"
	ResolutionByDesign        Resolution = "BY_DESIGN"
	ResolutionObsolete        Resolution = "OBSOLETE"
)

func ParseResolution(value string) (Resolution, error) {
	switch Resolution(strings.ToUpper(strings.TrimSpace(value))) {
	case ResolutionFixed:
		return ResolutionFixed, nil
	case ResolutionWontfix:
		return ResolutionWontfix, nil
	case ResolutionDuplicate:
		return ResolutionDuplicate, nil
	case ResolutionCannotReproduce:
		return ResolutionCannotReproduce, nil
	case ResolutionByDesign:
		return ResolutionByDesign, nil
	case ResolutionObsolete:
		return ResolutionObsolete, nil
	default:
		return "", apperr.Invalid("resolution", fmt.Sprintf("unknown resolution %q", value))
	}
}

type Priority int16

const (
	PriorityP0 Priority = 0
	PriorityP1 Priority = 1
	PriorityP2 Priority = 2
	PriorityP3 Priority = 3
)

func ParsePriority(value int16) (Priority, error) {
	if value < 0 || value > 3 {
		return 0, apperr.Invalid("priority", "must be between 0 (P0) and 3 (P3)")
	}
	return Priority(value), nil
}

type Severity string

const (
	SeverityBlocker  Severity = "BLOCKER"
	SeverityCritical Severity = "CRITICAL"
	SeverityMajor    Severity = "MAJOR"
	SeverityMinor    Severity = "MINOR"
	SeverityTrivial  Severity = "TRIVIAL"
)

func ParseSeverity(value string) (Severity, error) {
	switch Severity(strings.ToUpper(strings.TrimSpace(value))) {
	case SeverityBlocker:
		return SeverityBlocker, nil
	case SeverityCritical:
		return SeverityCritical, nil
	case SeverityMajor:
		return SeverityMajor, nil
	case SeverityMinor:
		return SeverityMinor, nil
	case SeverityTrivial:
		return SeverityTrivial, nil
	default:
		return "", apperr.Invalid("severity", fmt.Sprintf("unknown severity %q", value))
	}
}

type LinkType string

const (
	LinkBlocks     LinkType = "BLOCKS"
	LinkBlockedBy  LinkType = "BLOCKED_BY"
	LinkRelates    LinkType = "RELATES"
	LinkDuplicates LinkType = "DUPLICATES"
	LinkCauses     LinkType = "CAUSES"
)

func ParseLinkType(value string) (LinkType, error) {
	switch LinkType(strings.ToUpper(strings.TrimSpace(value))) {
	case LinkBlocks:
		return LinkBlocks, nil
	case LinkBlockedBy:
		return LinkBlockedBy, nil
	case LinkRelates:
		return LinkRelates, nil
	case LinkDuplicates:
		return LinkDuplicates, nil
	case LinkCauses:
		return LinkCauses, nil
	default:
		return "", apperr.Invalid("link_type", fmt.Sprintf("unknown link type %q", value))
	}
}
