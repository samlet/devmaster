// ReleaseWorkflow 是发布审批链：起流程 → 轮询审批 → 打 tag → 发布。
//
// 为什么轮询而不是回调：approver 没有通用出站回调；轮询间隔与上限都是
// 输入参数，测试可以缩短到秒级，生产保持 10s/24h 量级。
package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/approver"
	versionapp "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
	versiondomain "github.com/xiaofeiwu/devmaster/backend/internal/version/domain"
)

type ReleaseInput struct {
	OrganizationID uuid.UUID     `json:"organization_id"`
	ReleaseID      uuid.UUID     `json:"release_id"`
	VersionID      uuid.UUID     `json:"version_id"`
	SubmittedBy    string        `json:"submitted_by"`
	PollInterval   time.Duration `json:"poll_interval"`
	PollTimeout    time.Duration `json:"poll_timeout"`
}

type ReleaseInfo struct {
	VersionName string `json:"version_name"`
	ProductCode string `json:"product_code"`
	CommitFrom  string `json:"commit_from"`
	CommitTo    string `json:"commit_to"`
	NotesRef    string `json:"notes_ref"`
	ItemCount   int    `json:"item_count"`
}

type ApprovalResult struct {
	Status     string `json:"status"`
	EndResult  string `json:"end_result"`
	ApproverID string `json:"approver_id"`
}

func ReleaseWorkflow(ctx workflow.Context, input ReleaseInput) error {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			MaximumAttempts:    3,
			MaximumInterval:    10 * time.Second,
			BackoffCoefficient: 2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	var info ReleaseInfo
	if err := workflow.ExecuteActivity(ctx, "CheckReleaseSubmittable", input).Get(ctx, &info); err != nil {
		return err
	}

	var instanceID string
	if err := workflow.ExecuteActivity(ctx, "StartReleaseApproval", input, info).Get(ctx, &instanceID); err != nil {
		return err
	}

	pollInterval := input.PollInterval
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	pollTimeout := input.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = 24 * time.Hour
	}
	deadline := workflow.Now(ctx).Add(pollTimeout)

	var result ApprovalResult
	approved := false
	for !approved {
		if err := workflow.ExecuteActivity(ctx, "PollReleaseApproval", instanceID).Get(ctx, &result); err != nil {
			return err
		}
		switch result.Status {
		case "COMPLETED":
			if result.EndResult != "APPROVED" {
				return failRelease(ctx, input, fmt.Sprintf("approval rejected: %s", result.EndResult))
			}
			if result.ApproverID == "" {
				return failRelease(ctx, input, "approver identity is missing")
			}
			if result.ApproverID == input.SubmittedBy {
				return failRelease(ctx, input, "four-eyes: approver is the submitter")
			}
			approved = true
		case "REJECTED", "FAILED", "CANCELLED":
			return failRelease(ctx, input, "approval "+result.Status)
		default:
			if workflow.Now(ctx).After(deadline) {
				return failRelease(ctx, input, "approval timed out")
			}
			workflow.Sleep(ctx, pollInterval)
		}
	}

	if err := workflow.ExecuteActivity(ctx, "ApproveRelease", input, result.ApproverID).Get(ctx, nil); err != nil {
		return err
	}
	var tag string
	if err := workflow.ExecuteActivity(ctx, "CreateReleaseTag", info).Get(ctx, &tag); err != nil {
		return err
	}
	if err := workflow.ExecuteActivity(ctx, "MarkReleaseTagged", input, tag).Get(ctx, nil); err != nil {
		return err
	}
	return workflow.ExecuteActivity(ctx, "PublishRelease", input, info.NotesRef).Get(ctx, nil)
}

func failRelease(ctx workflow.Context, input ReleaseInput, reason string) error {
	// 失败落库是"记录事实"，即使 Activity 失败也要把原因带回调用方。
	_ = workflow.ExecuteActivity(ctx, "FailRelease", input, reason).Get(ctx, nil)
	return fmt.Errorf("release failed: %s", reason)
}

func (a *Activities) workflowActor(input ReleaseInput) versionapp.Actor {
	return versionapp.Actor{Subject: "release-workflow", OrganizationID: input.OrganizationID}
}

func (a *Activities) CheckReleaseSubmittable(ctx context.Context, input ReleaseInput) (ReleaseInfo, error) {
	actor := a.workflowActor(input)
	release, err := a.Versions.GetRelease(ctx, actor, input.ReleaseID)
	if err != nil {
		return ReleaseInfo{}, err
	}
	if release.Status != versiondomain.ReleasePendingApproval {
		return ReleaseInfo{}, fmt.Errorf("release %s is %s, not pending approval", release.ID, release.Status)
	}
	version, err := a.Versions.Get(ctx, actor, input.VersionID)
	if err != nil {
		return ReleaseInfo{}, err
	}
	productCode, err := a.Versions.ProductCode(ctx, actor, version.ProductID)
	if err != nil {
		return ReleaseInfo{}, err
	}
	itemCount, err := a.Versions.ItemCount(ctx, actor, input.VersionID)
	if err != nil {
		return ReleaseInfo{}, err
	}
	return ReleaseInfo{
		VersionName: version.Name,
		ProductCode: productCode,
		CommitFrom:  deref(release.CommitFrom),
		CommitTo:    deref(release.CommitTo),
		NotesRef:    deref(version.NotesRef),
		ItemCount:   itemCount,
	}, nil
}

func (a *Activities) StartReleaseApproval(ctx context.Context, input ReleaseInput, info ReleaseInfo) (string, error) {
	variables := map[string]any{
		"product_code":      info.ProductCode,
		"version":           info.VersionName,
		"tag":               "v" + info.VersionName,
		"commit_from":       info.CommitFrom,
		"commit_to":         info.CommitTo,
		"item_count":        info.ItemCount,
		"release_notes_ref": info.NotesRef,
		"submitted_by":      input.SubmittedBy,
	}
	instanceID, err := a.Approver.StartProcess(ctx, input.VersionID.String(), input.ReleaseID.String(), variables)
	if err != nil {
		return "", err
	}
	if _, err := a.Versions.StartApproval(ctx, a.workflowActor(input), input.ReleaseID, instanceID); err != nil {
		return "", err
	}
	return instanceID, nil
}

func (a *Activities) PollReleaseApproval(ctx context.Context, instanceID string) (ApprovalResult, error) {
	instance, err := a.Approver.GetInstance(ctx, instanceID)
	if err != nil {
		return ApprovalResult{}, err
	}
	result := ApprovalResult{Status: instance.Status, EndResult: instance.EndResult}
	switch instance.Status {
	case "COMPLETED", "REJECTED", "FAILED", "CANCELLED":
		events, err := a.Approver.Events(ctx, instanceID)
		if err != nil {
			return ApprovalResult{}, err
		}
		result.ApproverID = approver.ApproverFromEvents(events)
	}
	return result, nil
}

func (a *Activities) ApproveRelease(ctx context.Context, input ReleaseInput, approverID string) error {
	_, err := a.Versions.Approve(ctx, a.workflowActor(input), input.ReleaseID, approverID)
	return err
}

func (a *Activities) CreateReleaseTag(ctx context.Context, info ReleaseInfo) (string, error) {
	tag := "v" + info.VersionName
	target := info.CommitTo
	if target == "" {
		return "", fmt.Errorf("commit_to is empty; cannot create tag")
	}
	if err := a.Gitea.EnsureTag(ctx, tag, target, "devmaster release "+info.VersionName); err != nil {
		return "", err
	}
	return tag, nil
}

func (a *Activities) MarkReleaseTagged(ctx context.Context, input ReleaseInput, tag string) error {
	_, err := a.Versions.MarkTagged(ctx, a.workflowActor(input), input.ReleaseID, tag)
	return err
}

func (a *Activities) PublishRelease(ctx context.Context, input ReleaseInput, evidenceRef string) error {
	_, err := a.Versions.Publish(ctx, a.workflowActor(input), input.ReleaseID, evidenceRef)
	if err == nil && a.Notifier != nil {
		version, getErr := a.Versions.Get(ctx, a.workflowActor(input), input.VersionID)
		release, releaseErr := a.Versions.GetRelease(ctx, a.workflowActor(input), input.ReleaseID)
		if getErr == nil && releaseErr == nil {
			tag := ""
			if release.Tag != nil {
				tag = *release.Tag
			}
			if notifyErr := a.Notifier.ReleasePublished(ctx, input.VersionID.String(), version.Name, tag); notifyErr != nil && a.Logger != nil {
				a.Logger.Warn("release published notification failed", "error", notifyErr)
			}
		}
	}
	return err
}

func (a *Activities) FailRelease(ctx context.Context, input ReleaseInput, reason string) error {
	_, err := a.Versions.Fail(ctx, a.workflowActor(input), input.ReleaseID, reason)
	if err == nil && a.Notifier != nil {
		// 通知是旁路：失败只记日志，不改发布结果。
		if version, getErr := a.Versions.Get(ctx, a.workflowActor(input), input.VersionID); getErr == nil {
			if notifyErr := a.Notifier.ReleaseFailed(ctx, input.VersionID.String(), version.Name, reason); notifyErr != nil && a.Logger != nil {
				a.Logger.Warn("release failure notification failed", "error", notifyErr)
			}
		}
	}
	return err
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
