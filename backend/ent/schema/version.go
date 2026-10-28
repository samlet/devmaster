package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type Version struct {
	ent.Schema
}

func (Version) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("product_id", uuid.UUID{}),
		field.String("name").NotEmpty().MaxLen(32),
		field.String("status").Default("PLANNED").MaxLen(16),
		field.String("milestone_label").Optional().Nillable().MaxLen(32),
		field.Time("planned_date").Optional().Nillable(),
		field.Time("released_at").Optional().Nillable(),
		field.String("notes_ref").Optional().Nillable(),
		field.Time("scope_frozen_at").Optional().Nillable(),
		field.Int("version").Default(0),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Version) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("product_id", "name").Unique(),
	}
}

type ReleaseRecord struct {
	ent.Schema
}

func (ReleaseRecord) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("version_id", uuid.UUID{}),
		field.String("status").Default("PENDING_APPROVAL").MaxLen(24),
		field.String("tag").Optional().Nillable().MaxLen(64),
		field.String("commit_from").Optional().Nillable().MaxLen(64),
		field.String("commit_to").Optional().Nillable().MaxLen(64),
		field.String("submitted_by").NotEmpty().MaxLen(128),
		field.String("approval_instance_id").Optional().Nillable().MaxLen(64),
		field.String("approver_id").Optional().Nillable().MaxLen(128),
		field.String("evidence_ref").Optional().Nillable(),
		field.String("failure_reason").Optional().Nillable(),
		field.String("released_by").Optional().Nillable().MaxLen(128),
		field.Time("released_at").Optional().Nillable(),
		field.Int("version").Default(0),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
