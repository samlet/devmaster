package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type WorkItemLink struct {
	ent.Schema
}

func (WorkItemLink) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("from_id", uuid.UUID{}),
		field.UUID("to_id", uuid.UUID{}),
		field.String("link_type").NotEmpty().MaxLen(16),
		field.String("created_by").Optional().Nillable().MaxLen(128),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (WorkItemLink) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("from_id", "to_id", "link_type").Unique(),
		index.Fields("to_id"),
	}
}

type Comment struct {
	ent.Schema
}

func (Comment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("work_item_id", uuid.UUID{}),
		field.String("author_id").NotEmpty().MaxLen(128),
		field.String("body_md").NotEmpty(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("edited_at").Optional().Nillable(),
	}
}

func (Comment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_item_id", "created_at"),
	}
}
