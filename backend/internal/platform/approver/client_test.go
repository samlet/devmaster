package approver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/approver"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

// 用 approver 端点的真实 JSON 形状（HumanTaskPendingAssigneesEndpointTest 断言的
// 就是它）验证客户端解析。字段名对不上时这里会立刻红，而不是等到线上催办
// 静默降级到角色才发现。
func TestPendingAssigneesParsesApproverPayload(t *testing.T) {
	var seenPath, seenInstance, seenTenant, seenUser string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenInstance = r.URL.Query().Get("processInstanceId")
		seenTenant = r.Header.Get("X-Tenant-Id")
		seenUser = r.Header.Get("X-User-Id")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"processInstanceId": seenInstance,
			"assignees":         []string{"alice", "bob"},
		})
	}))
	defer server.Close()

	client := approver.New(config.ApproverConfig{
		BaseURL: server.URL, TenantID: "tenant-a", ServiceUser: "devmaster-service",
	})
	assignees, err := client.PendingAssignees(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("pending assignees: %v", err)
	}
	if len(assignees) != 2 || assignees[0] != "alice" || assignees[1] != "bob" {
		t.Fatalf("assignees=%v", assignees)
	}
	if seenPath != "/api/v1/tasks/pending-assignees" {
		t.Errorf("path=%q", seenPath)
	}
	if seenInstance != "instance-1" {
		t.Errorf("processInstanceId=%q", seenInstance)
	}
	if seenTenant != "tenant-a" || seenUser != "devmaster-service" {
		t.Errorf("headers tenant=%q user=%q", seenTenant, seenUser)
	}
}

// 空列表是正常结果（实例当前没有待办），不是错误。
func TestPendingAssigneesAcceptsEmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"processInstanceId": "instance-1",
			"assignees":         []string{},
		})
	}))
	defer server.Close()

	client := approver.New(config.ApproverConfig{BaseURL: server.URL, TenantID: "t", ServiceUser: "s"})
	assignees, err := client.PendingAssignees(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("empty list must not error: %v", err)
	}
	if len(assignees) != 0 {
		t.Fatalf("assignees=%v", assignees)
	}
}

// 非 2xx 必须报错——调用方据此降级到角色，而不是当成「没有待办人」。
func TestPendingAssigneesReportsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()

	client := approver.New(config.ApproverConfig{BaseURL: server.URL, TenantID: "t", ServiceUser: "s"})
	if _, err := client.PendingAssignees(context.Background(), "instance-1"); err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
}
