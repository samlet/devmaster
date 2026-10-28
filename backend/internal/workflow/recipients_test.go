package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/approver"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/notify"
)

type fakeResolver struct {
	anchorID string
	found    bool
	err      error
	calls    []string
}

func (f *fakeResolver) ResolveSubject(_ context.Context, sourceSystem, externalID string) (string, bool, error) {
	f.calls = append(f.calls, sourceSystem+"/"+externalID)
	return f.anchorID, f.found, f.err
}

type fakeNotifier struct {
	reminders []notify.ApprovalReminder
	published []notify.Target
	failed    []notify.Target
}

func (f *fakeNotifier) ReleaseFailed(_ context.Context, target notify.Target, _ string) error {
	f.failed = append(f.failed, target)
	return nil
}

func (f *fakeNotifier) ReleasePublished(_ context.Context, target notify.Target, _ string) error {
	f.published = append(f.published, target)
	return nil
}

func (f *fakeNotifier) ReleaseApprovalPending(_ context.Context, reminder notify.ApprovalReminder) error {
	f.reminders = append(f.reminders, reminder)
	return nil
}

// 主体目录未接入时返回空串，由 notify 客户端落到团队收件箱——显式降级。
func TestRecipientForWithoutRegistry(t *testing.T) {
	activities := &Activities{}
	if got := activities.recipientFor(context.Background(), "sub-alice"); got != "" {
		t.Fatalf("recipient=%q want empty", got)
	}
}

// 命中时用 anchor_id 而不是 Keycloak sub：换 IdP 后收件人不失效。
func TestRecipientForResolvesAnchor(t *testing.T) {
	resolver := &fakeResolver{anchorID: "anchor-1", found: true}
	activities := &Activities{Subjects: resolver}
	got := activities.recipientFor(context.Background(), "sub-alice")
	if got != "user:anchor-1" {
		t.Fatalf("recipient=%q", got)
	}
	if len(resolver.calls) != 1 || resolver.calls[0] != "keycloak/sub-alice" {
		t.Fatalf("calls=%v", resolver.calls)
	}
}

// 未登记返回空串（不是错误）：调用方据此降级，不重试。
func TestRecipientForNotFoundFallsBack(t *testing.T) {
	activities := &Activities{Subjects: &fakeResolver{found: false}}
	if got := activities.recipientFor(context.Background(), "sub-unknown"); got != "" {
		t.Fatalf("recipient=%q want empty", got)
	}
}

// 主体目录故障不能让通知发不出去：降级到团队收件箱，不返回错误。
func TestRecipientForSurvivesRegistryFailure(t *testing.T) {
	activities := &Activities{Subjects: &fakeResolver{err: errors.New("registry down")}}
	if got := activities.recipientFor(context.Background(), "sub-alice"); got != "" {
		t.Fatalf("recipient=%q want empty", got)
	}
}

// 空主体不查目录：本地 demo 或系统触发的发布没有提交人。
func TestRecipientForSkipsEmptySubject(t *testing.T) {
	resolver := &fakeResolver{anchorID: "anchor-1", found: true}
	activities := &Activities{Subjects: resolver}
	if got := activities.recipientFor(context.Background(), "   "); got != "" {
		t.Fatalf("recipient=%q want empty", got)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("empty subject triggered a lookup: %v", resolver.calls)
	}
}

// 催办发给角色而不是具体人：审批完成前拿不到审批人 id，
// 猜一个人比发给角色更糟（猜错就是信息泄露）。
func TestNotifyApprovalPendingTargetsApprovalRole(t *testing.T) {
	notifier := &fakeNotifier{}
	activities := &Activities{Notifier: notifier, Subjects: &fakeResolver{anchorID: "anchor-1", found: true}}
	if err := activities.NotifyApprovalPending(context.Background(),
		ReleaseInput{SubmittedBy: "alice"}, ReleaseInfo{VersionName: "1.4.0"}, "instance-1", "1h0m0s", "1h0m0s"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(notifier.reminders) != 1 {
		t.Fatalf("reminders=%d", len(notifier.reminders))
	}
	reminder := notifier.reminders[0]
	if reminder.RecipientID != "role:RELEASE_APPROVER" {
		t.Fatalf("recipient=%q", reminder.RecipientID)
	}
	if reminder.SubmittedBy != "alice" || reminder.Waited != "1h0m0s" {
		t.Fatalf("reminder=%+v", reminder)
	}
}

// fakeApprover 只实现待办人查询；其余方法在本组用例里不会被调用。
type fakeApprover struct {
	assignees []string
	err       error
}

func (f *fakeApprover) StartProcess(context.Context, string, string, map[string]any) (string, error) {
	return "", nil
}

func (f *fakeApprover) GetInstance(context.Context, string) (approver.Instance, error) {
	return approver.Instance{}, nil
}

func (f *fakeApprover) Events(context.Context, string) ([]approver.Event, error) {
	return nil, nil
}

func (f *fakeApprover) PendingAssignees(context.Context, string) ([]string, error) {
	return f.assignees, f.err
}

// mappingResolver 按 "source/external" 精确返回锚点，用来验证双 source 回退顺序。
type mappingResolver struct {
	mappings map[string]string
	err      error
	calls    []string
}

func (m *mappingResolver) ResolveSubject(_ context.Context, sourceSystem, externalID string) (string, bool, error) {
	key := sourceSystem + "/" + externalID
	m.calls = append(m.calls, key)
	if m.err != nil {
		return "", false, m.err
	}
	anchorID, ok := m.mappings[key]
	return anchorID, ok, nil
}

// 未接入 approver 时降级到审批角色：这条是最关键的安全行为——
// 宁可多人看到，也不能把催办发给猜出来的人。
func TestApprovalRecipientsFallsBackWithoutApprover(t *testing.T) {
	activities := &Activities{}
	got := activities.approvalRecipients(context.Background(), "instance-1")
	if len(got) != 1 || got[0] != "role:RELEASE_APPROVER" {
		t.Fatalf("recipients=%v", got)
	}
}

// 实例当前没有待办（已办完或还没到人工节点）：同样降级。
func TestApprovalRecipientsFallsBackWhenNoPendingTask(t *testing.T) {
	activities := &Activities{Approver: &fakeApprover{assignees: nil}}
	got := activities.approvalRecipients(context.Background(), "instance-1")
	if len(got) != 1 || got[0] != "role:RELEASE_APPROVER" {
		t.Fatalf("recipients=%v", got)
	}
}

// 查询待办人失败不能让催办消失，降级到角色继续发。
func TestApprovalRecipientsFallsBackOnQueryError(t *testing.T) {
	activities := &Activities{Approver: &fakeApprover{err: errors.New("approver down")}}
	got := activities.approvalRecipients(context.Background(), "instance-1")
	if len(got) != 1 || got[0] != "role:RELEASE_APPROVER" {
		t.Fatalf("recipients=%v", got)
	}
}

// 待办人能解析就逐个定向：多个审批人各发一条。
func TestApprovalRecipientsResolvesEveryAssignee(t *testing.T) {
	resolver := &mappingResolver{mappings: map[string]string{
		"approver/alice": "anchor-alice",
		"approver/bob":   "anchor-bob",
	}}
	activities := &Activities{
		Approver: &fakeApprover{assignees: []string{"alice", "bob"}},
		Subjects: resolver,
	}
	got := activities.approvalRecipients(context.Background(), "instance-1")
	want := []string{"user:anchor-alice", "user:anchor-bob"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("recipients=%v want %v", got, want)
	}
}

// 待办人一个都解析不出来时降级，而不是发一半或猜一个。
func TestApprovalRecipientsFallsBackWhenNoneResolvable(t *testing.T) {
	activities := &Activities{
		Approver: &fakeApprover{assignees: []string{"stranger"}},
		Subjects: &mappingResolver{mappings: map[string]string{}},
	}
	got := activities.approvalRecipients(context.Background(), "instance-1")
	if len(got) != 1 || got[0] != "role:RELEASE_APPROVER" {
		t.Fatalf("recipients=%v", got)
	}
}

// 解析顺序：先按 approver 用户 id，再按 Keycloak sub。
// 两种登记方式都要支持——有的部署把 approver 用户绑进目录，有的直接用 OIDC 主体。
func TestRecipientForApproverTriesApproverThenKeycloak(t *testing.T) {
	resolver := &mappingResolver{mappings: map[string]string{
		"keycloak/alice": "anchor-oidc",
	}}
	activities := &Activities{Subjects: resolver}
	if got := activities.recipientForApprover(context.Background(), "alice"); got != "user:anchor-oidc" {
		t.Fatalf("recipient=%q", got)
	}
	wantCalls := []string{"approver/alice", "keycloak/alice"}
	if len(resolver.calls) != 2 || resolver.calls[0] != wantCalls[0] || resolver.calls[1] != wantCalls[1] {
		t.Fatalf("calls=%v want %v", resolver.calls, wantCalls)
	}
}

// 第一个 source 命中就不再查第二个：多一次远程调用没有意义。
func TestRecipientForApproverStopsAtFirstHit(t *testing.T) {
	resolver := &mappingResolver{mappings: map[string]string{
		"approver/alice": "anchor-approver",
		"keycloak/alice": "anchor-oidc",
	}}
	activities := &Activities{Subjects: resolver}
	if got := activities.recipientForApprover(context.Background(), "alice"); got != "user:anchor-approver" {
		t.Fatalf("recipient=%q", got)
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("calls=%v want a single lookup", resolver.calls)
	}
}
