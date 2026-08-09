package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type businessRepository struct {
	db *sql.DB
}

func NewBusinessRepository(db *sql.DB) service.BusinessRepository {
	return &businessRepository{db: db}
}

func (r *businessRepository) LoadSources(
	ctx context.Context,
	month, asOf time.Time,
) (*service.BusinessSourceBundle, error) {
	bundle := &service.BusinessSourceBundle{}
	var err error
	if bundle.APIKeys, err = r.loadAPIKeys(ctx, asOf); err != nil {
		return nil, fmt.Errorf("load API keys: %w", err)
	}
	if bundle.TokenPackages, err = r.loadTokenPackages(ctx, month, asOf); err != nil {
		return nil, fmt.Errorf("load token packages: %w", err)
	}
	if bundle.PrivateSubscriptions, err = r.loadPrivateSubscriptions(ctx); err != nil {
		return nil, fmt.Errorf("load private subscriptions: %w", err)
	}
	if bundle.PricingRules, err = r.loadPricingRules(ctx); err != nil {
		return nil, fmt.Errorf("load pricing rules: %w", err)
	}
	if bundle.APIKeyConfigs, err = r.loadAPIKeyConfigs(ctx); err != nil {
		return nil, fmt.Errorf("load API key configs: %w", err)
	}
	if bundle.Costs, err = r.loadCosts(ctx); err != nil {
		return nil, fmt.Errorf("load costs: %w", err)
	}
	if bundle.ExchangeRates, err = r.loadExchangeRates(ctx, month); err != nil {
		return nil, fmt.Errorf("load exchange rates: %w", err)
	}
	return bundle, nil
}

func (r *businessRepository) loadAPIKeys(
	ctx context.Context,
	asOf time.Time,
) ([]service.BusinessAPIKeySource, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			k.id,
			k.name,
			k.status,
			k.created_at,
			k.expires_at,
			k.group_id,
			COALESCE(g.name, ''),
			COALESCE(g.status, ''),
			k.user_id,
			COALESCE(u.email, ''),
			COALESCE(u.status, ''),
			k.token_package_required,
			GREATEST(0, ROUND((
				COALESCE((
					SELECT SUM(p.amount_usd)
					FROM api_key_token_packages AS p
					WHERE p.api_key_id = k.id AND p.started_at <= $1
				), 0) - COALESCE((
					SELECT SUM(u.cost_usd)
					FROM api_key_token_package_usage AS u
					WHERE u.api_key_id = k.id AND u.requested_at <= $1
				), 0)
			) * 100))::bigint
		FROM api_keys AS k
		LEFT JOIN groups AS g
			ON g.id = k.group_id
			AND g.deleted_at IS NULL
		LEFT JOIN users AS u
			ON u.id = k.user_id
			AND u.deleted_at IS NULL
		WHERE k.deleted_at IS NULL
		ORDER BY k.id ASC
	`, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.BusinessAPIKeySource, 0)
	for rows.Next() {
		var item service.BusinessAPIKeySource
		var expiresAt sql.NullTime
		var groupID sql.NullInt64
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Status,
			&item.CreatedAt,
			&expiresAt,
			&groupID,
			&item.GroupName,
			&item.GroupStatus,
			&item.UserID,
			&item.UserEmail,
			&item.UserStatus,
			&item.TokenPackageRequired,
			&item.TokenPackageRemainingUSDMinor,
		); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			item.ExpiresAt = &expiresAt.Time
		}
		if groupID.Valid {
			item.GroupID = &groupID.Int64
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) loadTokenPackages(
	ctx context.Context,
	month, asOf time.Time,
) ([]service.BusinessTokenPackageSource, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			p.id,
			p.api_key_id,
			COALESCE(k.name, ''),
			COALESCE(g.name, ''),
			ROUND(p.amount_usd * 100)::bigint,
			p.created_at
		FROM api_key_token_packages AS p
		JOIN api_keys AS k ON k.id = p.api_key_id
		LEFT JOIN groups AS g ON g.id = k.group_id
		WHERE p.created_at >= $1
		  AND p.created_at < $1 + INTERVAL '1 month'
		  AND p.created_at <= $2
		  AND k.token_package_required = TRUE
		ORDER BY p.id ASC
	`, month, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.BusinessTokenPackageSource, 0)
	for rows.Next() {
		var item service.BusinessTokenPackageSource
		if err := rows.Scan(
			&item.ID,
			&item.APIKeyID,
			&item.APIKeyName,
			&item.GroupName,
			&item.AmountUSDMinor,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) loadPrivateSubscriptions(
	ctx context.Context,
) ([]service.BusinessPrivateSubscriptionSource, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, subscription_type, amount_cents, expires_on, created_at
		FROM private_customer_subscriptions
		WHERE deleted_at IS NULL
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.BusinessPrivateSubscriptionSource, 0)
	for rows.Next() {
		var item service.BusinessPrivateSubscriptionSource
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.SubscriptionType,
			&item.AmountCents,
			&item.ExpiresOn,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) loadPricingRules(ctx context.Context) ([]service.BusinessPricingRule, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			r.id,
			r.group_id,
			COALESCE(g.name, ''),
			r.tier,
			r.monthly_price_cents,
			r.active,
			r.notes,
			r.created_at,
			r.updated_at
		FROM business_pricing_rules AS r
		LEFT JOIN groups AS g
			ON g.id = r.group_id
			AND g.deleted_at IS NULL
		ORDER BY r.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.BusinessPricingRule, 0)
	for rows.Next() {
		var item service.BusinessPricingRule
		var notes sql.NullString
		if err := rows.Scan(
			&item.ID,
			&item.GroupID,
			&item.GroupName,
			&item.Tier,
			&item.MonthlyPriceCents,
			&item.Active,
			&notes,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.Notes = nullableStringPointer(notes)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) loadAPIKeyConfigs(ctx context.Context) ([]service.BusinessAPIKeyConfig, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id,
			api_key_id,
			revenue_excluded,
			override_amount_cents,
			private_subscription_id,
			reason
		FROM business_api_key_configs
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.BusinessAPIKeyConfig, 0)
	for rows.Next() {
		var item service.BusinessAPIKeyConfig
		var overrideAmount sql.NullInt64
		var privateSubscriptionID sql.NullInt64
		var reason sql.NullString
		if err := rows.Scan(
			&item.ID,
			&item.APIKeyID,
			&item.RevenueExcluded,
			&overrideAmount,
			&privateSubscriptionID,
			&reason,
		); err != nil {
			return nil, err
		}
		item.OverrideAmountCents = nullableInt64Pointer(overrideAmount)
		item.PrivateSubscriptionID = nullableInt64Pointer(privateSubscriptionID)
		item.Reason = nullableStringPointer(reason)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) loadCosts(ctx context.Context) ([]service.BusinessCostItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id,
			name,
			cost_class,
			category,
			amount_minor,
			currency,
			billing_cycle,
			starts_on,
			ends_on,
			account_id,
			account_identifier,
			is_free,
			active,
			notes,
			created_at,
			updated_at
		FROM business_cost_items
		WHERE deleted_at IS NULL
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.BusinessCostItem, 0)
	for rows.Next() {
		var item service.BusinessCostItem
		var endsOn sql.NullTime
		var accountID sql.NullInt64
		var accountIdentifier sql.NullString
		var notes sql.NullString
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.CostClass,
			&item.Category,
			&item.AmountMinor,
			&item.Currency,
			&item.BillingCycle,
			&item.StartsOn,
			&endsOn,
			&accountID,
			&accountIdentifier,
			&item.IsFree,
			&item.Active,
			&notes,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.EndsOn = nullableTimePointer(endsOn)
		item.AccountID = nullableInt64Pointer(accountID)
		item.AccountIdentifier = nullableStringPointer(accountIdentifier)
		item.Notes = nullableStringPointer(notes)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) loadExchangeRates(
	ctx context.Context,
	month time.Time,
) ([]service.BusinessExchangeRate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, month, currency, rate_scaled, source, notes, created_at, updated_at
		FROM business_exchange_rates
		WHERE month = $1::date
		ORDER BY currency ASC
	`, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.BusinessExchangeRate, 0)
	for rows.Next() {
		var item service.BusinessExchangeRate
		var notes sql.NullString
		if err := rows.Scan(
			&item.ID,
			&item.Month,
			&item.Currency,
			&item.RateScaled,
			&item.Source,
			&notes,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.Notes = nullableStringPointer(notes)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) ListSnapshots(
	ctx context.Context,
	throughMonth time.Time,
) ([]service.BusinessHistoryPoint, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id,
			month,
			status,
			data_quality,
			api_key_count,
			private_subscription_count,
			customer_count,
			excluded_api_key_count,
			anomaly_count,
			api_key_revenue_cents,
			private_subscription_revenue_cents,
			total_revenue_cents,
			direct_cost_cents,
			operating_cost_cents,
			gross_profit_cents,
			net_profit_cents,
			gross_margin_bps,
			net_margin_bps,
			costs_complete,
			closed_at
		FROM business_monthly_snapshots
		WHERE month < $1::date
		ORDER BY month ASC
	`, throughMonth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]service.BusinessHistoryPoint, 0)
	for rows.Next() {
		var item service.BusinessHistoryPoint
		var summary service.BusinessSummary
		var closedAt time.Time
		if err := rows.Scan(
			&item.ID,
			&item.Month,
			&item.Status,
			&item.DataQuality,
			&summary.APIKeyCount,
			&summary.PrivateSubscriptionCount,
			&summary.CustomerCount,
			&summary.ExcludedAPIKeyCount,
			&summary.AnomalyCount,
			&summary.APIKeyRevenueCents,
			&summary.PrivateSubscriptionRevenueCents,
			&summary.TotalRevenueCents,
			&summary.DirectCostCents,
			&summary.OperatingCostCents,
			&summary.GrossProfitCents,
			&summary.NetProfitCents,
			&summary.GrossMarginBPS,
			&summary.NetMarginBPS,
			&summary.CostsComplete,
			&closedAt,
		); err != nil {
			return nil, err
		}
		item.Summary = summary
		item.ClosedAt = &closedAt
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *businessRepository) GetSnapshot(
	ctx context.Context,
	month time.Time,
) (*service.BusinessReport, error) {
	report, err := getBusinessSnapshot(ctx, r.db, month)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBusinessSnapshotNotFound
	}
	return report, err
}

func (r *businessRepository) CloseSnapshot(
	ctx context.Context,
	input service.BusinessSnapshotWrite,
) (*service.BusinessReport, bool, error) {
	if input.Report == nil {
		return nil, false, errors.New("business snapshot report is required")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	summary := input.Report.Summary
	var snapshotID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO business_monthly_snapshots (
			month,
			status,
			data_quality,
			api_key_count,
			private_subscription_count,
			customer_count,
			excluded_api_key_count,
			anomaly_count,
			api_key_revenue_cents,
			private_subscription_revenue_cents,
			total_revenue_cents,
			direct_cost_cents,
			operating_cost_cents,
			gross_profit_cents,
			net_profit_cents,
			gross_margin_bps,
			net_margin_bps,
			costs_complete,
			notes,
			closed_at,
			closed_by
		) VALUES (
			$1, 'locked', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17, $18, $19, $20
		)
		ON CONFLICT (month) DO NOTHING
		RETURNING id
	`,
		input.Report.Month,
		input.DataQuality,
		summary.APIKeyCount,
		summary.PrivateSubscriptionCount,
		summary.CustomerCount,
		summary.ExcludedAPIKeyCount,
		summary.AnomalyCount,
		summary.APIKeyRevenueCents,
		summary.PrivateSubscriptionRevenueCents,
		summary.TotalRevenueCents,
		summary.DirectCostCents,
		summary.OperatingCostCents,
		summary.GrossProfitCents,
		summary.NetProfitCents,
		summary.GrossMarginBPS,
		summary.NetMarginBPS,
		summary.CostsComplete,
		input.Notes,
		input.ClosedAt,
		input.ClosedBy,
	).Scan(&snapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		existing, getErr := getBusinessSnapshot(ctx, tx, input.Report.Month)
		if getErr != nil {
			return nil, false, getErr
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	for i := range input.Report.Items {
		item := input.Report.Items[i]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO business_monthly_snapshot_items (
				snapshot_id,
				item_type,
				source_type,
				source_id,
				name,
				category,
				tier,
				original_amount_minor,
				currency,
				rate_scaled,
				amount_cny_cents,
				expires_on,
				reason,
				included,
				linked_api_key_id,
				group_name,
				user_email
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		`,
			snapshotID,
			item.ItemType,
			item.SourceType,
			item.SourceID,
			item.Name,
			item.Category,
			item.Tier,
			item.OriginalAmountMinor,
			item.Currency,
			item.RateScaled,
			item.AmountCNYCents,
			item.ExpiresOn,
			item.Reason,
			item.Included,
			item.LinkedAPIKeyID,
			item.GroupName,
			item.UserEmail,
		); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	created, err := r.GetSnapshot(ctx, input.Report.Month)
	if err != nil {
		return nil, false, err
	}
	return created, true, nil
}

type businessQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func getBusinessSnapshot(
	ctx context.Context,
	queryer businessQueryer,
	month time.Time,
) (*service.BusinessReport, error) {
	report := &service.BusinessReport{Items: make([]service.BusinessLineItem, 0)}
	var notes sql.NullString
	var closedBy sql.NullInt64
	var closedAt time.Time
	err := queryer.QueryRowContext(ctx, `
		SELECT
			id,
			month,
			status,
			data_quality,
			api_key_count,
			private_subscription_count,
			customer_count,
			excluded_api_key_count,
			anomaly_count,
			api_key_revenue_cents,
			private_subscription_revenue_cents,
			total_revenue_cents,
			direct_cost_cents,
			operating_cost_cents,
			gross_profit_cents,
			net_profit_cents,
			gross_margin_bps,
			net_margin_bps,
			costs_complete,
			notes,
			closed_at,
			closed_by
		FROM business_monthly_snapshots
		WHERE month = $1::date
	`, month).Scan(
		&report.ID,
		&report.Month,
		&report.Status,
		&report.DataQuality,
		&report.Summary.APIKeyCount,
		&report.Summary.PrivateSubscriptionCount,
		&report.Summary.CustomerCount,
		&report.Summary.ExcludedAPIKeyCount,
		&report.Summary.AnomalyCount,
		&report.Summary.APIKeyRevenueCents,
		&report.Summary.PrivateSubscriptionRevenueCents,
		&report.Summary.TotalRevenueCents,
		&report.Summary.DirectCostCents,
		&report.Summary.OperatingCostCents,
		&report.Summary.GrossProfitCents,
		&report.Summary.NetProfitCents,
		&report.Summary.GrossMarginBPS,
		&report.Summary.NetMarginBPS,
		&report.Summary.CostsComplete,
		&notes,
		&closedAt,
		&closedBy,
	)
	if err != nil {
		return nil, err
	}
	report.Notes = nullableStringPointer(notes)
	report.ClosedAt = &closedAt
	report.ClosedBy = nullableInt64Pointer(closedBy)
	report.AsOf = closedAt
	report.IsCurrent = false

	rows, err := queryer.QueryContext(ctx, `
		SELECT
			id,
			item_type,
			source_type,
			source_id,
			name,
			category,
			tier,
			original_amount_minor,
			currency,
			rate_scaled,
			amount_cny_cents,
			expires_on,
			reason,
			included,
			linked_api_key_id,
			group_name,
			user_email
		FROM business_monthly_snapshot_items
		WHERE snapshot_id = $1
		ORDER BY item_type ASC, name ASC, id ASC
	`, report.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item service.BusinessLineItem
		var sourceID sql.NullInt64
		var category sql.NullString
		var tier sql.NullString
		var expiresOn sql.NullTime
		var reason sql.NullString
		var linkedAPIKeyID sql.NullInt64
		var groupName sql.NullString
		var userEmail sql.NullString
		if err := rows.Scan(
			&item.ID,
			&item.ItemType,
			&item.SourceType,
			&sourceID,
			&item.Name,
			&category,
			&tier,
			&item.OriginalAmountMinor,
			&item.Currency,
			&item.RateScaled,
			&item.AmountCNYCents,
			&expiresOn,
			&reason,
			&item.Included,
			&linkedAPIKeyID,
			&groupName,
			&userEmail,
		); err != nil {
			return nil, err
		}
		item.SourceID = nullableInt64Pointer(sourceID)
		item.Category = nullableStringPointer(category)
		item.Tier = nullableStringPointer(tier)
		item.ExpiresOn = nullableTimePointer(expiresOn)
		item.Reason = nullableStringPointer(reason)
		item.LinkedAPIKeyID = nullableInt64Pointer(linkedAPIKeyID)
		if groupName.Valid {
			item.GroupName = groupName.String
		}
		if userEmail.Valid {
			item.UserEmail = userEmail.String
		}
		report.Items = append(report.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return report, nil
}

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
