package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// newReleaseEnv 建一个注册好工作流与活动名的测试环境。活动本身全部走 mock，
// 注册只是为了名字能被解析（不注册的话 OnActivity 会 panic）。
func newReleaseEnv() *testsuite.TestWorkflowEnvironment {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ReleaseWorkflow)
	env.RegisterActivity(&Activities{})
	return env
}

func testReleaseInput() ReleaseInput {
	return ReleaseInput{
		OrganizationID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		ReleaseID:      uuid.MustParse("11111111-1111-7111-8111-111111111111"),
		VersionID:      uuid.MustParse("22222222-2222-7222-8222-222222222222"),
		SubmittedBy:    "alice",
		PollInterval:   10 * time.Minute,
		PollTimeout:    24 * time.Hour,
		ReminderAfter:  time.Hour,
	}
}

func testReleaseInfo() ReleaseInfo {
	return ReleaseInfo{
		VersionName: "1.4.0", ProductCode: "HOSP",
		CommitFrom: "abc1234", CommitTo: "def5678",
		NotesRef: "content-1:revision-1", ItemCount: 3,
	}
}

// stubHappyPathTail 注册审批通过之后的全部活动。
func stubHappyPathTail(env *testsuite.TestWorkflowEnvironment, input ReleaseInput, info ReleaseInfo, approval ApprovalResult) {
	env.OnActivity("ApproveRelease", mock.Anything, input, "bob").Return(nil)
	env.OnActivity("CreateReleaseTag", mock.Anything, info).Return("v1.4.0", nil)
	env.OnActivity("MarkReleaseTagged", mock.Anything, input, "v1.4.0").Return(nil)
	env.OnActivity("SealEvidence", mock.Anything, input, info, approval, "v1.4.0").
		Return(EvidenceResult{Ref: "content-9:revision-9"}, nil)
	env.OnActivity("PublishRelease", mock.Anything, input, "content-9:revision-9").Return(nil)
}

// 审批拖着不办时只催一次：反复催办会让审批人对整个通知通道脱敏。
func TestReleaseWorkflowRemindsApprovalOnlyOnce(t *testing.T) {
	env := newReleaseEnv()

	input, info := testReleaseInput(), testReleaseInfo()
	approved := ApprovalResult{Status: "COMPLETED", EndResult: "APPROVED", ApproverID: "bob"}

	env.OnActivity("CheckReleaseSubmittable", mock.Anything, input).Return(info, nil)
	env.OnActivity("StartReleaseApproval", mock.Anything, input, info).Return("instance-1", nil)
	// 每 10 分钟轮询一次：t=0..60 都还在审，t=70 通过。60 分钟正好到催办阈值。
	env.OnActivity("PollReleaseApproval", mock.Anything, "instance-1").
		Return(ApprovalResult{Status: "RUNNING"}, nil).Times(7)
	env.OnActivity("PollReleaseApproval", mock.Anything, "instance-1").Return(approved, nil).Once()
	env.OnActivity("NotifyApprovalPending", mock.Anything, input, info, "instance-1", "1h0m0s", "1h0m0s").Return(nil).Once()
	stubHappyPathTail(env, input, info, approved)

	env.ExecuteWorkflow(ReleaseWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// 阈值不该超过整体超时，否则提醒永远发不出来；工作流要把阈值压回一半。
func TestReleaseWorkflowClampsReminderBelowTimeout(t *testing.T) {
	env := newReleaseEnv()

	input, info := testReleaseInput(), testReleaseInfo()
	input.PollInterval = 10 * time.Minute
	input.PollTimeout = 2 * time.Hour
	input.ReminderAfter = 5 * time.Hour // 明显不合理
	approved := ApprovalResult{Status: "COMPLETED", EndResult: "APPROVED", ApproverID: "bob"}

	env.OnActivity("CheckReleaseSubmittable", mock.Anything, input).Return(info, nil)
	env.OnActivity("StartReleaseApproval", mock.Anything, input, info).Return("instance-1", nil)
	env.OnActivity("PollReleaseApproval", mock.Anything, "instance-1").
		Return(ApprovalResult{Status: "RUNNING"}, nil).Times(7)
	env.OnActivity("PollReleaseApproval", mock.Anything, "instance-1").Return(approved, nil).Once()
	// 压到 poll_timeout/2 = 1h，所以在 t=60 分钟催办。
	env.OnActivity("NotifyApprovalPending", mock.Anything, input, info, "instance-1", "1h0m0s", "1h0m0s").Return(nil).Once()
	stubHappyPathTail(env, input, info, approved)

	env.ExecuteWorkflow(ReleaseWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// 超时必须落失败记录，且失败原因带上等了多久——只说「超时」查不出是卡在哪。
func TestReleaseWorkflowTimesOutWithDuration(t *testing.T) {
	env := newReleaseEnv()

	input, info := testReleaseInput(), testReleaseInfo()
	input.PollInterval = 30 * time.Minute
	input.PollTimeout = 2 * time.Hour
	input.ReminderAfter = time.Hour

	env.OnActivity("CheckReleaseSubmittable", mock.Anything, input).Return(info, nil)
	env.OnActivity("StartReleaseApproval", mock.Anything, input, info).Return("instance-1", nil)
	env.OnActivity("PollReleaseApproval", mock.Anything, "instance-1").
		Return(ApprovalResult{Status: "RUNNING"}, nil)
	env.OnActivity("NotifyApprovalPending", mock.Anything, input, info, "instance-1", mock.Anything, "1h0m0s").Return(nil)
	env.OnActivity("FailRelease", mock.Anything, input, mock.MatchedBy(func(reason string) bool {
		return strings.Contains(reason, "timed out after")
	})).Return(nil)

	env.ExecuteWorkflow(ReleaseWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out after")
	env.AssertExpectations(t)
}

// 催办失败不能把发布搞挂：通知是旁路。
func TestReleaseWorkflowSurvivesReminderFailure(t *testing.T) {
	env := newReleaseEnv()

	input, info := testReleaseInput(), testReleaseInfo()
	approved := ApprovalResult{Status: "COMPLETED", EndResult: "APPROVED", ApproverID: "bob"}

	env.OnActivity("CheckReleaseSubmittable", mock.Anything, input).Return(info, nil)
	env.OnActivity("StartReleaseApproval", mock.Anything, input, info).Return("instance-1", nil)
	env.OnActivity("PollReleaseApproval", mock.Anything, "instance-1").
		Return(ApprovalResult{Status: "RUNNING"}, nil).Times(7)
	env.OnActivity("PollReleaseApproval", mock.Anything, "instance-1").Return(approved, nil).Once()
	env.OnActivity("NotifyApprovalPending", mock.Anything, input, info, "instance-1", mock.Anything, mock.Anything).
		Return(errTestNotifyDown)
	stubHappyPathTail(env, input, info, approved)

	env.ExecuteWorkflow(ReleaseWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

type notifyDownError struct{}

func (notifyDownError) Error() string { return "notify is down" }

var errTestNotifyDown = notifyDownError{}
