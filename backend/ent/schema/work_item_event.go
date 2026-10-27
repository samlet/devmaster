package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// WorkItemEvent 是工作项的事件日志。append-only，数据库触发器阻止改写。
type WorkItemEvent struct {
	ent.Schema
}

func (WorkItemEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("work_item_id", uuid.UUID{}),
		field.String("event_type").NotEmpty().MaxLen(32),
		field.String("from_status").Optional().Nillable().MaxLen(16),
		field.String("to_status").Optional().Nillable().MaxLen(16),
		field.String("actor_id").Optional().Nillable().MaxLen(128),
		field.JSON("payload", map[string]any{}).Optional(),
		field.Time("occurred_at").Default(time.Now).Immutable(),
	}
}

func (WorkItemEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_item_id"),
		index.Fields("organization_id", "occurred_at"),
	}
}
