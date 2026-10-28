package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type WorkItemCommit struct {
	ent.Schema
}

func (WorkItemCommit) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("work_item_id", uuid.UUID{}),
		field.String("repo").NotEmpty().MaxLen(200),
		field.String("branch").Optional().Nillable().MaxLen(200),
		field.String("commit_sha").NotEmpty().MaxLen(64),
		field.Int("pr_no").Optional().Nillable(),
		field.String("pr_state").Optional().Nillable().MaxLen(16),
		field.Time("committed_at").Optional().Nillable(),
		field.String("author_id").Optional().Nillable().MaxLen(128),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (WorkItemCommit) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_item_id", "repo", "commit_sha").Unique(),
	}
}

type WorkLog struct {
	ent.Schema
}

func (WorkLog) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.UUID("work_item_id", uuid.UUID{}),
		field.String("author_id").NotEmpty().MaxLen(128),
		field.Int("minutes"),
		field.Time("work_date"),
		field.String("note").Optional().Nillable(),
		field.String("project_code").Optional().Nillable().MaxLen(64),
		field.String("wbs_code").Optional().Nillable().MaxLen(64),
		field.String("grade_code").Optional().Nillable().MaxLen(32),
		field.String("source").Default("MANUAL").MaxLen(16),
		field.UUID("reversal_of", uuid.UUID{}).Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

type RepoSyncCursor struct {
	ent.Schema
}

func (RepoSyncCursor) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("organization_id", uuid.UUID{}),
		field.String("repo").NotEmpty().MaxLen(200),
		field.Time("last_commit_at").Optional().Nillable(),
		field.String("last_sha").Optional().Nillable().MaxLen(64),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (RepoSyncCursor) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("organization_id", "repo").Unique(),
	}
}
