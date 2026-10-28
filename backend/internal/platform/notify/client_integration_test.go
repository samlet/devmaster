package notify

import (
	"context"
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

// TestReleaseFailedIntentReachesNotify 真发一条意图给本机 notify，再从收件箱读回来。
// 需要 notifyd 在 18091、NATS 在 4222，且模板已迁移。
func TestReleaseFailedIntentReachesNotify(t *testing.T) {
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
	version := "it-" + versionID[:8]
	if err := client.ReleaseFailed(context.Background(), Target{
		VersionID: versionID, Version: version,
	}, "integration test"); err != nil {
		t.Fatalf("publish intent: %v", err)
	}

	inbox := "http://127.0.0.1:18091/v1/inbox?tenant=devmaster&recipient_id=" +
		url.QueryEscape("team:devmaster")
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(inbox)
		if err != nil {
			t.Fatalf("read inbox: %v", err)
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if strings.Contains(string(body), version) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("notify inbox did not receive intent for %s", version)
}
