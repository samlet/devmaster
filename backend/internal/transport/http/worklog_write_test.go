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

	worklogapp "github.com/xiaofeiwu/devmaster/backend/internal/worklog/application"
	worklogdomain "github.com/xiaofeiwu/devmaster/backend/internal/worklog/domain"
)

type fakeWorkLogs struct {
	logCalls     int
	reverseCalls int
	lastCmd      worklogapp.LogCommand
	lastMinutes  *int
	lastNote     *string
	err          error
}

func (f *fakeWorkLogs) List(_ context.Context, _ worklogapp.Actor, _ worklogapp.ListFilter) ([]worklogdomain.WorkLog, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	note := "写接口"
	return []worklogdomain.WorkLog{{
		ID: uuid.New(), WorkItemID: uuid.New(), WorkItemKey: "DEMO-1",
		AuthorID: "alice", Minutes: 90, WorkDate: time.Now(), Note: &note,
		Source: worklogdomain.SourceManual, CreatedAt: time.Now(),
	}}, 1, nil
}

func (f *fakeWorkLogs) Sum(_ context.Context, _ worklogapp.Actor, _ worklogapp.ListFilter) (int, error) {
	return 90, f.err
}

func (f *fakeWorkLogs) Log(_ context.Context, actor worklogapp.Actor, cmd worklogapp.LogCommand) (worklogdomain.WorkLog, error) {
	f.logCalls++
	f.lastCmd = cmd
	if f.err != nil {
		return worklogdomain.WorkLog{}, f.err
	}
	return worklogdomain.WorkLog{
		ID: uuid.New(), WorkItemKey: cmd.WorkItemKey, AuthorID: actor.Subject,
		Minutes: cmd.Minutes, WorkDate: cmd.WorkDate, Source: worklogdomain.SourceManual,
	}, nil
}

func (f *fakeWorkLogs) Reverse(_ context.Context, _ worklogapp.Actor, _ uuid.UUID, minutes *int, note *string) (worklogdomain.WorkLog, error) {
	f.reverseCalls++
	f.lastMinutes = minutes
	f.lastNote = note
	if f.err != nil {
		return worklogdomain.WorkLog{}, f.err
	}
	value := -30
	if minutes != nil {
		value = -*minutes
	}
	return worklogdomain.WorkLog{ID: uuid.New(), Minutes: value}, nil
}

func workLogServer(logs WorkLogStore) *Server {
	return &Server{
		workLogs:              logs,
		bffSecret:             testBFFSecret,
		logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		defaultOrganizationID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	}
}

func TestListWorkLogsReturnsSummary(t *testing.T) {
	server := workLogServer(&fakeWorkLogs{})

	recorder := httptest.NewRecorder()
	server.handleWorkLogs(recorder, httptest.NewRequest(http.MethodGet, "/api/worklogs?author_id=alice", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items        []map[string]any `json:"items"`
		Total        int              `json:"total"`
		TotalMinutes int              `json:"total_minutes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// 列表与汇总一次返回：工时页必然两个都要。
	if response.Total != 1 || response.TotalMinutes != 90 || len(response.Items) != 1 {
		t.Fatalf("response=%+v", response)
	}
	if response.Items[0]["work_item_key"] != "DEMO-1" {
		t.Fatalf("item=%v", response.Items[0])
	}
}

func TestCreateWorkLogAcceptsSignedRequest(t *testing.T) {
	logs := &fakeWorkLogs{}
	server := workLogServer(logs)

	recorder := httptest.NewRecorder()
	server.handleWorkLogs(recorder, signedPost(t, "/api/worklogs", map[string]any{
		"work_item_key": "DEMO-1", "minutes": 90, "work_date": "2026-10-28",
		"note": "写接口", "actor": "alice",
	}, false))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if logs.logCalls != 1 || logs.lastCmd.Minutes != 90 || logs.lastCmd.WorkItemKey != "DEMO-1" {
		t.Fatalf("cmd=%+v", logs.lastCmd)
	}
}

func TestCreateWorkLogRejectsBadDate(t *testing.T) {
	logs := &fakeWorkLogs{}
	server := workLogServer(logs)

	recorder := httptest.NewRecorder()
	server.handleWorkLogs(recorder, signedPost(t, "/api/worklogs", map[string]any{
		"work_item_key": "DEMO-1", "minutes": 90, "work_date": "28/10/2026", "actor": "alice",
	}, false))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	if logs.logCalls != 0 {
		t.Fatal("service was called with a malformed date")
	}
}

// 冲正必须说明原因：半年后没人记得为什么扣掉这 30 分钟。
func TestReverseWorkLogRequiresNote(t *testing.T) {
	logs := &fakeWorkLogs{}
	server := workLogServer(logs)

	payload, _ := json.Marshal(map[string]any{"minutes": 30, "actor": "alice"})
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/api/worklogs/"+uuid.NewString()+"/reverse", bytes.NewReader(payload))
	request.SetPathValue("id", uuid.NewString())
	request.Header.Set("X-BFF-Timestamp", timestamp)
	request.Header.Set("X-BFF-Signature", signBFF(testBFFSecret, timestamp, payload))

	recorder := httptest.NewRecorder()
	server.handleReverseWorkLog(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", recorder.Code)
	}
	if logs.reverseCalls != 0 {
		t.Fatal("service was called without a reason")
	}
}

func TestReverseWorkLogAcceptsPartialMinutes(t *testing.T) {
	logs := &fakeWorkLogs{}
	server := workLogServer(logs)

	id := uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"minutes": 30, "note": "记多了", "actor": "alice"})
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/api/worklogs/"+id+"/reverse", bytes.NewReader(payload))
	request.SetPathValue("id", id)
	request.Header.Set("X-BFF-Timestamp", timestamp)
	request.Header.Set("X-BFF-Signature", signBFF(testBFFSecret, timestamp, payload))

	recorder := httptest.NewRecorder()
	server.handleReverseWorkLog(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if logs.reverseCalls != 1 || logs.lastMinutes == nil || *logs.lastMinutes != 30 {
		t.Fatalf("minutes=%v", logs.lastMinutes)
	}
}
