package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/privatecustomersubscription"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"

	entsql "entgo.io/ent/dialect/sql"
)

type privateSubscriptionRepository struct {
	client *dbent.Client
	sql    *sql.DB
}

func NewPrivateSubscriptionRepository(
	client *dbent.Client,
	sqlDB *sql.DB,
) service.PrivateSubscriptionRepository {
	return &privateSubscriptionRepository{
		client: client,
		sql:    sqlDB,
	}
}

func (r *privateSubscriptionRepository) Create(
	ctx context.Context,
	subscription *service.PrivateSubscription,
) error {
	client := clientFromContext(ctx, r.client)
	created, err := client.PrivateCustomerSubscription.Create().
		SetName(subscription.Name).
		SetSubscriptionType(subscription.SubscriptionType).
		SetAmountCents(subscription.AmountCents).
		SetExpiresOn(subscription.ExpiresOn).
		Save(ctx)
	if err != nil {
		return err
	}
	applyPrivateSubscriptionEntity(subscription, created)
	return nil
}

func (r *privateSubscriptionRepository) GetByID(
	ctx context.Context,
	id int64,
) (*service.PrivateSubscription, error) {
	client := clientFromContext(ctx, r.client)
	entity, err := client.PrivateCustomerSubscription.Get(ctx, id)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrPrivateSubscriptionNotFound, nil)
	}
	return privateSubscriptionEntityToService(entity), nil
}

func (r *privateSubscriptionRepository) Update(
	ctx context.Context,
	subscription *service.PrivateSubscription,
) error {
	client := clientFromContext(ctx, r.client)
	builder := client.PrivateCustomerSubscription.UpdateOneID(subscription.ID).
		SetName(subscription.Name).
		SetSubscriptionType(subscription.SubscriptionType).
		SetAmountCents(subscription.AmountCents).
		SetExpiresOn(subscription.ExpiresOn)

	if subscription.ReminderSentForExpiry != nil {
		builder.SetReminderSentForExpiry(*subscription.ReminderSentForExpiry)
	} else {
		builder.ClearReminderSentForExpiry()
	}
	if subscription.ReminderSentAt != nil {
		builder.SetReminderSentAt(*subscription.ReminderSentAt)
	} else {
		builder.ClearReminderSentAt()
	}

	updated, err := builder.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrPrivateSubscriptionNotFound, nil)
	}
	applyPrivateSubscriptionEntity(subscription, updated)
	return nil
}

func (r *privateSubscriptionRepository) Delete(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	return client.PrivateCustomerSubscription.DeleteOneID(id).Exec(ctx)
}

func (r *privateSubscriptionRepository) List(
	ctx context.Context,
	params pagination.PaginationParams,
	filters service.PrivateSubscriptionListFilters,
	today time.Time,
) ([]service.PrivateSubscription, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	query := client.PrivateCustomerSubscription.Query()

	if filters.Search != "" {
		query = query.Where(
			privatecustomersubscription.Or(
				privatecustomersubscription.NameContainsFold(filters.Search),
				privatecustomersubscription.SubscriptionTypeContainsFold(filters.Search),
			),
		)
	}
	if filters.SubscriptionType != "" {
		query = query.Where(
			privatecustomersubscription.SubscriptionTypeEQ(filters.SubscriptionType),
		)
	}
	query = applyPrivateSubscriptionStatusFilter(query, filters.Status, today)

	total, err := query.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	itemsQuery := query.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range privateSubscriptionListOrder(params) {
		itemsQuery = itemsQuery.Order(order)
	}

	entities, err := itemsQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	items := make([]service.PrivateSubscription, 0, len(entities))
	for i := range entities {
		item := privateSubscriptionEntityToService(entities[i])
		if item != nil {
			items = append(items, *item)
		}
	}
	return items, paginationResultFromTotal(int64(total), params), nil
}

func (r *privateSubscriptionRepository) Summary(
	ctx context.Context,
	today time.Time,
) (*service.PrivateSubscriptionSummary, error) {
	var summary service.PrivateSubscriptionSummary
	err := r.sql.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE expires_on > ($1::date + 7)) AS active,
			COUNT(*) FILTER (
				WHERE expires_on >= $1::date
				  AND expires_on <= ($1::date + 7)
			) AS due_soon,
			COUNT(*) FILTER (WHERE expires_on < $1::date) AS expired,
			COALESCE(SUM(amount_cents), 0) AS total_amount_cents
		FROM private_customer_subscriptions
		WHERE deleted_at IS NULL
	`, today).Scan(
		&summary.Total,
		&summary.Active,
		&summary.DueSoon,
		&summary.Expired,
		&summary.TotalAmountCents,
	)
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

func (r *privateSubscriptionRepository) ListDueForReminder(
	ctx context.Context,
	expiresOn time.Time,
	limit int,
) ([]service.PrivateSubscription, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	client := clientFromContext(ctx, r.client)
	entities, err := client.PrivateCustomerSubscription.Query().
		Where(
			privatecustomersubscription.ExpiresOnEQ(expiresOn),
			privatecustomersubscription.Or(
				privatecustomersubscription.ReminderSentForExpiryIsNil(),
				privatecustomersubscription.ReminderSentForExpiryNEQ(expiresOn),
			),
		).
		Order(
			dbent.Asc(privatecustomersubscription.FieldExpiresOn),
			dbent.Asc(privatecustomersubscription.FieldID),
		).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}

	items := make([]service.PrivateSubscription, 0, len(entities))
	for i := range entities {
		item := privateSubscriptionEntityToService(entities[i])
		if item != nil {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (r *privateSubscriptionRepository) MarkReminderSent(
	ctx context.Context,
	id int64,
	expiresOn, sentAt time.Time,
) (bool, error) {
	client := clientFromContext(ctx, r.client)
	updated, err := client.PrivateCustomerSubscription.Update().
		Where(
			privatecustomersubscription.IDEQ(id),
			privatecustomersubscription.ExpiresOnEQ(expiresOn),
			privatecustomersubscription.Or(
				privatecustomersubscription.ReminderSentForExpiryIsNil(),
				privatecustomersubscription.ReminderSentForExpiryNEQ(expiresOn),
			),
		).
		SetReminderSentForExpiry(expiresOn).
		SetReminderSentAt(sentAt).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return updated == 1, nil
}

func applyPrivateSubscriptionStatusFilter(
	query *dbent.PrivateCustomerSubscriptionQuery,
	status string,
	today time.Time,
) *dbent.PrivateCustomerSubscriptionQuery {
	dueSoonEnd := today.AddDate(0, 0, 7)
	switch status {
	case service.PrivateSubscriptionStatusActive:
		return query.Where(privatecustomersubscription.ExpiresOnGT(dueSoonEnd))
	case service.PrivateSubscriptionStatusDueSoon:
		return query.Where(
			privatecustomersubscription.ExpiresOnGTE(today),
			privatecustomersubscription.ExpiresOnLTE(dueSoonEnd),
		)
	case service.PrivateSubscriptionStatusExpired:
		return query.Where(privatecustomersubscription.ExpiresOnLT(today))
	default:
		return query
	}
}

func privateSubscriptionListOrder(
	params pagination.PaginationParams,
) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderAsc)

	field := privatecustomersubscription.FieldExpiresOn
	switch sortBy {
	case "name":
		field = privatecustomersubscription.FieldName
	case "subscription_type":
		field = privatecustomersubscription.FieldSubscriptionType
	case "amount_cents":
		field = privatecustomersubscription.FieldAmountCents
	case "created_at":
		field = privatecustomersubscription.FieldCreatedAt
	case "updated_at":
		field = privatecustomersubscription.FieldUpdatedAt
	case "id":
		field = privatecustomersubscription.FieldID
	case "", "expires_on":
		field = privatecustomersubscription.FieldExpiresOn
	}

	if sortOrder == pagination.SortOrderDesc {
		return []func(*entsql.Selector){
			dbent.Desc(field),
			dbent.Desc(privatecustomersubscription.FieldID),
		}
	}
	return []func(*entsql.Selector){
		dbent.Asc(field),
		dbent.Asc(privatecustomersubscription.FieldID),
	}
}

func privateSubscriptionEntityToService(
	entity *dbent.PrivateCustomerSubscription,
) *service.PrivateSubscription {
	if entity == nil {
		return nil
	}
	return &service.PrivateSubscription{
		ID:                    entity.ID,
		Name:                  entity.Name,
		SubscriptionType:      entity.SubscriptionType,
		AmountCents:           entity.AmountCents,
		ExpiresOn:             entity.ExpiresOn,
		ReminderSentForExpiry: entity.ReminderSentForExpiry,
		ReminderSentAt:        entity.ReminderSentAt,
		CreatedAt:             entity.CreatedAt,
		UpdatedAt:             entity.UpdatedAt,
	}
}

func applyPrivateSubscriptionEntity(
	target *service.PrivateSubscription,
	entity *dbent.PrivateCustomerSubscription,
) {
	if target == nil || entity == nil {
		return
	}
	converted := privateSubscriptionEntityToService(entity)
	if converted != nil {
		*target = *converted
	}
}
