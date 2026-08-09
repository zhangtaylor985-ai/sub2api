-- Business profitability dashboard, cost ledger, reconciliation settings, and
-- immutable monthly operating snapshots. These tables are isolated from the
-- request authentication, routing, and payment hot paths.

CREATE TABLE IF NOT EXISTS business_pricing_rules (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL,
    tier VARCHAR(32) NOT NULL,
    monthly_price_cents BIGINT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    notes TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT business_pricing_rules_group_unique UNIQUE (group_id),
    CONSTRAINT business_pricing_rules_group_fk
        FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE RESTRICT,
    CONSTRAINT business_pricing_rules_price_nonnegative
        CHECK (monthly_price_cents >= 0)
);

CREATE INDEX IF NOT EXISTS business_pricing_rules_active_idx
    ON business_pricing_rules (active);

CREATE INDEX IF NOT EXISTS business_pricing_rules_tier_idx
    ON business_pricing_rules (tier);

CREATE TABLE IF NOT EXISTS business_api_key_configs (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL,
    revenue_excluded BOOLEAN NOT NULL DEFAULT FALSE,
    override_amount_cents BIGINT NULL,
    private_subscription_id BIGINT NULL,
    reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT business_api_key_configs_key_unique UNIQUE (api_key_id),
    CONSTRAINT business_api_key_configs_subscription_unique UNIQUE (private_subscription_id),
    CONSTRAINT business_api_key_configs_key_fk
        FOREIGN KEY (api_key_id) REFERENCES api_keys(id) ON DELETE CASCADE,
    CONSTRAINT business_api_key_configs_subscription_fk
        FOREIGN KEY (private_subscription_id) REFERENCES private_customer_subscriptions(id) ON DELETE SET NULL,
    CONSTRAINT business_api_key_configs_override_nonnegative
        CHECK (override_amount_cents IS NULL OR override_amount_cents >= 0)
);

CREATE INDEX IF NOT EXISTS business_api_key_configs_revenue_excluded_idx
    ON business_api_key_configs (revenue_excluded);

CREATE TABLE IF NOT EXISTS business_cost_items (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(160) NOT NULL,
    cost_class VARCHAR(20) NOT NULL,
    category VARCHAR(50) NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    billing_cycle VARCHAR(20) NOT NULL,
    starts_on DATE NOT NULL,
    ends_on DATE NULL,
    account_id BIGINT NULL,
    account_identifier VARCHAR(160) NULL,
    is_free BOOLEAN NOT NULL DEFAULT FALSE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    notes TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ NULL,
    CONSTRAINT business_cost_items_account_fk
        FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT business_cost_items_amount_nonnegative CHECK (amount_minor >= 0),
    CONSTRAINT business_cost_items_class_check CHECK (cost_class IN ('direct', 'operating')),
    CONSTRAINT business_cost_items_cycle_check CHECK (billing_cycle IN ('monthly', 'yearly', 'one_time')),
    CONSTRAINT business_cost_items_dates_check CHECK (ends_on IS NULL OR ends_on >= starts_on),
    CONSTRAINT business_cost_items_free_amount_check CHECK (NOT is_free OR amount_minor = 0)
);

CREATE INDEX IF NOT EXISTS business_cost_items_active_starts_idx
    ON business_cost_items (active, starts_on);

CREATE INDEX IF NOT EXISTS business_cost_items_class_idx
    ON business_cost_items (cost_class);

CREATE INDEX IF NOT EXISTS business_cost_items_category_idx
    ON business_cost_items (category);

CREATE INDEX IF NOT EXISTS business_cost_items_currency_idx
    ON business_cost_items (currency);

CREATE INDEX IF NOT EXISTS business_cost_items_account_idx
    ON business_cost_items (account_id);

CREATE INDEX IF NOT EXISTS business_cost_items_deleted_at_idx
    ON business_cost_items (deleted_at);

CREATE TABLE IF NOT EXISTS business_exchange_rates (
    id BIGSERIAL PRIMARY KEY,
    month DATE NOT NULL,
    currency VARCHAR(3) NOT NULL,
    rate_scaled BIGINT NOT NULL,
    source VARCHAR(32) NOT NULL DEFAULT 'manual',
    notes TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT business_exchange_rates_month_currency_unique UNIQUE (month, currency),
    CONSTRAINT business_exchange_rates_rate_positive CHECK (rate_scaled > 0),
    CONSTRAINT business_exchange_rates_month_first_day CHECK (EXTRACT(DAY FROM month) = 1)
);

CREATE TABLE IF NOT EXISTS business_monthly_snapshots (
    id BIGSERIAL PRIMARY KEY,
    month DATE NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'locked',
    data_quality VARCHAR(20) NOT NULL,
    api_key_count INTEGER NOT NULL DEFAULT 0,
    private_subscription_count INTEGER NOT NULL DEFAULT 0,
    customer_count INTEGER NOT NULL DEFAULT 0,
    excluded_api_key_count INTEGER NOT NULL DEFAULT 0,
    anomaly_count INTEGER NOT NULL DEFAULT 0,
    api_key_revenue_cents BIGINT NOT NULL DEFAULT 0,
    private_subscription_revenue_cents BIGINT NOT NULL DEFAULT 0,
    total_revenue_cents BIGINT NOT NULL DEFAULT 0,
    direct_cost_cents BIGINT NOT NULL DEFAULT 0,
    operating_cost_cents BIGINT NOT NULL DEFAULT 0,
    gross_profit_cents BIGINT NOT NULL DEFAULT 0,
    net_profit_cents BIGINT NOT NULL DEFAULT 0,
    gross_margin_bps BIGINT NOT NULL DEFAULT 0,
    net_margin_bps BIGINT NOT NULL DEFAULT 0,
    costs_complete BOOLEAN NOT NULL DEFAULT TRUE,
    notes TEXT NULL,
    closed_at TIMESTAMPTZ NOT NULL,
    closed_by BIGINT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT business_monthly_snapshots_month_unique UNIQUE (month),
    CONSTRAINT business_monthly_snapshots_status_check CHECK (status = 'locked'),
    CONSTRAINT business_monthly_snapshots_quality_check CHECK (data_quality IN ('actual', 'estimated', 'manual')),
    CONSTRAINT business_monthly_snapshots_counts_nonnegative CHECK (
        api_key_count >= 0 AND private_subscription_count >= 0 AND customer_count >= 0
        AND excluded_api_key_count >= 0 AND anomaly_count >= 0
    ),
    CONSTRAINT business_monthly_snapshots_month_first_day CHECK (EXTRACT(DAY FROM month) = 1)
);

CREATE INDEX IF NOT EXISTS business_monthly_snapshots_quality_idx
    ON business_monthly_snapshots (data_quality);

CREATE INDEX IF NOT EXISTS business_monthly_snapshots_closed_at_idx
    ON business_monthly_snapshots (closed_at);

CREATE TABLE IF NOT EXISTS business_monthly_snapshot_items (
    id BIGSERIAL PRIMARY KEY,
    snapshot_id BIGINT NOT NULL,
    item_type VARCHAR(40) NOT NULL,
    source_type VARCHAR(40) NOT NULL,
    source_id BIGINT NULL,
    name VARCHAR(180) NOT NULL,
    category VARCHAR(50) NULL,
    tier VARCHAR(32) NULL,
    original_amount_minor BIGINT NOT NULL DEFAULT 0,
    currency VARCHAR(3) NOT NULL,
    rate_scaled BIGINT NOT NULL DEFAULT 1000000,
    amount_cny_cents BIGINT NOT NULL DEFAULT 0,
    expires_on DATE NULL,
    reason TEXT NULL,
    included BOOLEAN NOT NULL DEFAULT TRUE,
    linked_api_key_id BIGINT NULL,
    group_name VARCHAR(160) NULL,
    user_email VARCHAR(254) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT business_monthly_snapshot_items_snapshot_fk
        FOREIGN KEY (snapshot_id) REFERENCES business_monthly_snapshots(id) ON DELETE CASCADE,
    CONSTRAINT business_monthly_snapshot_items_rate_positive CHECK (rate_scaled > 0)
);

CREATE INDEX IF NOT EXISTS business_monthly_snapshot_items_snapshot_idx
    ON business_monthly_snapshot_items (snapshot_id);

CREATE INDEX IF NOT EXISTS business_monthly_snapshot_items_type_idx
    ON business_monthly_snapshot_items (snapshot_id, item_type);

CREATE INDEX IF NOT EXISTS business_monthly_snapshot_items_source_idx
    ON business_monthly_snapshot_items (source_type, source_id);
