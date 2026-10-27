package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// ProductCounter 是工作项编号的单调计数器。
// 编号不回收：取消的工作项也占号，否则历史编号会被复用。
type ProductCounter struct {
	ent.Schema
}

func (ProductCounter) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("product_id", uuid.UUID{}).Unique(),
		field.Int64("next_seq").Default(1),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
