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

// PrivateCustomerSubscription stores manually managed subscriptions that are
// intentionally isolated from Sub2API users, API keys, groups, and billing.
type PrivateCustomerSubscription struct {
	ent.Schema
}

func (PrivateCustomerSubscription) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "private_customer_subscriptions"},
	}
}

func (PrivateCustomerSubscription) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (PrivateCustomerSubscription) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			NotEmpty().
			MaxLen(120).
			Comment("Private customer display name"),
		field.String("subscription_type").
			NotEmpty().
			MaxLen(50).
			Comment("Operator-defined subscription type, for example 5X or 20X"),
		field.Int64("amount_cents").
			Min(0).
			Comment("Subscription amount in CNY cents"),
		field.Time("expires_on").
			SchemaType(map[string]string{dialect.Postgres: "date"}).
			Comment("Calendar expiry date in the configured business timezone"),
		field.Time("reminder_sent_for_expiry").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "date"}).
			Comment("Expiry date for which the Telegram reminder was delivered"),
		field.Time("reminder_sent_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("Most recent successful Telegram reminder timestamp"),
	}
}

func (PrivateCustomerSubscription) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("name"),
		index.Fields("subscription_type"),
		index.Fields("expires_on"),
		index.Fields("expires_on", "reminder_sent_for_expiry"),
		index.Fields("deleted_at"),
	}
}
