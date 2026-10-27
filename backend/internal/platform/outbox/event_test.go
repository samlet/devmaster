package outbox

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestNewEventDerivesStableEventID(t *testing.T) {
	params := NewEventParams{
		OrganizationID:   uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Producer:         "devmaster-server",
		AggregateType:    "product",
		AggregateID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AggregateVersion: 1,
		EventType:        "dev.v1.product.created",
		Payload:          map[string]any{"key": "HOSP"},
	}
	first, err := NewEvent(params)
	if err != nil {
		t.Fatalf("NewEvent 报错：%v", err)
	}
	second, err := NewEvent(params)
	if err != nil {
		t.Fatalf("NewEvent 报错：%v", err)
	}
	if first.EventID != second.EventID {
		t.Fatalf("同一事实两次构造应得到同一 event_id：%s vs %s", first.EventID, second.EventID)
	}

	params.AggregateVersion = 2
	third, err := NewEvent(params)
	if err != nil {
		t.Fatalf("NewEvent 报错：%v", err)
	}
	if third.EventID == first.EventID {
		t.Fatal("聚合版本不同应当得到不同 event_id")
	}
}

func TestNewEventEnvelopeShape(t *testing.T) {
	event, err := NewEvent(NewEventParams{
		OrganizationID:   uuid.New(),
		Producer:         "devmaster-server",
		AggregateType:    "product",
		AggregateID:      uuid.New(),
		AggregateVersion: 1,
		EventType:        "dev.v1.product.created",
		Payload:          map[string]any{"key": "HOSP"},
		TraceParent:      "00-trace-span-01",
	})
	if err != nil {
		t.Fatalf("NewEvent 报错：%v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(event.Payload, &envelope); err != nil {
		t.Fatalf("解析信封失败：%v", err)
	}
	for _, field := range []string{
		"event_id", "event_type", "event_version", "occurred_at",
		"organization_id", "aggregate_type", "aggregate_id", "aggregate_version",
		"producer", "traceparent", "payload",
	} {
		if _, ok := envelope[field]; !ok {
			t.Fatalf("信封缺少字段 %s：%s", field, string(event.Payload))
		}
	}
	if envelope["event_version"].(float64) != 1 {
		t.Fatalf("event_version = %v", envelope["event_version"])
	}
	if event.Subject != "dev.v1.product.created" {
		t.Fatalf("subject = %s", event.Subject)
	}
}

func TestNewEventRejectsIncompleteInput(t *testing.T) {
	base := NewEventParams{
		OrganizationID:   uuid.New(),
		Producer:         "devmaster-server",
		AggregateType:    "product",
		AggregateID:      uuid.New(),
		AggregateVersion: 1,
		EventType:        "dev.v1.product.created",
	}
	cases := map[string]func(*NewEventParams){
		"缺组织":    func(p *NewEventParams) { p.OrganizationID = uuid.Nil },
		"缺事件类型":  func(p *NewEventParams) { p.EventType = "" },
		"缺聚合 id": func(p *NewEventParams) { p.AggregateID = uuid.Nil },
		"版本为 0":  func(p *NewEventParams) { p.AggregateVersion = 0 },
		"缺生产者":   func(p *NewEventParams) { p.Producer = "" },
	}
	for name, mutate := range cases {
		params := base
		mutate(&params)
		if _, err := NewEvent(params); err == nil {
			t.Fatalf("%s：应当被拒绝", name)
		}
	}
}
