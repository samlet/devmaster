package http

import (
	"bytes"
	"context"
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
	workitemapp "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

const testWorkItemID = "33333333-3333-7333-8333-333333333333"

type fakeWorkItems struct {
	calls     int
	err       error
	lastActor workitemapp.Actor
	lastCmd   workitemapp.TransitionCommand
	// 编辑路径：记录调用顺序，验证 Update 先于 Assign 且版本号正确接续。
	calls2     []string
	lastUpdate workitemapp.UpdateCommand
	lastAssign workitemapp.AssignCommand
	version    int
	updateErr  error
}

func (f *fakeWorkItems) Create(_ context.Context, actor workitemapp.Actor, cmd workitemapp.CreateCommand) (workitemdomain.WorkItem, error) {
	f.calls++
	f.lastActor = actor
	if f.err != nil {
		return workitemdomain.WorkItem{}, f.err
	}
	return workitemdomain.WorkItem{ID: uuid.New(), Key: "DEMO-1", Status: workitemdomain.StatusBacklog, Version: 1}, nil
}

func (f *fakeWorkItems) Update(_ context.Context, actor workitemapp.Actor, cmd workitemapp.UpdateCommand) (workitemdomain.WorkItem, error) {
	f.calls2 = append(f.calls2, "update")
	f.lastActor = actor
	f.lastUpdate = cmd
	if f.updateErr != nil {
		return workitemdomain.WorkItem{}, f.updateErr
	}
	f.version = cmd.ExpectedVersion + 1
	return workitemdomain.WorkItem{ID: cmd.WorkItemID, Status: workitemdomain.StatusTodo, Version: f.version}, nil
}

func (f *fakeWorkItems) Assign(_ context.Context, actor workitemapp.Actor, cmd workitemapp.AssignCommand) (workitemdomain.WorkItem, error) {
	f.calls2 = append(f.calls2, "assign")
	f.lastActor = actor
	f.lastAssign = cmd
	if f.err != nil {
		return workitemdomain.WorkItem{}, f.err
	}
	return workitemdomain.WorkItem{ID: cmd.WorkItemID, Status: workitemdomain.StatusTodo, Version: cmd.ExpectedVersion + 1}, nil
}

func (f *fakeWorkItems) Transition(_ context.Context, actor workitemapp.Actor, cmd workitemapp.TransitionCommand) (workitemdomain.WorkItem, error) {
	f.calls++
	f.lastActor = actor
	f.lastCmd = cmd
	if f.err != nil {
		return workitemdomain.WorkItem{}, f.err
	}
	return workitemdomain.WorkItem{
		ID:      cmd.WorkItemID,
		Status:  cmd.To,
		Version: cmd.ExpectedVersion + 1,
	}, nil
}

func transitionServer(items WorkItemWriter) *Server {
	return &Server{
		workItemsW:            items,
		bffSecret:             testBFFSecret,
		logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		defaultOrganizationID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	}
}

func transitionRequest(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/api/work-items/"+testWorkItemID+"/transition", bytes.NewReader(payload))
	request.SetPathValue("id", testWorkItemID)
	request.Header.Set("X-BFF-Timestamp", timestamp)
	request.Header.Set("X-BFF-Signature", signBFF(testBFFSecret, timestamp, payload))
	return request
}

func validTransitionBody() map[string]any {
	return map[string]any{
		"to":               "IN_PROGRESS",
		"expected_version": 3,
		"actor":            "alice",
		"actor_roles":      []string{"DEV_MEMBER"},
	}
}

func TestTransitionAcceptsSignedRequest(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleTransitionWorkItem(recorder, transitionRequest(t, validTransitionBody()))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if items.calls != 1 {
		t.Fatalf("Transition calls=%d", items.calls)
	}
	if items.lastActor.Subject != "alice" || len(items.lastActor.Roles) != 1 {
		t.Fatalf("actor=%+v", items.lastActor)
	}
	if items.lastCmd.To != workitemdomain.StatusInProgress || items.lastCmd.ExpectedVersion != 3 {
		t.Fatalf("cmd=%+v", items.lastCmd)
	}
	var response struct {
		Status  string `json:"status"`
		Version int32  `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Status != "IN_PROGRESS" || response.Version != 4 {
		t.Fatalf("response=%+v", response)
	}
}

// actor 必须来自签名覆盖的 body：放 header 就等于留一个可篡改的口子。
func TestTransitionRequiresActorInBody(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)
	body := validTransitionBody()
	delete(body, "actor")

	recorder := httptest.NewRecorder()
	server.handleTransitionWorkItem(recorder, transitionRequest(t, body))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	if items.calls != 0 {
		t.Fatal("service was called without an actor")
	}
}

// 版本 0 是合法值（新建的工作项就是 0），只有「没传」才算缺。
func TestTransitionRequiresExpectedVersion(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)
	body := validTransitionBody()
	delete(body, "expected_version")

	recorder := httptest.NewRecorder()
	server.handleTransitionWorkItem(recorder, transitionRequest(t, body))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	if items.calls != 0 {
		t.Fatal("service was called without expected_version")
	}
}

func TestTransitionRejectsUnknownStatusAndResolution(t *testing.T) {
	cases := map[string]map[string]any{
		"bad status":     {"to": "NOT_A_STATUS", "expected_version": 1, "actor": "alice"},
		"bad resolution": {"to": "RESOLVED", "resolution": "NOPE", "expected_version": 1, "actor": "alice"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			items := &fakeWorkItems{}
			server := transitionServer(items)
			recorder := httptest.NewRecorder()
			server.handleTransitionWorkItem(recorder, transitionRequest(t, body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if items.calls != 0 {
				t.Fatal("service was called with malformed input")
			}
		})
	}
}

// 状态机拒绝是 422：请求本身合法，是当前状态不允许。
func TestTransitionMapsPreconditionToUnprocessable(t *testing.T) {
	items := &fakeWorkItems{err: apperr.FailedPrecondition("assignee_id", "assign the work item before starting it")}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleTransitionWorkItem(recorder, transitionRequest(t, validTransitionBody()))

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

// 并发改同一个工作项时 version 落后 → 409，前端应提示刷新而不是重试。
func TestTransitionMapsConflictToConflictStatus(t *testing.T) {
	items := &fakeWorkItems{err: apperr.Conflict("version", "work item was modified")}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleTransitionWorkItem(recorder, transitionRequest(t, validTransitionBody()))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestTransitionRejectsUnsignedRequest(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	payload, _ := json.Marshal(validTransitionBody())
	request := httptest.NewRequest(http.MethodPost, "/api/work-items/"+testWorkItemID+"/transition", bytes.NewReader(payload))
	request.SetPathValue("id", testWorkItemID)

	recorder := httptest.NewRecorder()
	server.handleTransitionWorkItem(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
	if items.calls != 0 {
		t.Fatal("service was called without a signature")
	}
}

func TestTransitionRejectsNonPost(t *testing.T) {
	server := transitionServer(&fakeWorkItems{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/work-items/"+testWorkItemID+"/transition", nil)
	request.SetPathValue("id", testWorkItemID)
	server.handleTransitionWorkItem(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func createRequest(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/api/work-items", bytes.NewReader(payload))
	request.Header.Set("X-BFF-Timestamp", timestamp)
	request.Header.Set("X-BFF-Signature", signBFF(testBFFSecret, timestamp, payload))
	return request
}

func validCreateBody() map[string]any {
	return map[string]any{
		"product_id": "44444444-4444-7444-8444-444444444444",
		"type":       "TASK",
		"title":      "控制台建的任务",
		"priority":   2,
		"actor":      "alice",
	}
}

func TestCreateWorkItemAcceptsSignedRequest(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleCreateWorkItem(recorder, createRequest(t, validCreateBody()))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if items.calls != 1 || items.lastActor.Subject != "alice" {
		t.Fatalf("calls=%d actor=%+v", items.calls, items.lastActor)
	}
	var response struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Key != "DEMO-1" {
		t.Fatalf("key=%q", response.Key)
	}
}

// reporter 由 actor 决定，客户端不能指定别人——否则能往别人名下塞工作项。
func TestCreateWorkItemRejectsMalformedInput(t *testing.T) {
	cases := map[string]map[string]any{
		"missing title": {"product_id": "44444444-4444-7444-8444-444444444444", "type": "TASK", "actor": "alice"},
		"bad type":      {"product_id": "44444444-4444-7444-8444-444444444444", "type": "NOPE", "title": "x", "actor": "alice"},
		"bad product":   {"product_id": "not-a-uuid", "type": "TASK", "title": "x", "actor": "alice"},
		"missing actor": {"product_id": "44444444-4444-7444-8444-444444444444", "type": "TASK", "title": "x"},
		"bad priority":  {"product_id": "44444444-4444-7444-8444-444444444444", "type": "TASK", "title": "x", "priority": 9, "actor": "alice"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			items := &fakeWorkItems{}
			server := transitionServer(items)
			recorder := httptest.NewRecorder()
			server.handleCreateWorkItem(recorder, createRequest(t, body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if items.calls != 0 {
				t.Fatal("service was called with malformed input")
			}
		})
	}
}

func patchRequest(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPatch, "/api/work-items/"+testWorkItemID, bytes.NewReader(payload))
	request.SetPathValue("id", testWorkItemID)
	request.Header.Set("X-BFF-Timestamp", timestamp)
	request.Header.Set("X-BFF-Signature", signBFF(testBFFSecret, timestamp, payload))
	return request
}

// 编辑经办人必须走 Assign，且用 Update 之后的新版本：两次写都要过 CAS。
func TestUpdateWorkItemAssignsWithRefreshedVersion(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	title := "新标题"
	assignee := "bob"
	recorder := httptest.NewRecorder()
	server.handleUpdateWorkItem(recorder, patchRequest(t, map[string]any{
		"expected_version": 3,
		"title":            title,
		"assignee_id":      assignee,
		"actor":            "alice",
	}))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(items.calls2) != 2 || items.calls2[0] != "update" || items.calls2[1] != "assign" {
		t.Fatalf("calls=%v", items.calls2)
	}
	if items.lastAssign.ExpectedVersion != 4 {
		t.Fatalf("assign used stale version: %d", items.lastAssign.ExpectedVersion)
	}
	if items.lastAssign.AssigneeID == nil || *items.lastAssign.AssigneeID != "bob" {
		t.Fatalf("assignee=%v", items.lastAssign.AssigneeID)
	}
}

// 只改标题时不调 Assign：没有指派意图就不要产生指派事件。
func TestUpdateWorkItemSkipsAssignWhenNotProvided(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleUpdateWorkItem(recorder, patchRequest(t, map[string]any{
		"expected_version": 3,
		"title":            "只改标题",
		"actor":            "alice",
	}))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if len(items.calls2) != 1 || items.calls2[0] != "update" {
		t.Fatalf("calls=%v", items.calls2)
	}
}

// 空串表示取消指派：显式给出才处理。
func TestUpdateWorkItemClearsAssignee(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleUpdateWorkItem(recorder, patchRequest(t, map[string]any{
		"expected_version": 3,
		"assignee_id":      "",
		"actor":            "alice",
	}))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if items.lastAssign.AssigneeID != nil {
		t.Fatalf("assignee should be nil, got %v", items.lastAssign.AssigneeID)
	}
}

func TestUpdateWorkItemRejectsMissingVersion(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)
	recorder := httptest.NewRecorder()
	server.handleUpdateWorkItem(recorder, patchRequest(t, map[string]any{"title": "x", "actor": "alice"}))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	if len(items.calls2) != 0 {
		t.Fatal("service was called without expected_version")
	}
}
