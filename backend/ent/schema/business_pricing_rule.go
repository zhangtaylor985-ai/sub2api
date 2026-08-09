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

// BusinessPricingRule maps one API key group to a monthly operating price.
type BusinessPricingRule struct {
	ent.Schema
}

func (BusinessPricingRule) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "business_pricing_rules"}}
}

func (BusinessPricingRule) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (BusinessPricingRule) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("group_id").Unique(),
		field.String("tier").MaxLen(32).NotEmpty(),
		field.Int64("monthly_price_cents").Min(0),
		field.Bool("active").Default(true),
		field.String("notes").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

func (BusinessPricingRule) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("active"),
		index.Fields("tier"),
	}
}
