-- Session capture policy is intentionally stored in the primary Sub2API
-- database. The isolated Session database contains captured payloads only.

CREATE TABLE IF NOT EXISTS session_capture_settings (
    id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    mode        VARCHAR(16) NOT NULL DEFAULT 'all'
                CHECK (mode IN ('all', 'selected', 'disabled')),
    updated_by  BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO session_capture_settings (id, mode)
VALUES (1, 'all')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS session_capture_api_key_policies (
    api_key_id  BIGINT PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    policy      VARCHAR(16) NOT NULL
                CHECK (policy IN ('include', 'exclude')),
    updated_by  BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_session_capture_api_key_policies_policy
    ON session_capture_api_key_policies (policy);

CREATE TABLE IF NOT EXISTS session_capture_policy_audit (
    id              BIGSERIAL PRIMARY KEY,
    actor_user_id   BIGINT,
    action          VARCHAR(32) NOT NULL,
    api_key_id      BIGINT,
    previous_value  JSONB NOT NULL DEFAULT '{}'::jsonb,
    new_value       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_session_capture_policy_audit_created_at
    ON session_capture_policy_audit (created_at DESC);
