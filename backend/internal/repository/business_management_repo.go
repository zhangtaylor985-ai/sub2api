package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *businessRepository) ListCosts(ctx context.Context) ([]service.BusinessCostItem, error) {
	return r.loadCosts(ctx)
}

func (r *businessRepository) CreateCost(
	ctx context.Context,
	cost *service.BusinessCostItem,
) error {
	if cost == nil {
		return service.ErrBusinessCostInvalid
	}
	return r.db.QueryRowContext(ctx, `
		INSERT INTO business_cost_items (
			name, cost_class, category, amount_minor, currency, billing_cycle,
			starts_on, ends_on, account_id, account_identifier, is_free, active, notes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at, updated_at
	`,
		cost.Name,
		cost.CostClass,
		cost.Category,
		cost.AmountMinor,
		cost.Currency,
		cost.BillingCycle,
		cost.StartsOn,
		cost.EndsOn,
		cost.AccountID,
		cost.AccountIdentifier,
		cost.IsFree,
		cost.Active,
		cost.Notes,
	).Scan(&cost.ID, &cost.CreatedAt, &cost.UpdatedAt)
}

func (r *businessRepository) UpdateCost(
	ctx context.Context,
	cost *service.BusinessCostItem,
) error {
	if cost == nil || cost.ID <= 0 {
		return service.ErrBusinessCostInvalid
	}
	err := r.db.QueryRowContext(ctx, `
		UPDATE business_cost_items
		SET
			name = $2,
			cost_class = $3,
			category = $4,
			amount_minor = $5,
			currency = $6,
			billing_cycle = $7,
			starts_on = $8,
			ends_on = $9,
			account_id = $10,
			account_identifier = $11,
			is_free = $12,
			active = $13,
			notes = $14,
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING created_at, updated_at
	`,
		cost.ID,
		cost.Name,
		cost.CostClass,
		cost.Category,
		cost.AmountMinor,
		cost.Currency,
		cost.BillingCycle,
		cost.StartsOn,
		cost.EndsOn,
		cost.AccountID,
		cost.AccountIdentifier,
		cost.IsFree,
		cost.Active,
		cost.Notes,
	).Scan(&cost.CreatedAt, &cost.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrBusinessCostNotFound
	}
	return err
}

func (r *businessRepository) DeleteCost(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE business_cost_items
		SET deleted_at = NOW(), updated_at = NOW(), active = FALSE
		WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return service.ErrBusinessCostNotFound
	}
	return nil
}

func (r *businessRepository) ListPricingRules(ctx context.Context) ([]service.BusinessPricingRule, error) {
	return r.loadPricingRules(ctx)
}

func (r *businessRepository) UpsertPricingRule(
	ctx context.Context,
	rule *service.BusinessPricingRule,
) error {
	if rule == nil {
		return service.ErrBusinessPricingRuleInvalid
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO business_pricing_rules (
			group_id, tier, monthly_price_cents, active, notes
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (group_id) DO UPDATE SET
			tier = EXCLUDED.tier,
			monthly_price_cents = EXCLUDED.monthly_price_cents,
			active = EXCLUDED.active,
			notes = EXCLUDED.notes,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`, rule.GroupID, rule.Tier, rule.MonthlyPriceCents, rule.Active, rule.Notes).
		Scan(&rule.ID, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return err
	}
	return r.db.QueryRowContext(ctx, `
		SELECT COALESCE(name, '')
		FROM groups
		WHERE id = $1 AND deleted_at IS NULL
	`, rule.GroupID).Scan(&rule.GroupName)
}

func (r *businessRepository) ListExchangeRates(
	ctx context.Context,
	month time.Time,
) ([]service.BusinessExchangeRate, error) {
	return r.loadExchangeRates(ctx, month)
}

func (r *businessRepository) UpsertExchangeRate(
	ctx context.Context,
	rate *service.BusinessExchangeRate,
) error {
	if rate == nil {
		return service.ErrBusinessExchangeRateInvalid
	}
	return r.db.QueryRowContext(ctx, `
		INSERT INTO business_exchange_rates (
			month, currency, rate_scaled, source, notes
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (month, currency) DO UPDATE SET
			rate_scaled = EXCLUDED.rate_scaled,
			source = EXCLUDED.source,
			notes = EXCLUDED.notes,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`, rate.Month, rate.Currency, rate.RateScaled, rate.Source, rate.Notes).
		Scan(&rate.ID, &rate.CreatedAt, &rate.UpdatedAt)
}

func (r *businessRepository) UpsertAPIKeyConfig(
	ctx context.Context,
	config *service.BusinessAPIKeyConfig,
) error {
	if config == nil {
		return service.ErrBusinessAPIKeyConfigInvalid
	}
	return r.db.QueryRowContext(ctx, `
		INSERT INTO business_api_key_configs (
			api_key_id, revenue_excluded, override_amount_cents,
			private_subscription_id, reason
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (api_key_id) DO UPDATE SET
			revenue_excluded = EXCLUDED.revenue_excluded,
			override_amount_cents = EXCLUDED.override_amount_cents,
			private_subscription_id = EXCLUDED.private_subscription_id,
			reason = EXCLUDED.reason,
			updated_at = NOW()
		RETURNING id
	`,
		config.APIKeyID,
		config.RevenueExcluded,
		config.OverrideAmountCents,
		config.PrivateSubscriptionID,
		config.Reason,
	).Scan(&config.ID)
}

func (r *businessRepository) ListReferences(ctx context.Context) (*service.BusinessReferenceData, error) {
	result := &service.BusinessReferenceData{}
	var err error
	if result.Groups, err = r.listGroupReferences(ctx); err != nil {
		return nil, fmt.Errorf("list group references: %w", err)
	}
	if result.APIKeys, err = r.listAPIKeyReferences(ctx); err != nil {
		return nil, fmt.Errorf("list API key references: %w", err)
	}
	if result.Accounts, err = r.listAccountReferences(ctx); err != nil {
		return nil, fmt.Errorf("list account references: %w", err)
	}
	if result.PrivateSubscriptions, err = r.loadPrivateSubscriptions(ctx); err != nil {
		return nil, fmt.Errorf("list private subscription references: %w", err)
	}
	return result, nil
}

func (r *businessRepository) listGroupReferences(
	ctx context.Context,
) ([]service.BusinessGroupReference, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, status, is_exclusive
		FROM groups
		WHERE deleted_at IS NULL
		ORDER BY name ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]service.BusinessGroupReference, 0)
	for rows.Next() {
		var item service.BusinessGroupReference
		if err := rows.Scan(&item.ID, &item.Name, &item.Status, &item.IsExclusive); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) listAPIKeyReferences(
	ctx context.Context,
) ([]service.BusinessAPIKeyReference, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			k.id,
			k.name,
			k.status,
			k.expires_at,
			k.group_id,
			COALESCE(g.name, ''),
			COALESCE(u.email, ''),
			c.id,
			c.revenue_excluded,
			c.override_amount_cents,
			c.private_subscription_id,
			c.reason
		FROM api_keys AS k
		LEFT JOIN groups AS g ON g.id = k.group_id AND g.deleted_at IS NULL
		LEFT JOIN users AS u ON u.id = k.user_id AND u.deleted_at IS NULL
		LEFT JOIN business_api_key_configs AS c ON c.api_key_id = k.id
		WHERE k.deleted_at IS NULL
		ORDER BY k.name ASC, k.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]service.BusinessAPIKeyReference, 0)
	for rows.Next() {
		var item service.BusinessAPIKeyReference
		var expiresAt sql.NullTime
		var groupID sql.NullInt64
		var configID sql.NullInt64
		var revenueExcluded sql.NullBool
		var overrideAmount sql.NullInt64
		var privateSubscriptionID sql.NullInt64
		var reason sql.NullString
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Status,
			&expiresAt,
			&groupID,
			&item.GroupName,
			&item.UserEmail,
			&configID,
			&revenueExcluded,
			&overrideAmount,
			&privateSubscriptionID,
			&reason,
		); err != nil {
			return nil, err
		}
		item.ExpiresAt = nullableTimePointer(expiresAt)
		item.GroupID = nullableInt64Pointer(groupID)
		if configID.Valid {
			item.Config = &service.BusinessAPIKeyConfig{
				ID:                    configID.Int64,
				APIKeyID:              item.ID,
				RevenueExcluded:       revenueExcluded.Bool,
				OverrideAmountCents:   nullableInt64Pointer(overrideAmount),
				PrivateSubscriptionID: nullableInt64Pointer(privateSubscriptionID),
				Reason:                nullableStringPointer(reason),
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) listAccountReferences(
	ctx context.Context,
) ([]service.BusinessAccountReference, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, platform, status
		FROM accounts
		WHERE deleted_at IS NULL
		ORDER BY name ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]service.BusinessAccountReference, 0)
	for rows.Next() {
		var item service.BusinessAccountReference
		if err := rows.Scan(&item.ID, &item.Name, &item.Platform, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) AccountExists(ctx context.Context, id int64) (bool, error) {
	return r.referenceExists(ctx, "accounts", id)
}

func (r *businessRepository) GroupExists(ctx context.Context, id int64) (bool, error) {
	return r.referenceExists(ctx, "groups", id)
}

func (r *businessRepository) APIKeyExists(ctx context.Context, id int64) (bool, error) {
	return r.referenceExists(ctx, "api_keys", id)
}

func (r *businessRepository) PrivateSubscriptionExists(ctx context.Context, id int64) (bool, error) {
	return r.referenceExists(ctx, "private_customer_subscriptions", id)
}

func (r *businessRepository) referenceExists(
	ctx context.Context,
	table string,
	id int64,
) (bool, error) {
	switch table {
	case "accounts", "groups", "api_keys", "private_customer_subscriptions":
	default:
		return false, fmt.Errorf("unsupported reference table %q", table)
	}
	query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1 AND deleted_at IS NULL)", table)
	var exists bool
	if err := r.db.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (r *businessRepository) InitializeDefaults(
	ctx context.Context,
	defaults service.BusinessDefaultInitialization,
) (*service.BusinessInitializationResult, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result := &service.BusinessInitializationResult{}

	for _, pricing := range defaults.Pricing {
		created, err := execCreated(ctx, tx, `
			INSERT INTO business_pricing_rules (
				group_id, tier, monthly_price_cents, active, notes
			) VALUES ($1, $2, $3, TRUE, $4)
			ON CONFLICT (group_id) DO NOTHING
		`, pricing.GroupID, pricing.Tier, pricing.MonthlyPriceCents, businessDefaultNotes())
		if err != nil {
			return nil, err
		}
		if created {
			result.PricingCreated++
		} else {
			result.PricingExisting++
		}
	}

	for _, excluded := range defaults.ExcludedKeys {
		created, err := execCreated(ctx, tx, `
			INSERT INTO business_api_key_configs (
				api_key_id, revenue_excluded, reason
			) VALUES ($1, TRUE, $2)
			ON CONFLICT (api_key_id) DO NOTHING
		`, excluded.APIKeyID, excluded.Reason)
		if err != nil {
			return nil, err
		}
		if created {
			result.ExclusionsCreated++
		} else {
			result.ExclusionsExisting++
		}
	}

	for _, cost := range defaults.Costs {
		created, err := execCreated(ctx, tx, `
			INSERT INTO business_cost_items (
				name,
				cost_class,
				category,
				amount_minor,
				currency,
				billing_cycle,
				starts_on,
				account_id,
				account_identifier,
				is_free,
				active,
				notes
			)
			SELECT $1::varchar(160), $2::varchar(20), $3::varchar(50), $4, $5::varchar(3), $6::varchar(20), $7,
				$8, $9::varchar(160), $10, TRUE, $11
			WHERE NOT EXISTS (
				SELECT 1
				FROM business_cost_items
				WHERE deleted_at IS NULL
				  AND category = $3::varchar(50)
				  AND LOWER(COALESCE(account_identifier, '')) = LOWER($9::varchar(160))
			)
		`,
			cost.Name,
			cost.CostClass,
			cost.Category,
			cost.AmountMinor,
			cost.Currency,
			cost.BillingCycle,
			defaults.Month,
			cost.AccountID,
			cost.AccountIdentifier,
			cost.IsFree,
			businessDefaultNotes(),
		)
		if err != nil {
			return nil, err
		}
		if created {
			result.CostsCreated++
		} else {
			result.CostsExisting++
		}
	}

	created, err := execCreated(ctx, tx, `
		INSERT INTO business_exchange_rates (
			month, currency, rate_scaled, source, notes
		) VALUES ($1, 'USD', $2, 'confirmed', $3)
		ON CONFLICT (month, currency) DO NOTHING
	`, defaults.Month, defaults.USDRateScaled, "Confirmed USD/CNY operating rate.")
	if err != nil {
		return nil, err
	}
	result.ExchangeRateCreated = created

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func execCreated(
	ctx context.Context,
	tx *sql.Tx,
	query string,
	args ...any,
) (bool, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func businessDefaultNotes() string {
	return strings.TrimSpace("Managed by business profitability defaults.")
}
