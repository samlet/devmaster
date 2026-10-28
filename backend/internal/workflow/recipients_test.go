package workflow

import (
	"context"
	"errors"
	"testing"

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
		ReleaseInput{SubmittedBy: "alice"}, ReleaseInfo{VersionName: "1.4.0"}, "1h0m0s", "1h0m0s"); err != nil {
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
