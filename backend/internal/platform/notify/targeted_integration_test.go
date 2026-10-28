package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

// TestTargetedRecipientOnlyReachesThatInbox 验证「定向」真的定向：
// 指定 user:<anchor> 的通知必须出现在那个收件箱，且**不出现在团队收件箱**。
//
// 只断言「能读到」是不够的——如果收件人被忽略、落回团队箱，那条断言照样通过，
// 而实际上每个团队成员都收到了本该只给一个人的通知。
func TestTargetedRecipientOnlyReachesThatInbox(t *testing.T) {
	if os.Getenv("DEVMASTER_RUN_INTEGRATION") != "1" {
		t.Skip("set DEVMASTER_RUN_INTEGRATION=1")
	}
	client, err := New(config.NotifyConfig{
		Enabled: true, NATSURL: "nats://127.0.0.1:4222",
		Tenant: "devmaster", RecipientID: "team:devmaster",
	})
	if err != nil {
		t.Fatalf("connect notify: %v", err)
	}
	defer client.Close()

	versionID := uuid.NewString()
	version := "tg-" + versionID[:8]
	recipient := "user:" + uuid.NewString()
	if err := client.ReleasePublished(context.Background(), Target{
		VersionID: versionID, Version: version, RecipientID: recipient,
	}, "v"+version); err != nil {
		t.Fatalf("publish intent: %v", err)
	}

	if !waitForInbox(t, recipient, version, 8*time.Second) {
		t.Fatalf("targeted inbox %s did not receive %s", recipient, version)
	}
	// 团队箱不应收到这条：定向通知落到团队箱意味着收件人被忽略了。
	if inboxContains(t, "team:devmaster", version) {
		t.Fatalf("targeted notification leaked into the team inbox")
	}
}

func waitForInbox(t *testing.T, recipient, needle string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if inboxContains(t, recipient, needle) {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

func inboxContains(t *testing.T, recipient, needle string) bool {
	t.Helper()
	endpoint := "http://127.0.0.1:18091/v1/inbox?tenant=devmaster&recipient_id=" + url.QueryEscape(recipient)
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatalf("read inbox %s: %v", recipient, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if !json.Valid(body) {
		t.Fatalf("inbox %s returned non-JSON: %s", recipient, strings.TrimSpace(string(body)))
	}
	return strings.Contains(string(body), needle)
}
