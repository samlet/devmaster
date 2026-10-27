package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// InboxEvent 是入站幂等表。M0 没有入站源，表先建好，M4 接 Gitea 时不再改迁移。
type InboxEvent struct {
	ent.Schema
}

func (InboxEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("organization_id", uuid.UUID{}),
		field.String("source_system").NotEmpty().MaxLen(32),
		field.String("event_id").NotEmpty().MaxLen(128),
		field.String("event_type").NotEmpty().MaxLen(200),
		field.Int("event_version").Default(1),
		field.String("subject").Default("").MaxLen(200),
		field.Int64("stream_sequence").Default(0),
		field.JSON("payload", map[string]any{}),
		field.Bytes("payload_sha256"),
		field.String("status").Default("RECEIVED").MaxLen(16),
		field.Int("attempts").Default(0),
		field.String("last_error").Optional().Nillable(),
		field.Time("received_at").Default(time.Now).Immutable(),
		field.Time("processed_at").Optional().Nillable(),
	}
}

func (InboxEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source_system", "event_id").Unique(),
	}
}
