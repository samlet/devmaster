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

func TestTransitionRequiresExpectedVersion(t *testing.T) {
	items := &fakeWorkItems{}
	server := transitionServer(items)
	body := validTransitionBody()
	body["expected_version"] = 0

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
