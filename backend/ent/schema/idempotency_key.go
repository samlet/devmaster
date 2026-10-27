package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// IdempotencyKey 记录写接口的请求指纹与响应快照。
type IdempotencyKey struct {
	ent.Schema
}

func (IdempotencyKey) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").Unique().NotEmpty().MaxLen(256),
		field.UUID("organization_id", uuid.UUID{}),
		field.String("scope").NotEmpty().MaxLen(64),
		field.Bytes("request_hash"),
		field.Bytes("response_blob"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("expires_at"),
	}
}
