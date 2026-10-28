package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	sprintapp "github.com/xiaofeiwu/devmaster/backend/internal/sprint/application"
	sprintdomain "github.com/xiaofeiwu/devmaster/backend/internal/sprint/domain"
)

type fakeSprints struct {
	createCalls int
	startCalls  int
	addCalls    int
	lastCreate  sprintapp.CreateCommand
	lastItems   []uuid.UUID
	snapshots   []sprintdomain.Snapshot
	err         error
}

func (f *fakeSprints) List(context.Context, sprintapp.Actor, uuid.UUID) ([]sprintdomain.Sprint, error) {
	return []sprintdomain.Sprint{{ID: uuid.New(), Name: "S1", State: sprintdomain.StateActive, Version: 2}}, f.err
}

func (f *fakeSprints) Get(context.Context, sprintapp.Actor, uuid.UUID) (sprintdomain.Sprint, error) {
	return sprintdomain.Sprint{ID: uuid.New(), Name: "S1", State: sprintdomain.StateActive}, f.err
}

func (f *fakeSprints) Stats(context.Context, sprintapp.Actor, uuid.UUID) (sprintdomain.SprintStats, error) {
	return sprintdomain.SprintStats{CommittedPoints: 10, AddedPoints: 3, RemovedPoints: 1, CreepRate: 0.2}, f.err
}

func (f *fakeSprints) Burndown(context.Context, sprintapp.Actor, uuid.UUID) ([]sprintdomain.Snapshot, error) {
	return f.snapshots, f.err
}

func (f *fakeSprints) Create(_ context.Context, _ sprintapp.Actor, cmd sprintapp.CreateCommand) (sprintdomain.Sprint, error) {
	f.createCalls++
	f.lastCreate = cmd
	if f.err != nil {
		return sprintdomain.Sprint{}, f.err
	}
	return sprintdomain.Sprint{ID: uuid.New(), Name: cmd.Name, State: sprintdomain.StatePlanned, Version: 1}, nil
}

func (f *fakeSprints) Start(_ context.Context, _ sprintapp.Actor, sprintID uuid.UUID, _ int) (sprintdomain.Sprint, int16, error) {
	f.startCalls++
	if f.err != nil {
		return sprintdomain.Sprint{}, 0, f.err
	}
	return sprintdomain.Sprint{ID: sprintID, State: sprintdomain.StateActive, Version: 2}, 10, nil
}

func (f *fakeSprints) Close(_ context.Context, _ sprintapp.Actor, cmd sprintapp.CloseCommand) (sprintdomain.Sprint, error) {
	if f.err != nil {
		return sprintdomain.Sprint{}, f.err
	}
	return sprintdomain.Sprint{ID: cmd.SprintID, State: sprintdomain.StateClosed, Version: 3}, nil
}

func (f *fakeSprints) AddItems(_ context.Context, _ sprintapp.Actor, _ uuid.UUID, itemIDs []uuid.UUID) (int, error) {
	f.addCalls++
	f.lastItems = itemIDs
	if f.err != nil {
		return 0, f.err
	}
	return len(itemIDs), nil
}

func (f *fakeSprints) RemoveItems(_ context.Context, _ sprintapp.Actor, _ uuid.UUID, itemIDs []uuid.UUID) (int, error) {
	return len(itemIDs), f.err
}

func sprintServer(sprints *fakeSprints, items *fakeWorkItems) *Server {
	return &Server{
		sprints:               sprints,
		sprintsW:              sprints,
		workItems:             items,
		bffSecret:             testBFFSecret,
		logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		defaultOrganizationID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	}
}

func TestCreateSprintAcceptsSignedRequest(t *testing.T) {
	sprints := &fakeSprints{}
	server := sprintServer(sprints, &fakeWorkItems{})

	recorder := httptest.NewRecorder()
	server.handleCreateSprint(recorder, signedPost(t, "/api/sprints", map[string]any{
		"product_id": "44444444-4444-7444-8444-444444444444",
		"name":       "Sprint 12",
		"goal":       "把工时入口做完",
		"start_at":   "2026-10-28",
		"end_at":     "2026-11-10",
		"actor":      "alice",
	}, false))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if sprints.createCalls != 1 || sprints.lastCreate.Name != "Sprint 12" {
		t.Fatalf("cmd=%+v", sprints.lastCreate)
	}
	if sprints.lastCreate.StartAt == nil || sprints.lastCreate.EndAt == nil {
		t.Fatal("dates were not parsed")
	}
}

// 开始冲刺必须带 expected_version：并发开始要能被 CAS 挡住。
func TestStartSprintRequiresVersion(t *testing.T) {
	sprints := &fakeSprints{}
	server := sprintServer(sprints, &fakeWorkItems{})

	payload := map[string]any{"actor": "alice"}
	request := signedPost(t, "/api/sprints/"+uuid.NewString()+"/start", payload, false)
	request.SetPathValue("id", uuid.NewString())

	recorder := httptest.NewRecorder()
	server.handleStartSprint(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	if sprints.startCalls != 0 {
		t.Fatal("service was called without expected_version")
	}
}

// 范围增删用 key：人在界面上填的是 HOSP-3。
func TestAddSprintItemsResolvesKeys(t *testing.T) {
	sprints := &fakeSprints{}
	server := sprintServer(sprints, &fakeWorkItems{})

	id := uuid.NewString()
	request := signedPost(t, "/api/sprints/"+id+"/items", map[string]any{
		"item_keys": []string{"DEMO-1"}, "actor": "alice",
	}, false)
	request.SetPathValue("id", id)

	recorder := httptest.NewRecorder()
	server.handleAddSprintItems(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if sprints.addCalls != 1 || len(sprints.lastItems) != 1 {
		t.Fatalf("items=%v", sprints.lastItems)
	}
}

func TestAddSprintItemsRejectsUnknownKey(t *testing.T) {
	sprints := &fakeSprints{}
	items := &fakeWorkItems{getErr: sprintNotFound{}}
	server := sprintServer(sprints, items)

	id := uuid.NewString()
	request := signedPost(t, "/api/sprints/"+id+"/items", map[string]any{
		"item_keys": []string{"NOPE-1"}, "actor": "alice",
	}, false)
	request.SetPathValue("id", id)

	recorder := httptest.NewRecorder()
	server.handleAddSprintItems(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if sprints.addCalls != 0 {
		t.Fatal("service was called with an unresolved key")
	}
}

// 没有快照时返回空数组而不是 null：页面据此提示去跑 snapshot。
func TestSprintBurndownReturnsEmptyArray(t *testing.T) {
	sprints := &fakeSprints{}
	server := sprintServer(sprints, &fakeWorkItems{})

	id := uuid.NewString()
	request := httptest.NewRequest(http.MethodGet, "/api/sprints/"+id+"/burndown", nil)
	request.SetPathValue("id", id)

	recorder := httptest.NewRecorder()
	server.handleSprintBurndown(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	var response struct {
		Points []map[string]any `json:"points"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Points == nil {
		t.Fatal("points must be [] not null")
	}
	if len(response.Points) != 0 {
		t.Fatalf("points=%v", response.Points)
	}
}

// sprintNotFound 让 fake 的 GetByKey 返回一个非 nil 错误。
type sprintNotFound struct{}

func (sprintNotFound) Error() string { return "work item not found" }

var _ = time.Now
