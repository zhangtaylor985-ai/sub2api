CREATE TABLE IF NOT EXISTS api_key_plan_packages (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    request_id VARCHAR(100) NOT NULL,
    package_name VARCHAR(160) NOT NULL,
    daily_limit_usd DECIMAL(20, 8) NOT NULL DEFAULT 0,
    weekly_limit_usd DECIMAL(20, 8) NOT NULL DEFAULT 0,
    concurrency INTEGER NOT NULL DEFAULT 0,
    months INTEGER NOT NULL DEFAULT 1,
    starts_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    source VARCHAR(32) NOT NULL DEFAULT 'admin',
    note TEXT NULL,
    created_by VARCHAR(120) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT api_key_plan_packages_api_key_fk
        FOREIGN KEY (api_key_id) REFERENCES api_keys(id) ON DELETE CASCADE,
    CONSTRAINT api_key_plan_packages_group_fk
        FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE RESTRICT,
    CONSTRAINT api_key_plan_packages_request_unique UNIQUE (api_key_id, request_id),
    CONSTRAINT api_key_plan_packages_daily_limit_nonnegative CHECK (daily_limit_usd >= 0),
    CONSTRAINT api_key_plan_packages_weekly_limit_nonnegative CHECK (weekly_limit_usd >= 0),
    CONSTRAINT api_key_plan_packages_concurrency_nonnegative CHECK (concurrency >= 0),
    CONSTRAINT api_key_plan_packages_months_valid CHECK (months BETWEEN 1 AND 24),
    CONSTRAINT api_key_plan_packages_period_valid CHECK (expires_at > starts_at),
    CONSTRAINT api_key_plan_packages_source_valid CHECK (source IN ('admin', 'legacy_baseline'))
);

CREATE INDEX IF NOT EXISTS api_key_plan_packages_api_key_period_idx
    ON api_key_plan_packages (api_key_id, starts_at, expires_at);

CREATE INDEX IF NOT EXISTS api_key_plan_packages_api_key_group_expiry_idx
    ON api_key_plan_packages (api_key_id, group_id, expires_at DESC);
