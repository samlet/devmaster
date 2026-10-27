package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Product 是被开发的软件（HOSP / FI / PPM），不是项目。
// key 是工作项编号前缀，创建后不可变。
type Product struct {
	ent.Schema
}

func (Product) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("organization_id", uuid.UUID{}),
		field.String("key").NotEmpty().MaxLen(10),
		field.String("name").NotEmpty().MaxLen(200),
		field.String("description").Default("").MaxLen(2000),
		field.String("status").Default("ACTIVE").MaxLen(16),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Product) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("organization_id", "key").Unique(),
		index.Fields("organization_id", "status", "key"),
	}
}
