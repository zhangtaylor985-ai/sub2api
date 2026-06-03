-- API key token packages.
-- Token packages are one-time USD allowances consumed only after effective
-- daily/weekly rate windows are exhausted. Effective limits may come from the
-- API key itself or from its group defaults.

CREATE TABLE IF NOT EXISTS api_key_token_packages (
    id          BIGSERIAL PRIMARY KEY,
    api_key_id  BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    amount_usd  DECIMAL(20, 8) NOT NULL CHECK (amount_usd > 0),
    used_usd    DECIMAL(20, 8) NOT NULL DEFAULT 0 CHECK (used_usd >= 0),
    note        TEXT,
    created_by  VARCHAR(100),
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_key_token_packages_key_started
    ON api_key_token_packages(api_key_id, started_at, id);

CREATE INDEX IF NOT EXISTS idx_api_key_token_packages_remaining
    ON api_key_token_packages(api_key_id)
    WHERE amount_usd > used_usd;

CREATE TABLE IF NOT EXISTS api_key_token_package_usage (
    id                  BIGSERIAL PRIMARY KEY,
    package_id          BIGINT NOT NULL REFERENCES api_key_token_packages(id) ON DELETE CASCADE,
    api_key_id           BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    request_id          VARCHAR(128),
    request_fingerprint VARCHAR(64),
    model               VARCHAR(100),
    cost_usd            DECIMAL(20, 8) NOT NULL CHECK (cost_usd > 0),
    requested_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_key_token_package_usage_key_created
    ON api_key_token_package_usage(api_key_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_api_key_token_package_usage_package_created
    ON api_key_token_package_usage(package_id, created_at DESC);

COMMENT ON TABLE api_key_token_packages IS 'One-time API key token packages in USD, consumed after key daily/weekly windows are exhausted';
COMMENT ON TABLE api_key_token_package_usage IS 'Per-request token package consumption ledger';
