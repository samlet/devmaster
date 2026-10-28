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
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	versionapp "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
	versiondomain "github.com/xiaofeiwu/devmaster/backend/internal/version/domain"
)

const testBFFSecret = "test-bff-secret-at-least-32-bytes-long"

type fakeReleases struct {
	calls   int
	err     error
	release versiondomain.ReleaseRecord
}

func (f *fakeReleases) SubmitRelease(_ context.Context, _ versionapp.Actor, cmd versionapp.SubmitReleaseCommand) (versiondomain.ReleaseRecord, error) {
	f.calls++
	if f.err != nil {
		return versiondomain.ReleaseRecord{}, f.err
	}
	return versiondomain.ReleaseRecord{ID: uuid.MustParse("11111111-1111-7111-8111-111111111111"), Status: versiondomain.ReleasePendingApproval}, nil
}

func testServer(releases ReleaseSubmitter, secret string) *Server {
	return &Server{
		releases:              releases,
		bffSecret:             secret,
		logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		defaultOrganizationID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	}
}

func signBFF(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func submitRequest(t *testing.T, secret string, body map[string]any) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/api/releases", bytes.NewReader(payload))
	request.Header.Set("X-BFF-Timestamp", timestamp)
	request.Header.Set("X-BFF-Signature", signBFF(secret, timestamp, payload))
	return request
}

func validSubmitBody() map[string]any {
	return map[string]any{
		"version_id":   "22222222-2222-7222-8222-222222222222",
		"commit_from":  "abc1234",
		"commit_to":    "def5678",
		"submitted_by": "local-dev",
	}
}

func TestSubmitReleaseAcceptsSignedRequest(t *testing.T) {
	releases := &fakeReleases{}
	server := testServer(releases, testBFFSecret)

	recorder := httptest.NewRecorder()
	server.handleSubmitRelease(recorder, submitRequest(t, testBFFSecret, validSubmitBody()))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if releases.calls != 1 {
		t.Fatalf("SubmitRelease calls=%d", releases.calls)
	}
	var response struct {
		ReleaseID string `json:"release_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ReleaseID != "11111111-1111-7111-8111-111111111111" {
		t.Errorf("release_id=%q", response.ReleaseID)
	}
}

// 未配置签名密钥时必须一律拒绝：写端点不能有「没配就放行」的降级。
func TestSubmitReleaseRejectsWhenSecretMissing(t *testing.T) {
	releases := &fakeReleases{}
	server := testServer(releases, "")

	recorder := httptest.NewRecorder()
	server.handleSubmitRelease(recorder, submitRequest(t, "", validSubmitBody()))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
	if releases.calls != 0 {
		t.Fatal("service was called without a configured secret")
	}
}

func TestSubmitReleaseRejectsBadSignature(t *testing.T) {
	releases := &fakeReleases{}
	server := testServer(releases, testBFFSecret)

	payload, _ := json.Marshal(validSubmitBody())
	request := httptest.NewRequest(http.MethodPost, "/api/releases", bytes.NewReader(payload))
	request.Header.Set("X-BFF-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	request.Header.Set("X-BFF-Signature", signBFF("wrong-secret", request.Header.Get("X-BFF-Timestamp"), payload))

	recorder := httptest.NewRecorder()
	server.handleSubmitRelease(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
	if releases.calls != 0 {
		t.Fatal("service was called with an invalid signature")
	}
}

// 过期时间戳必须拒绝：写请求的重放代价比读大得多。
func TestSubmitReleaseRejectsStaleTimestamp(t *testing.T) {
	releases := &fakeReleases{}
	server := testServer(releases, testBFFSecret)

	payload, _ := json.Marshal(validSubmitBody())
	stale := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/api/releases", bytes.NewReader(payload))
	request.Header.Set("X-BFF-Timestamp", stale)
	request.Header.Set("X-BFF-Signature", signBFF(testBFFSecret, stale, payload))

	recorder := httptest.NewRecorder()
	server.handleSubmitRelease(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestSubmitReleaseRejectsMalformedInput(t *testing.T) {
	cases := map[string]map[string]any{
		"bad version id": {"version_id": "not-a-uuid", "submitted_by": "local-dev"},
		"missing submitter": {
			"version_id": "22222222-2222-7222-8222-222222222222",
		},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			releases := &fakeReleases{}
			server := testServer(releases, testBFFSecret)
			recorder := httptest.NewRecorder()
			server.handleSubmitRelease(recorder, submitRequest(t, testBFFSecret, body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if releases.calls != 0 {
				t.Fatal("service was called with malformed input")
			}
		})
	}
}

// 已有在途发布是状态问题不是请求问题：422 而不是 400，客户端据此提示而不是改参数。
func TestSubmitReleaseMapsPreconditionToUnprocessable(t *testing.T) {
	releases := &fakeReleases{err: apperr.FailedPrecondition("release", "a release attempt is already in flight")}
	server := testServer(releases, testBFFSecret)

	recorder := httptest.NewRecorder()
	server.handleSubmitRelease(recorder, submitRequest(t, testBFFSecret, validSubmitBody()))

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSubmitReleaseRejectsNonPost(t *testing.T) {
	server := testServer(&fakeReleases{}, testBFFSecret)
	recorder := httptest.NewRecorder()
	server.handleSubmitRelease(recorder, httptest.NewRequest(http.MethodGet, "/api/releases", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", recorder.Code)
	}
}
