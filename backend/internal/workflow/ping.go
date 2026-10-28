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
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/gitea"
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

type Activities struct {
	DB       *database.DB
	Versions *versionapp.Service
	Approver *approver.Client
	Gitea    *gitea.Client
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
