package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// OutboxEvent 是事务性发件箱。业务写入与它同事务，relay 负责投递。
type OutboxEvent struct {
	ent.Schema
}

func (OutboxEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("event_id", uuid.UUID{}).Unique(),
		field.String("subject").NotEmpty().MaxLen(200),
		field.JSON("payload", map[string]any{}),
		field.JSON("headers", map[string]string{}).Optional(),
		field.Int("attempts").Default(0),
		field.Time("next_attempt_at").Default(time.Now),
		field.Time("sent_at").Optional().Nillable(),
		field.String("last_error").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (OutboxEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("next_attempt_at", "id"),
	}
}
