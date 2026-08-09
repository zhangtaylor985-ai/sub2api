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

// BusinessMonthlySnapshot is an immutable month-level operating close.
type BusinessMonthlySnapshot struct {
	ent.Schema
}

func (BusinessMonthlySnapshot) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "business_monthly_snapshots"}}
}

func (BusinessMonthlySnapshot) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (BusinessMonthlySnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.Time("month").Unique().SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.String("status").MaxLen(20).Default("locked"),
		field.String("data_quality").MaxLen(20).NotEmpty(),
		field.Int("api_key_count").Default(0).Min(0),
		field.Int("private_subscription_count").Default(0).Min(0),
		field.Int("customer_count").Default(0).Min(0),
		field.Int("excluded_api_key_count").Default(0).Min(0),
		field.Int("anomaly_count").Default(0).Min(0),
		field.Int64("api_key_revenue_cents").Default(0),
		field.Int64("private_subscription_revenue_cents").Default(0),
		field.Int64("total_revenue_cents").Default(0),
		field.Int64("direct_cost_cents").Default(0),
		field.Int64("operating_cost_cents").Default(0),
		field.Int64("gross_profit_cents").Default(0),
		field.Int64("net_profit_cents").Default(0),
		field.Int64("gross_margin_bps").Default(0),
		field.Int64("net_margin_bps").Default(0),
		field.Bool("costs_complete").Default(true),
		field.String("notes").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("closed_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int64("closed_by").Optional().Nillable(),
	}
}

func (BusinessMonthlySnapshot) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("data_quality"),
		index.Fields("closed_at"),
	}
}
