package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type Board struct {
	ent.Schema
}

func (Board) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("product_id", uuid.UUID{}),
		field.String("name").NotEmpty().MaxLen(100),
		field.JSON("wip_limits", map[string]int{}).Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (Board) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("product_id", "name").Unique(),
	}
}

type Sprint struct {
	ent.Schema
}

func (Sprint) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("board_id", uuid.UUID{}),
		field.String("name").NotEmpty().MaxLen(100),
		field.String("goal").Default(""),
		field.String("state").Default("PLANNED").MaxLen(16),
		field.Time("start_at").Optional().Nillable(),
		field.Time("end_at").Optional().Nillable(),
		field.Int16("capacity_points").Optional().Nillable(),
		field.Int16("committed_points").Default(0),
		field.Int("version").Default(0),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Time("closed_at").Optional().Nillable(),
	}
}

func (Sprint) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("board_id", "name").Unique(),
	}
}

type SprintScopeEvent struct {
	ent.Schema
}

func (SprintScopeEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("sprint_id", uuid.UUID{}),
		field.UUID("work_item_id", uuid.UUID{}),
		field.String("action").NotEmpty().MaxLen(16),
		field.Int16("points_at_event").Optional().Nillable(),
		field.String("actor_id").Optional().Nillable().MaxLen(128),
		field.String("note").Optional().Nillable(),
		field.Time("occurred_at").Default(time.Now).Immutable(),
	}
}

type SprintSnapshot struct {
	ent.Schema
}

func (SprintSnapshot) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("sprint_id", "snapshot_date").Unique(),
	}
}

func (SprintSnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("sprint_id", uuid.UUID{}),
		field.UUID("organization_id", uuid.UUID{}),
		field.Time("snapshot_date"),
		field.Int("scope_points"),
		field.Int("completed_points"),
		field.Int("remaining_points"),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
