package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BusinessCostItem stores recurring or one-time operating costs.
type BusinessCostItem struct {
	ent.Schema
}

func (BusinessCostItem) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "business_cost_items"}}
}

func (BusinessCostItem) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}, mixins.SoftDeleteMixin{}}
}

func (BusinessCostItem) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(160).NotEmpty(),
		field.String("cost_class").MaxLen(20).NotEmpty(),
		field.String("category").MaxLen(50).NotEmpty(),
		field.Int64("amount_minor").Min(0),
		field.String("currency").MaxLen(3).NotEmpty(),
		field.String("billing_cycle").MaxLen(20).NotEmpty(),
		field.Time("starts_on").SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.Time("ends_on").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.Int64("account_id").Optional().Nillable(),
		field.String("account_identifier").MaxLen(160).Optional().Nillable(),
		field.Bool("is_free").Default(false),
		field.Bool("active").Default(true),
		field.String("notes").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

func (BusinessCostItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("active", "starts_on"),
		index.Fields("cost_class"),
		index.Fields("category"),
		index.Fields("currency"),
		index.Fields("account_id"),
		index.Fields("deleted_at"),
	}
}
