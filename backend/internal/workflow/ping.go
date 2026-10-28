// Package workflow 放 Temporal 工作流与活动。
//
// M0 只有一个 Ping：它的价值不是"能跑通 Temporal"，而是把
// "Workflow 只做编排、Activity 做 IO" 这条纪律先钉进代码结构里。
package workflow

import (
	"context"
	"log/slog"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/approver"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/artifact"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/gitea"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/notify"
	versionapp "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
)

type PingResult struct {
	Database string    `json:"database"`
	At       time.Time `json:"at"`
}

// PingWorkflow 是连通性自检：一次活动调用，验证 worker → Temporal → Activity 链路。
func PingWorkflow(ctx workflow.Context) (PingResult, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         nil,
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	var result PingResult
	if err := workflow.ExecuteActivity(ctx, "Ping").Get(ctx, &result); err != nil {
		return PingResult{}, err
	}
	return result, nil
}

// Notifier 是发布通知端口；nil 表示未启用。
//
// 通知是旁路：实现方返回错误只记日志，不改变发布结果。
type Notifier interface {
	ReleaseFailed(ctx context.Context, target notify.Target, reason string) error
	ReleasePublished(ctx context.Context, target notify.Target, tag string) error
	// ReleaseApprovalPending 是催办：waited/threshold 都是人可读时长（如 "1h30m0s"）。
	ReleaseApprovalPending(ctx context.Context, reminder notify.ApprovalReminder) error
}

// ApprovalClient 是 approver 端口。用接口而不是具体类型：待办人查询与
// 降级分支需要可替换，否则「查不到待办人时降级到角色」这条最关键的
// 安全行为只能靠人工走查。
type ApprovalClient interface {
	StartProcess(ctx context.Context, businessKey, idempotencyKey string, variables map[string]any) (string, error)
	GetInstance(ctx context.Context, instanceID string) (approver.Instance, error)
	Events(ctx context.Context, instanceID string) ([]approver.Event, error)
	PendingAssignees(ctx context.Context, instanceID string) ([]string, error)
}

// SubjectResolver 是主体目录端口；nil 表示未接入。
//
// 未接入时通知发给团队收件箱——这是显式降级，不是失败。主体目录只影响
// 「发给谁」，不影响「发不发」。
type SubjectResolver interface {
	ResolveSubject(ctx context.Context, sourceSystem, externalID string) (anchorID string, found bool, err error)
}

type Activities struct {
	DB       *database.DB
	Versions *versionapp.Service
	Approver ApprovalClient
	Gitea    *gitea.Client
	// Artifact 为 nil 表示未接入证据存证：发布链显式降级，evidence_ref 退化为
	// 发布说明引用。生产环境配置校验会拒绝这种降级。
	Artifact *artifact.Client
	// Subjects 为 nil 表示未接入主体目录：通知发给团队收件箱，不做定向。
	Subjects SubjectResolver
	Notifier Notifier
	Logger   *slog.Logger
}

func (a *Activities) Ping(ctx context.Context) (PingResult, error) {
	logger := activity.GetLogger(ctx)
	if err := a.DB.Ping(ctx); err != nil {
		return PingResult{}, err
	}
	logger.Info("temporal ping ok")
	return PingResult{Database: "ok", At: time.Now().UTC()}, nil
}
