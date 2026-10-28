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

// TestReleaseApprovalPendingIntentReachesNotify 真发一条催办意图给本机 notify，
// 再从收件箱读回来，确认模板渲染出了「等待时长」而不是空串。
//
// 模板里引用了不存在的变量会静默渲染成空串（notify 的 text/template 语义），
// 所以这里必须断言 waited/threshold 真的出现在正文里，否则「催办发了」只是
// 一条没有信息的噪音。
func TestReleaseApprovalPendingIntentReachesNotify(t *testing.T) {
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
	version := "ap-" + versionID[:8]
	if err := client.ReleaseApprovalPending(context.Background(), versionID, version, "1h30m0s", "1h0m0s", "alice"); err != nil {
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
		text := string(body)
		if strings.Contains(text, version) {
			if !strings.Contains(text, "待审批") {
				t.Fatalf("inbox item is not the approval reminder: %s", text)
			}
			if !strings.Contains(text, "1h30m0s") || !strings.Contains(text, "alice") {
				t.Fatalf("reminder body lost waited/submitter: %s", text)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("notify inbox did not receive approval reminder for %s", version)
}
