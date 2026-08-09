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

// BusinessAPIKeyConfig stores key-scoped inclusion, price override, and
// private-subscription linkage without touching the authentication hot path.
type BusinessAPIKeyConfig struct {
	ent.Schema
}

func (BusinessAPIKeyConfig) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "business_api_key_configs"}}
}

func (BusinessAPIKeyConfig) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (BusinessAPIKeyConfig) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("api_key_id").Unique(),
		field.Bool("revenue_excluded").Default(false),
		field.Int64("override_amount_cents").Optional().Nillable().Min(0),
		field.Int64("private_subscription_id").Optional().Nillable().Unique(),
		field.String("reason").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

func (BusinessAPIKeyConfig) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("revenue_excluded"),
		index.Fields("private_subscription_id"),
	}
}
