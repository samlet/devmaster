package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	giteaapp "github.com/xiaofeiwu/devmaster/backend/internal/gitea/application"
)

type fakeGitea struct {
	deliveries map[string]bool
	pushed     []giteaapp.Commit
	prs        []giteaapp.PullRequest
}

func (f *fakeGitea) RecordDelivery(_ context.Context, _ uuid.UUID, deliveryID, _ string, _ []byte) (bool, error) {
	if f.deliveries == nil {
		f.deliveries = map[string]bool{}
	}
	if f.deliveries[deliveryID] {
		return false, nil
	}
	f.deliveries[deliveryID] = true
	return true, nil
}

func (f *fakeGitea) HandlePush(_ context.Context, _ uuid.UUID, _, _ string, commits []giteaapp.Commit) (int, error) {
	f.pushed = append(f.pushed, commits...)
	return len(commits), nil
}

func (f *fakeGitea) HandlePullRequest(_ context.Context, _ uuid.UUID, _ string, pr giteaapp.PullRequest) (int, error) {
	f.prs = append(f.prs, pr)
	return 1, nil
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func newWebhookServer(fake *fakeGitea, secret string) *Server {
	return &Server{
		gitea:       fake,
		giteaSecret: secret,
		giteaOrg:    uuid.New(),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestGiteaWebhookRejectsBadSignature(t *testing.T) {
	server := newWebhookServer(&fakeGitea{}, "secret")
	body := []byte(`{"ref":"refs/heads/main"}`)
	request := httptest.NewRequest(http.MethodPost, "/integrations/gitea/webhook", bytes.NewReader(body))
	request.Header.Set("X-Gitea-Event", "push")
	request.Header.Set("X-Gitea-Delivery", "d1")
	request.Header.Set("X-Gitea-Signature", "deadbeef")
	recorder := httptest.NewRecorder()
	server.handleGiteaWebhook(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d，want 401", recorder.Code)
	}
}

func TestGiteaWebhookPushIsIdempotent(t *testing.T) {
	fake := &fakeGitea{}
	server := newWebhookServer(fake, "secret")
	body, _ := json.Marshal(map[string]any{
		"ref":        "refs/heads/main",
		"repository": map[string]string{"full_name": "meshadmin/hosp"},
		"commits": []map[string]any{{
			"id":        "abc123",
			"message":   "HOSP-1 fix login",
			"timestamp": "2026-10-28T00:00:00Z",
			"author":    map[string]string{"username": "alice"},
		}},
	})

	post := func() int {
		request := httptest.NewRequest(http.MethodPost, "/integrations/gitea/webhook", bytes.NewReader(body))
		request.Header.Set("X-Gitea-Event", "push")
		request.Header.Set("X-Gitea-Delivery", "delivery-1")
		request.Header.Set("X-Gitea-Signature", sign("secret", body))
		recorder := httptest.NewRecorder()
		server.handleGiteaWebhook(recorder, request)
		return recorder.Code
	}
	if code := post(); code != http.StatusOK {
		t.Fatalf("首次 status = %d", code)
	}
	if len(fake.pushed) != 1 {
		t.Fatalf("push 处理 %d 次，want 1", len(fake.pushed))
	}
	// 同 delivery 重投：200 但不重复处理。
	if code := post(); code != http.StatusOK {
		t.Fatalf("重投 status = %d", code)
	}
	if len(fake.pushed) != 1 {
		t.Fatalf("重投不应重复处理，got %d", len(fake.pushed))
	}
}

func TestGiteaWebhookPullRequestMapsMerged(t *testing.T) {
	fake := &fakeGitea{}
	server := newWebhookServer(fake, "secret")
	body, _ := json.Marshal(map[string]any{
		"action":     "closed",
		"repository": map[string]string{"full_name": "meshadmin/hosp"},
		"pull_request": map[string]any{
			"number": 7,
			"state":  "closed",
			"merged": true,
			"head":   map[string]string{"ref": "feature/login"},
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/integrations/gitea/webhook", bytes.NewReader(body))
	request.Header.Set("X-Gitea-Event", "pull_request")
	request.Header.Set("X-Gitea-Delivery", "delivery-pr-1")
	request.Header.Set("X-Gitea-Signature", sign("secret", body))
	recorder := httptest.NewRecorder()
	server.handleGiteaWebhook(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if len(fake.prs) != 1 || fake.prs[0].State != "MERGED" || fake.prs[0].HeadBranch != "feature/login" {
		t.Fatalf("PR 解析不对：%+v", fake.prs)
	}
}
