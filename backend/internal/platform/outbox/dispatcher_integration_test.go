package outbox_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/config"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/dbtest"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/outbox"
)

type fakePublisher struct {
	calls int
	fail  bool
}

func (f *fakePublisher) Publish(context.Context, string, []byte, map[string]string) error {
	f.calls++
	if f.fail {
		return errors.New("nats is down")
	}
	return nil
}

func seedEvent(t *testing.T, organizationID uuid.UUID) outbox.Event {
	t.Helper()
	event, err := outbox.NewEvent(outbox.NewEventParams{
		OrganizationID:   organizationID,
		Producer:         "devmaster-server",
		AggregateType:    "product",
		AggregateID:      uuid.New(),
		AggregateVersion: 1,
		EventType:        "dev.v1.product.created",
		Payload:          map[string]any{"key": "HOSP"},
	})
	if err != nil {
		t.Fatalf("构造事件失败：%v", err)
	}
	db := dbtest.DB(t)
	err = db.InOrgTx(context.Background(), organizationID, func(tx *sql.Tx) error {
		return outbox.NewRepository().Insert(context.Background(), tx, event)
	})
	if err != nil {
		t.Fatalf("写 outbox 失败：%v", err)
	}
	return event
}

func TestDispatcherSendsAndMarks(t *testing.T) {
	db := dbtest.DB(t)
	dbtest.Cleanup(t)
	organizationID := uuid.New()
	event := seedEvent(t, organizationID)

	publisher := &fakePublisher{}
	dispatcher := outbox.NewDispatcher(db, outbox.NewRepository(), publisher, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		config.OutboxConfig{BatchSize: 10, MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute})
	sent, err := dispatcher.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce 报错：%v", err)
	}
	if sent != 1 || publisher.calls != 1 {
		t.Fatalf("sent=%d calls=%d，want 1/1", sent, publisher.calls)
	}
	stored, found, err := outbox.NewRepository().FindByEventID(context.Background(), db, organizationID, event.EventID)
	if err != nil || !found {
		t.Fatalf("读 outbox 失败：found=%v err=%v", found, err)
	}
	if stored.SentAt == nil {
		t.Fatal("投递成功后 sent_at 应当有值")
	}
}

func TestDispatcherBacksOffOnFailure(t *testing.T) {
	db := dbtest.DB(t)
	dbtest.Cleanup(t)
	organizationID := uuid.New()
	event := seedEvent(t, organizationID)

	publisher := &fakePublisher{fail: true}
	dispatcher := outbox.NewDispatcher(db, outbox.NewRepository(), publisher, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		config.OutboxConfig{BatchSize: 10, MaxAttempts: 3, BaseBackoff: 2 * time.Second, MaxBackoff: time.Minute})
	sent, err := dispatcher.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce 不应返回错误（失败已落库）：%v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d，want 0", sent)
	}
	stored, found, err := outbox.NewRepository().FindByEventID(context.Background(), db, organizationID, event.EventID)
	if err != nil || !found {
		t.Fatalf("读 outbox 失败：found=%v err=%v", found, err)
	}
	if stored.SentAt != nil {
		t.Fatal("失败时不应标记 sent")
	}
	if stored.Attempts != 1 {
		t.Fatalf("attempts = %d，want 1", stored.Attempts)
	}

	// 把下次尝试时间拨到当下，模拟退避到期后重试成功。
	if _, err := db.SQL.ExecContext(context.Background(),
		`UPDATE outbox_events SET next_attempt_at = now() WHERE event_id = $1`, event.EventID); err != nil {
		t.Fatalf("调整重试时间失败：%v", err)
	}
	publisher.fail = false
	sent, err = dispatcher.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("重试报错：%v", err)
	}
	if sent != 1 {
		t.Fatalf("重试后 sent = %d，want 1", sent)
	}
}
