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

// BusinessExchangeRate stores a month-specific exact rate to CNY. The value
// uses six decimal places: 6.75 is stored as 6_750_000.
type BusinessExchangeRate struct {
	ent.Schema
}

func (BusinessExchangeRate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "business_exchange_rates"}}
}

func (BusinessExchangeRate) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (BusinessExchangeRate) Fields() []ent.Field {
	return []ent.Field{
		field.Time("month").SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.String("currency").MaxLen(3).NotEmpty(),
		field.Int64("rate_scaled").Positive(),
		field.String("source").MaxLen(32).Default("manual"),
		field.String("notes").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

func (BusinessExchangeRate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("month", "currency").Unique(),
	}
}
