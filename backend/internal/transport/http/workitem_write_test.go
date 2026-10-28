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
	getErr     error
	version    int
	updateErr  error
}

// Get/GetByKey 返回固定的一条：单条路径现在用 key，handler 会先解析再写。
func (f *fakeWorkItems) Get(_ context.Context, _ workitemapp.Actor, id uuid.UUID) (workitemdomain.WorkItem, error) {
	return f.current()
}

func (f *fakeWorkItems) GetByKey(_ context.Context, _ workitemapp.Actor, key string) (workitemdomain.WorkItem, error) {
	if f.getErr != nil {
		return workitemdomain.WorkItem{}, f.getErr
	}
	item, err := f.current()
	if err != nil {
		return workitemdomain.WorkItem{}, err
	}
	item.Key = key
	return item, nil
}

func (f *fakeWorkItems) current() (workitemdomain.WorkItem, error) {
	if f.getErr != nil {
		return workitemdomain.WorkItem{}, f.getErr
	}
	return workitemdomain.WorkItem{
		ID: uuid.MustParse(testWorkItemID), Key: "DEMO-1",
		Status: workitemdomain.StatusInProgress, Version: 3,
	}, nil
}

func (f *fakeWorkItems) List(context.Context, workitemapp.Actor, workitemapp.ListFilter) ([]workitemdomain.WorkItem, int, error) {
	return nil, 0, nil
}

func (f *fakeWorkItems) ListComments(context.Context, workitemapp.Actor, uuid.UUID) ([]workitemdomain.Comment, error) {
	return nil, nil
}

func (f *fakeWorkItems) ListLinks(context.Context, workitemapp.Actor, uuid.UUID) ([]workitemdomain.Link, error) {
	return nil, nil
}

func (f *fakeWorkItems) ListEvents(context.Context, workitemapp.Actor, uuid.UUID) ([]workitemapp.EventRecord, error) {
	return nil, nil
}

func (f *fakeWorkItems) AddComment(_ context.Context, actor workitemapp.Actor, workItemID uuid.UUID, body string) (workitemdomain.Comment, error) {
	f.calls2 = append(f.calls2, "comment")
	if f.err != nil {
		return workitemdomain.Comment{}, f.err
	}
	return workitemdomain.Comment{ID: uuid.New(), WorkItemID: workItemID, AuthorID: actor.Subject, BodyMD: body}, nil
}

func (f *fakeWorkItems) AddLink(context.Context, workitemapp.Actor, uuid.UUID, uuid.UUID, workitemdomain.LinkType) (workitemdomain.Link, error) {
	f.calls2 = append(f.calls2, "link")
	return workitemdomain.Link{}, f.err
}

func (f *fakeWorkItems) RemoveLink(context.Context, workitemapp.Actor, uuid.UUID, uuid.UUID, workitemdomain.LinkType) error {
	f.calls2 = append(f.calls2, "unlink")
	return f.err
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

// fakeWorkItems 同时满足读端口与写端口：单条路径先用 key 解析（读），再写。
func transitionServer(items *fakeWorkItems) *Server {
	return &Server{
		workItems:             items,
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
	request.SetPathValue("key", "DEMO-1")
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
	request.SetPathValue("key", "DEMO-1")

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
	request.SetPathValue("key", "DEMO-1")
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
	request.SetPathValue("key", "DEMO-1")
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

func detailRequest(t *testing.T, key string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/work-items/"+key, nil)
	request.SetPathValue("key", key)
	return request
}

// 详情一次返回四块内容：拆分会让页面变慢、错误态变多。
func TestWorkItemDetailReturnsEverything(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleWorkItemDetail(recorder, detailRequest(t, "DEMO-1"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Item     map[string]any   `json:"item"`
		Comments []map[string]any `json:"comments"`
		Links    map[string]any   `json:"links"`
		Events   []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Item["key"] != "DEMO-1" {
		t.Fatalf("item=%v", response.Item)
	}
	// 即使为空也要是数组/对象，不能是 null——前端少一层判空。
	if response.Comments == nil || response.Links == nil || response.Events == nil {
		t.Fatalf("empty collections must be [], got %+v", response)
	}
}

func TestWorkItemDetailMapsNotFound(t *testing.T) {
	items := &fakeWorkItems{getErr: apperr.NotFound("work item not found")}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleWorkItemDetail(recorder, detailRequest(t, "NOPE-1"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func signedPost(t *testing.T, path string, body map[string]any, setKey bool) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	if setKey {
		request.SetPathValue("key", "DEMO-1")
	}
	request.Header.Set("X-BFF-Timestamp", timestamp)
	request.Header.Set("X-BFF-Signature", signBFF(testBFFSecret, timestamp, payload))
	return request
}

func TestAddCommentCreatesComment(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleAddComment(recorder, signedPost(t, "/api/work-items/DEMO-1/comments",
		map[string]any{"body_md": "看起来是缓存问题", "actor": "alice"}, true))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		AuthorID string `json:"author_id"`
		BodyMD   string `json:"body_md"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.AuthorID != "alice" || response.BodyMD != "看起来是缓存问题" {
		t.Fatalf("response=%+v", response)
	}
}

func TestAddCommentRejectsEmptyBody(t *testing.T) {
	items := &fakeWorkItems{err: apperr.Invalid("body_md", "must not be empty")}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleAddComment(recorder, signedPost(t, "/api/work-items/DEMO-1/comments",
		map[string]any{"body_md": "  ", "actor": "alice"}, true))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
}

func TestAddLinkRejectsUnknownType(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleAddLink(recorder, signedPost(t, "/api/work-items/DEMO-1/links",
		map[string]any{"target_key": "DEMO-2", "type": "NOPE", "actor": "alice"}, true))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(items.calls2) != 0 {
		t.Fatal("service was called with an unknown link type")
	}
}

func TestAddLinkCreatesLink(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)

	recorder := httptest.NewRecorder()
	server.handleAddLink(recorder, signedPost(t, "/api/work-items/DEMO-1/links",
		map[string]any{"target_key": "DEMO-2", "type": "BLOCKED_BY", "actor": "alice"}, true))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(items.calls2) != 1 || items.calls2[0] != "link" {
		t.Fatalf("calls=%v", items.calls2)
	}
}
