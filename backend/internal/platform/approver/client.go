// Package approver 是 approver 流程平台的 HTTP 客户端。
//
// 只做三件事：起流程、查实例、读事件。业务语义（四眼、超时、失败）留在
// devmaster 的工作流与领域里，客户端不替调用方做判断。
package approver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
)

type Client struct {
	cfg  config.ApproverConfig
	http *http.Client
}

func New(cfg config.ApproverConfig) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
}

type Instance struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	EndResult   string     `json:"endResult"`
	CompletedAt *time.Time `json:"completedAt"`
}

type Event struct {
	EventType string `json:"eventType"`
	NodeID    string `json:"nodeId"`
	ActorID   string `json:"actorId"`
}

type startRequest struct {
	ProcessKey  string         `json:"processKey"`
	BusinessKey string         `json:"businessKey"`
	Variables   map[string]any `json:"variables"`
}

// StartProcess 启动审批实例。businessKey 用 version_id（同一版本可追踪），
// idempotencyKey 用 release_record_id（同一次提交重试复用，新尝试新建）。
func (c *Client) StartProcess(ctx context.Context, businessKey, idempotencyKey string, variables map[string]any) (string, error) {
	payload, err := json.Marshal(startRequest{
		ProcessKey:  c.cfg.ProcessKey,
		BusinessKey: businessKey,
		Variables:   variables,
	})
	if err != nil {
		return "", fmt.Errorf("marshal start request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.cfg.BaseURL, "/")+"/api/v1/process-instances", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	c.setHeaders(request, "PROCESS_STARTER")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)

	var instance Instance
	if err := c.do(request, &instance); err != nil {
		return "", err
	}
	if instance.ID == "" {
		return "", fmt.Errorf("approver returned empty instance id")
	}
	return instance.ID, nil
}

func (c *Client) GetInstance(ctx context.Context, instanceID string) (Instance, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.cfg.BaseURL, "/")+"/api/v1/process-instances/"+instanceID, nil)
	if err != nil {
		return Instance{}, err
	}
	c.setHeaders(request, "PROCESS_READ")
	var instance Instance
	if err := c.do(request, &instance); err != nil {
		return Instance{}, err
	}
	return instance, nil
}

func (c *Client) Events(ctx context.Context, instanceID string) ([]Event, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.cfg.BaseURL, "/")+"/api/v1/process-instances/"+instanceID+"/events", nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(request, "PROCESS_READ")
	var events []Event
	if err := c.do(request, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// PendingAssignees 查流程实例当前还没办的待办人。
//
// 审批完成前 devmaster 拿不到 approver id（实例接口只有状态），
// 这个端点补上「该催谁」。返回空列表表示实例当前没有待办。
func (c *Client) PendingAssignees(ctx context.Context, instanceID string) ([]string, error) {
	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/v1/tasks/pending-assignees?processInstanceId=" +
		neturl.QueryEscape(instanceID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(request, "PROCESS_READ")
	var payload struct {
		Assignees []string `json:"assignees"`
	}
	if err := c.do(request, &payload); err != nil {
		return nil, err
	}
	return payload.Assignees, nil
}

// ApproverFromEvents 从事件流里找出实际审批人（TASK_ACTION_SUBMITTED 的 actor）。
// 四眼校验用：approver 侧的校验是主，devmaster 侧再核对一次是双保险。
func ApproverFromEvents(events []Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventType == "TASK_ACTION_SUBMITTED" && events[i].ActorID != "" {
			return events[i].ActorID
		}
	}
	return ""
}

func (c *Client) setHeaders(request *http.Request, role string) {
	request.Header.Set("X-Tenant-Id", c.cfg.TenantID)
	request.Header.Set("X-User-Id", c.cfg.ServiceUser)
	request.Header.Set("X-Roles", role)
}

func (c *Client) do(request *http.Request, target any) error {
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call approver %s: %w", request.URL.Path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read approver response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("approver %s returned %d: %s", request.URL.Path, response.StatusCode, truncate(string(body), 300))
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode approver response: %w", err)
	}
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
