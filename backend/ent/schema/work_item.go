package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// WorkItem 是工作项当前态。事件日志在 work_item_events，两者同事务更新。
type WorkItem struct {
	ent.Schema
}

func (WorkItem) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.String("key").NotEmpty().MaxLen(32),
		field.UUID("product_id", uuid.UUID{}),
		field.String("type").NotEmpty().MaxLen(16),
		field.String("status").NotEmpty().MaxLen(16),
		field.String("resolution").Optional().Nillable().MaxLen(32),
		field.String("title").NotEmpty().MaxLen(500),
		field.String("description_md").Default(""),
		field.String("acceptance_criteria").Optional().Nillable(),
		field.Int16("priority").Default(3),
		field.String("severity").Optional().Nillable().MaxLen(16),
		field.String("environment").Optional().Nillable().MaxLen(16),
		field.String("reporter_id").NotEmpty().MaxLen(128),
		field.String("assignee_id").Optional().Nillable().MaxLen(128),
		field.UUID("parent_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("sprint_id", uuid.UUID{}).Optional().Nillable(),
		field.Int16("story_points").Optional().Nillable(),
		field.Int("estimate_minutes").Optional().Nillable(),
		field.String("platform").Optional().Nillable().MaxLen(16),
		field.String("build_number").Optional().Nillable().MaxLen(64),
		field.String("repro_steps").Optional().Nillable(),
		field.String("expected_behavior").Optional().Nillable(),
		field.String("actual_behavior").Optional().Nillable(),
		field.UUID("version_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("affects_version_id", uuid.UUID{}).Optional().Nillable(),
		field.String("rank").NotEmpty().MaxLen(64),
		field.String("project_code").Optional().Nillable().MaxLen(64),
		field.String("wbs_code").Optional().Nillable().MaxLen(64),
		field.String("external_ref").Optional().Nillable().MaxLen(128),
		field.JSON("custom_fields", map[string]any{}).Optional(),
		field.String("blocked_reason").Optional().Nillable(),
		field.Int16("reopened_count").Default(0),
		field.Int("version").Default(0),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Time("resolved_at").Optional().Nillable(),
		field.Time("closed_at").Optional().Nillable(),
	}
}

func (WorkItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("organization_id", "key").Unique(),
		index.Fields("product_id", "rank"),
		index.Fields("assignee_id", "status"),
	}
}
