// Package outbox 实现事务性发件箱：业务写入与事件写入同一个事务，
// 由独立进程（relay）投递到 NATS JetStream。
//
// 为什么不直接在请求里发 NATS：业务提交成功但发布失败时，客户端重试会撞状态机，
// 而事件已经丢了；发布成功但事务回滚时，下游会收到一个不存在的事实。
// outbox 把"要不要发"变成"事务提交了没有"，这是唯一可靠的判据。
package outbox

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EventVersion 是契约版本。v1 固定为 1。
const EventVersion = 1

// namespace 用于确定性 event_id 派生。固定值，改动等于换一套事件身份。
var namespace = uuid.NewSHA1(uuid.NameSpaceDNS, []byte("devmaster.events"))

// Event 是 outbox 里的一行，也是投递到 NATS 的完整信封。
type Event struct {
	ID             int64
	OrganizationID uuid.UUID
	EventID        uuid.UUID
	Subject        string
	Payload        []byte
	Headers        map[string]string
	Attempts       int
	NextAttemptAt  time.Time
	SentAt         *time.Time
	CreatedAt      time.Time
}

type NewEventParams struct {
	OrganizationID   uuid.UUID
	Producer         string
	AggregateType    string
	AggregateID      uuid.UUID
	AggregateVersion int64
	EventType        string
	Payload          map[string]any
	TraceParent      string
}

// NewEvent 构造事件并派生确定性 event_id。
//
// 派生规则：UUIDv5(namespace, aggregate_type:aggregate_id:event_type:aggregate_version)
// 同一个业务事实重发时 event_id 不变，消费方按它去重就能做到幂等。
func NewEvent(params NewEventParams) (Event, error) {
	if params.OrganizationID == uuid.Nil {
		return Event{}, fmt.Errorf("outbox event requires organization_id")
	}
	if strings.TrimSpace(params.EventType) == "" {
		return Event{}, fmt.Errorf("outbox event requires event_type")
	}
	if params.AggregateID == uuid.Nil {
		return Event{}, fmt.Errorf("outbox event requires aggregate_id")
	}
	if params.AggregateVersion < 1 {
		return Event{}, fmt.Errorf("outbox event requires aggregate_version >= 1")
	}
	producer := strings.TrimSpace(params.Producer)
	if producer == "" {
		return Event{}, fmt.Errorf("outbox event requires producer")
	}

	eventID := uuid.NewSHA1(namespace, []byte(fmt.Sprintf("%s:%s:%s:%d",
		params.AggregateType, params.AggregateID, params.EventType, params.AggregateVersion)))

	envelope := map[string]any{
		"event_id":          eventID.String(),
		"event_type":        params.EventType,
		"event_version":     EventVersion,
		"occurred_at":       time.Now().UTC().Format(time.RFC3339Nano),
		"organization_id":   params.OrganizationID.String(),
		"aggregate_type":    params.AggregateType,
		"aggregate_id":      params.AggregateID.String(),
		"aggregate_version": params.AggregateVersion,
		"producer":          producer,
		"payload":           params.Payload,
	}
	if params.TraceParent != "" {
		envelope["traceparent"] = params.TraceParent
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return Event{}, fmt.Errorf("marshal outbox event: %w", err)
	}
	headers := map[string]string{
		"event_type": params.EventType,
		"event_id":   eventID.String(),
	}
	if params.TraceParent != "" {
		headers["traceparent"] = params.TraceParent
	}
	return Event{
		OrganizationID: params.OrganizationID,
		EventID:        eventID,
		Subject:        params.EventType,
		Payload:        payload,
		Headers:        headers,
	}, nil
}
