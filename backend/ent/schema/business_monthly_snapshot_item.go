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

// BusinessMonthlySnapshotItem preserves one included revenue or cost line.
type BusinessMonthlySnapshotItem struct {
	ent.Schema
}

func (BusinessMonthlySnapshotItem) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "business_monthly_snapshot_items"}}
}

func (BusinessMonthlySnapshotItem) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (BusinessMonthlySnapshotItem) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("snapshot_id"),
		field.String("item_type").MaxLen(40).NotEmpty(),
		field.String("source_type").MaxLen(40).NotEmpty(),
		field.Int64("source_id").Optional().Nillable(),
		field.String("name").MaxLen(180).NotEmpty(),
		field.String("category").MaxLen(50).Optional().Nillable(),
		field.String("tier").MaxLen(32).Optional().Nillable(),
		field.Int64("original_amount_minor").Default(0),
		field.String("currency").MaxLen(3).NotEmpty(),
		field.Int64("rate_scaled").Default(1_000_000).Positive(),
		field.Int64("amount_cny_cents").Default(0),
		field.Time("expires_on").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.String("reason").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Bool("included").Default(true),
		field.Int64("linked_api_key_id").Optional().Nillable(),
		field.String("group_name").MaxLen(160).Optional().Nillable(),
		field.String("user_email").MaxLen(254).Optional().Nillable(),
	}
}

func (BusinessMonthlySnapshotItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("snapshot_id"),
		index.Fields("snapshot_id", "item_type"),
		index.Fields("source_type", "source_id"),
	}
}
