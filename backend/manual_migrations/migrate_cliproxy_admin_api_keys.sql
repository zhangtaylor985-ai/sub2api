-- Migrate active, unexpired CLIProxyAPI API keys into the Sub2API admin account.
--
-- Run against the Sub2API PostgreSQL database.
--
-- Required psql variables:
--   src_host, src_port, src_db, src_user, src_password
--
-- Optional psql variables:
--   admin_email   default: admin-api-keys@sub2api.local
--   migration_tag default: generated timestamp by caller
--   commit        default: 0. Use 1 to commit; dry-run rolls back.
--   include_migrated_keys default: 0. Use 1 for final delta so previously
--                         migrated, still-enabled keys are included even if
--                         they expired after the initial migration.

\set ON_ERROR_STOP on

\if :{?admin_email}
\else
\set admin_email 'admin-api-keys@sub2api.local'
\endif

\if :{?migration_tag}
\else
\set migration_tag 'manual'
\endif

\if :{?commit}
\else
\set commit 0
\endif

\if :{?include_migrated_keys}
\else
\set include_migrated_keys 0
\endif

BEGIN;

CREATE EXTENSION IF NOT EXISTS postgres_fdw;

DROP SCHEMA IF EXISTS cliproxy_src_fdw CASCADE;
DROP SERVER IF EXISTS cliproxy_src_fdw CASCADE;

CREATE SCHEMA cliproxy_src_fdw;
CREATE SERVER cliproxy_src_fdw
  FOREIGN DATA WRAPPER postgres_fdw
  OPTIONS (host :'src_host', port :'src_port', dbname :'src_db');
CREATE USER MAPPING FOR CURRENT_USER
  SERVER cliproxy_src_fdw
  OPTIONS (user :'src_user', password :'src_password');

IMPORT FOREIGN SCHEMA public
  LIMIT TO (
    api_key_config_entries,
    api_key_groups,
    api_key_model_daily_usage,
    usage_events
  )
  FROM SERVER cliproxy_src_fdw INTO cliproxy_src_fdw;

CREATE TABLE IF NOT EXISTS cliproxy_legacy_migration_backup (
  migration_tag text NOT NULL,
  table_name text NOT NULL,
  row_id text NOT NULL,
  row_data jsonb NOT NULL,
  backed_up_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (migration_tag, table_name, row_id)
);

CREATE TABLE IF NOT EXISTS cliproxy_legacy_group_migration (
  source_group_id text PRIMARY KEY,
  source_group_name text,
  target_group_id bigint NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
  daily_budget_usd numeric(20,8) NOT NULL DEFAULT 0,
  weekly_budget_usd numeric(20,8) NOT NULL DEFAULT 0,
  concurrency_limit integer NOT NULL DEFAULT 0,
  migrated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE cliproxy_legacy_group_migration
  ADD COLUMN IF NOT EXISTS concurrency_limit integer NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS cliproxy_legacy_api_key_migration (
  source_api_key_hash text PRIMARY KEY,
  api_key_id bigint NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
  source_group_id text NOT NULL,
  target_group_id bigint REFERENCES groups(id) ON DELETE SET NULL,
  source_policy_json jsonb NOT NULL,
  token_packages jsonb NOT NULL DEFAULT '[]'::jsonb,
  token_package_total_usd numeric(20,8) NOT NULL DEFAULT 0,
  token_package_spent_usd numeric(20,8) NOT NULL DEFAULT 0,
  token_package_remaining_usd numeric(20,8) NOT NULL DEFAULT 0,
  source_expires_at timestamptz,
  source_owner_username text,
  source_owner_role text,
  source_concurrency_limit integer NOT NULL DEFAULT 0,
  effective_daily_budget_usd numeric(20,8) NOT NULL DEFAULT 0,
  effective_weekly_budget_usd numeric(20,8) NOT NULL DEFAULT 0,
  daily_usage_usd numeric(20,8) NOT NULL DEFAULT 0,
  weekly_usage_usd numeric(20,8) NOT NULL DEFAULT 0,
  total_usage_usd numeric(20,8) NOT NULL DEFAULT 0,
  migrated_at timestamptz NOT NULL DEFAULT now(),
  migration_tag text NOT NULL
);

ALTER TABLE cliproxy_legacy_api_key_migration
  ADD COLUMN IF NOT EXISTS source_concurrency_limit integer NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS cliproxy_legacy_usage_event_migration (
  source_usage_event_id bigint PRIMARY KEY,
  usage_log_id bigint NOT NULL REFERENCES usage_logs(id) ON DELETE CASCADE,
  source_api_key_hash text NOT NULL,
  migrated_at timestamptz NOT NULL DEFAULT now(),
  migration_tag text NOT NULL
);

INSERT INTO cliproxy_legacy_migration_backup (migration_tag, table_name, row_id, row_data)
SELECT :'migration_tag', 'groups', id::text, to_jsonb(groups.*)
FROM groups
ON CONFLICT DO NOTHING;

INSERT INTO cliproxy_legacy_migration_backup (migration_tag, table_name, row_id, row_data)
SELECT :'migration_tag', 'api_keys', id::text, to_jsonb(api_keys.*)
FROM api_keys
ON CONFLICT DO NOTHING;

INSERT INTO cliproxy_legacy_migration_backup (migration_tag, table_name, row_id, row_data)
SELECT :'migration_tag', 'account_groups', account_id::text || ':' || group_id::text, to_jsonb(account_groups.*)
FROM account_groups
ON CONFLICT DO NOTHING;

INSERT INTO users (
  email,
  password_hash,
  role,
  username,
  balance,
  concurrency,
  status,
  notes
)
SELECT
  :'admin_email',
  'admin-managed-api-keys',
  'user',
  'Admin API Keys',
  1000000,
  10,
  'active',
  'System carrier user for admin-managed API keys'
WHERE NOT EXISTS (
  SELECT 1 FROM users WHERE email = :'admin_email' AND deleted_at IS NULL
);

UPDATE users
SET
  username = 'Admin API Keys',
  balance = GREATEST(balance, 1000000),
  concurrency = 10,
  status = 'active',
  updated_at = now()
WHERE email = :'admin_email'
  AND deleted_at IS NULL;

CREATE TEMP TABLE _migration_context AS
SELECT
  (SELECT id FROM users WHERE email = :'admin_email' AND deleted_at IS NULL ORDER BY id LIMIT 1) AS admin_user_id,
  (SELECT id FROM accounts WHERE deleted_at IS NULL AND status = 'active' ORDER BY id LIMIT 1) AS account_id,
  (SELECT messages_dispatch_model_config FROM groups WHERE deleted_at IS NULL AND platform = 'openai' ORDER BY id LIMIT 1) AS openai_messages_config;

DO $$
BEGIN
  IF (SELECT admin_user_id FROM _migration_context) IS NULL THEN
    RAISE EXCEPTION 'admin user not found';
  END IF;
  IF (SELECT account_id FROM _migration_context) IS NULL THEN
    RAISE EXCEPTION 'active Sub2API account not found';
  END IF;
END $$;

CREATE TEMP TABLE _src_active_keys AS
SELECT
  e.api_key,
  md5(e.api_key) AS api_key_hash,
  e.policy_json,
  e.created_at,
  e.expires_at,
  e.owner_username,
  e.owner_role,
  COALESCE(NULLIF(e.policy_json->>'group-id', ''), 'legacy-ungrouped') AS source_group_id
FROM cliproxy_src_fdw.api_key_config_entries e
WHERE COALESCE(e.disabled, false) = false
  AND (
    e.expires_at IS NULL
    OR e.expires_at > now()
    OR (
      :'include_migrated_keys'::int = 1
      AND EXISTS (
        SELECT 1
        FROM cliproxy_legacy_api_key_migration migrated
        WHERE migrated.source_api_key_hash = md5(e.api_key)
      )
    )
  );

CREATE TEMP TABLE _src_groups AS
SELECT
  k.source_group_id,
  COALESCE(g.name, CASE WHEN k.source_group_id = 'legacy-ungrouped' THEN 'Ungrouped' ELSE k.source_group_id END) AS source_group_name,
  CASE
    WHEN k.source_group_id = 'legacy-ungrouped' THEN 'CP Legacy ungrouped'
    ELSE left('CP Legacy ' || k.source_group_id, 100)
  END AS target_group_name,
  COALESCE(g.daily_budget_micro_usd::numeric / 1000000.0, 0) AS group_daily_budget_usd,
  COALESCE(g.weekly_budget_micro_usd::numeric / 1000000.0, 0) AS group_weekly_budget_usd,
  COALESCE(g.concurrency_limit, 0) AS group_concurrency_limit
FROM (SELECT DISTINCT source_group_id FROM _src_active_keys) k
LEFT JOIN cliproxy_src_fdw.api_key_groups g ON g.id = k.source_group_id;

WITH to_insert AS (
  SELECT s.*
  FROM _src_groups s
  LEFT JOIN cliproxy_legacy_group_migration m ON m.source_group_id = s.source_group_id
  WHERE m.source_group_id IS NULL
), inserted AS (
  INSERT INTO groups (
    name,
    description,
    platform,
    subscription_type,
    daily_limit_usd,
    weekly_limit_usd,
    monthly_limit_usd,
    rate_multiplier,
    is_exclusive,
    status,
    model_routing,
    model_routing_enabled,
    supported_model_scopes,
    allow_messages_dispatch,
    messages_dispatch_model_config,
    default_mapped_model,
    sort_order,
    mcp_xml_inject,
    allow_image_generation,
    concurrency
  )
  SELECT
    t.target_group_name,
    'Migrated from CLIProxyAPI group ' || t.source_group_id || ' (' || t.source_group_name || ')',
    'openai',
    'standard',
    t.group_daily_budget_usd,
    t.group_weekly_budget_usd,
    0,
    1,
    false,
    'active',
    '{}'::jsonb,
    false,
    '[]'::jsonb,
    false,
    COALESCE((SELECT openai_messages_config FROM _migration_context), '{}'::jsonb),
    '',
    1000,
    true,
    false,
    t.group_concurrency_limit
  FROM to_insert t
  RETURNING id, name
)
INSERT INTO cliproxy_legacy_group_migration (
  source_group_id,
  source_group_name,
  target_group_id,
  daily_budget_usd,
  weekly_budget_usd,
  concurrency_limit
)
SELECT
  t.source_group_id,
  t.source_group_name,
  i.id,
  t.group_daily_budget_usd,
  t.group_weekly_budget_usd,
  t.group_concurrency_limit
FROM to_insert t
JOIN inserted i ON i.name = t.target_group_name
ON CONFLICT (source_group_id) DO NOTHING;

UPDATE cliproxy_legacy_group_migration m
SET
  source_group_name = s.source_group_name,
  daily_budget_usd = s.group_daily_budget_usd,
  weekly_budget_usd = s.group_weekly_budget_usd,
  concurrency_limit = s.group_concurrency_limit,
  migrated_at = now()
FROM _src_groups s
WHERE m.source_group_id = s.source_group_id;

UPDATE groups g
SET
  daily_limit_usd = s.group_daily_budget_usd,
  weekly_limit_usd = s.group_weekly_budget_usd,
  concurrency = s.group_concurrency_limit,
  updated_at = now()
FROM cliproxy_legacy_group_migration m
JOIN _src_groups s ON s.source_group_id = m.source_group_id
WHERE g.id = m.target_group_id
  AND g.deleted_at IS NULL;

INSERT INTO account_groups (account_id, group_id, priority)
SELECT (SELECT account_id FROM _migration_context), m.target_group_id, 50
FROM cliproxy_legacy_group_migration m
JOIN _src_groups s ON s.source_group_id = m.source_group_id
ON CONFLICT (account_id, group_id) DO NOTHING;

CREATE TEMP TABLE _src_key_packages AS
WITH raw_pkg AS (
  SELECT
    k.api_key,
    CASE
      WHEN jsonb_typeof(k.policy_json->'token-packages') = 'array' THEN k.policy_json->'token-packages'
      WHEN COALESCE(NULLIF(k.policy_json->>'token-package-usd', ''), '0')::numeric > 0
        AND NULLIF(k.policy_json->>'token-package-started-at', '') IS NOT NULL
      THEN jsonb_build_array(jsonb_build_object(
        'id', 'legacy-single',
        'usd', (k.policy_json->>'token-package-usd')::numeric,
        'started-at', k.policy_json->>'token-package-started-at'
      ))
      ELSE '[]'::jsonb
    END AS packages
  FROM _src_active_keys k
), expanded AS (
  SELECT
    r.api_key,
    pkg,
    COALESCE((pkg->>'usd')::numeric, 0) AS usd,
    (pkg->>'started-at')::timestamptz AS started_at
  FROM raw_pkg r
  LEFT JOIN LATERAL jsonb_array_elements(r.packages) pkg ON true
), agg AS (
  SELECT
    r.api_key,
    r.packages,
    COALESCE(SUM(e.usd) FILTER (WHERE e.usd > 0), 0) AS total_usd,
    MIN(e.started_at) FILTER (WHERE e.usd > 0) AS first_started_at
  FROM raw_pkg r
  LEFT JOIN expanded e ON e.api_key = r.api_key
  GROUP BY r.api_key, r.packages
)
SELECT
  a.api_key,
  a.packages,
  a.total_usd,
  a.first_started_at,
  COALESCE(SUM(u.cost_micro_usd), 0)::numeric / 1000000.0 AS spent_usd
FROM agg a
LEFT JOIN cliproxy_src_fdw.usage_events u
  ON u.api_key = a.api_key
 AND a.first_started_at IS NOT NULL
 AND to_timestamp(u.requested_at) >= a.first_started_at
GROUP BY a.api_key, a.packages, a.total_usd, a.first_started_at;

CREATE TEMP TABLE _src_key_usage AS
WITH day_usage AS (
  SELECT api_key, COALESCE(SUM(cost_micro_usd), 0)::numeric / 1000000.0 AS daily_usage_usd
  FROM cliproxy_src_fdw.api_key_model_daily_usage
  WHERE day = to_char(now() AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD')
  GROUP BY api_key
), key_windows AS (
  SELECT
    k.api_key,
    COALESCE(
      NULLIF(k.policy_json->>'weekly-budget-anchor-at', '')::timestamptz,
      k.created_at
    ) AS weekly_anchor_at
  FROM _src_active_keys k
), window_bounds AS (
  SELECT
    api_key,
    weekly_anchor_at
      + floor(extract(epoch FROM (now() - weekly_anchor_at)) / 604800.0) * interval '7 days' AS weekly_window_start
  FROM key_windows
), week_usage AS (
  SELECT
    wb.api_key,
    wb.weekly_window_start,
    wb.weekly_window_start + interval '7 days' AS weekly_window_end,
    COALESCE(SUM(u.cost_micro_usd), 0)::numeric / 1000000.0 AS weekly_usage_usd
  FROM window_bounds wb
  LEFT JOIN cliproxy_src_fdw.usage_events u
    ON u.api_key = wb.api_key
   AND u.requested_at >= extract(epoch FROM wb.weekly_window_start)
   AND u.requested_at < extract(epoch FROM wb.weekly_window_start + interval '7 days')
  GROUP BY wb.api_key, wb.weekly_window_start
), total_usage AS (
  SELECT
    api_key,
    COALESCE(SUM(cost_micro_usd), 0)::numeric / 1000000.0 AS total_usage_usd,
    max(to_timestamp(requested_at)) AS last_used_at
  FROM cliproxy_src_fdw.usage_events
  GROUP BY api_key
)
SELECT
  k.api_key,
  COALESCE(d.daily_usage_usd, 0) AS daily_usage_usd,
  COALESCE(w.weekly_usage_usd, 0) AS weekly_usage_usd,
  COALESCE(t.total_usage_usd, 0) AS total_usage_usd,
  t.last_used_at,
  (date_trunc('day', now() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai') AS daily_window_start,
  w.weekly_window_start,
  w.weekly_window_end
FROM _src_active_keys k
LEFT JOIN day_usage d ON d.api_key = k.api_key
LEFT JOIN week_usage w ON w.api_key = k.api_key
LEFT JOIN total_usage t ON t.api_key = k.api_key;

CREATE TEMP TABLE _src_key_map AS
SELECT
  k.*,
  gm.target_group_id,
  CASE
    WHEN g.source_group_id IS NOT NULL AND k.source_group_id <> 'legacy-ungrouped' THEN g.group_daily_budget_usd
    WHEN COALESCE(NULLIF(k.policy_json->>'daily-budget-usd', ''), '0') ~ '^[0-9]+(\.[0-9]+)?$'
      THEN COALESCE(NULLIF(k.policy_json->>'daily-budget-usd', ''), '0')::numeric
    ELSE 0
  END AS effective_daily_budget_usd,
  CASE
    WHEN g.source_group_id IS NOT NULL AND k.source_group_id <> 'legacy-ungrouped' THEN g.group_weekly_budget_usd
    WHEN COALESCE(NULLIF(k.policy_json->>'weekly-budget-usd', ''), '0') ~ '^[0-9]+(\.[0-9]+)?$'
      THEN COALESCE(NULLIF(k.policy_json->>'weekly-budget-usd', ''), '0')::numeric
    ELSE 0
  END AS effective_weekly_budget_usd,
  CASE
    WHEN COALESCE(NULLIF(k.policy_json->>'concurrency-limit', ''), '0') ~ '^[0-9]+$'
      THEN COALESCE(NULLIF(k.policy_json->>'concurrency-limit', ''), '0')::integer
    ELSE 0
  END AS source_concurrency_limit,
  COALESCE(u.daily_usage_usd, 0) AS daily_usage_usd,
  COALESCE(u.weekly_usage_usd, 0) AS weekly_usage_usd,
  COALESCE(u.total_usage_usd, 0) AS total_usage_usd,
  u.last_used_at,
  u.daily_window_start,
  u.weekly_window_start,
  COALESCE(p.packages, '[]'::jsonb) AS token_packages,
  COALESCE(p.total_usd, 0) AS token_package_total_usd,
  COALESCE(p.spent_usd, 0) AS token_package_spent_usd,
  GREATEST(COALESCE(p.total_usd, 0) - COALESCE(p.spent_usd, 0), 0) AS token_package_remaining_usd
FROM _src_active_keys k
LEFT JOIN _src_groups g ON g.source_group_id = k.source_group_id
LEFT JOIN cliproxy_legacy_group_migration gm ON gm.source_group_id = k.source_group_id
LEFT JOIN _src_key_usage u ON u.api_key = k.api_key
LEFT JOIN _src_key_packages p ON p.api_key = k.api_key;

WITH upserted AS (
  INSERT INTO api_keys (
    user_id,
    key,
    name,
    group_id,
    status,
    created_at,
    updated_at,
    ip_whitelist,
    ip_blacklist,
    quota,
    quota_used,
    expires_at,
    last_used_at,
    rate_limit_5h,
    rate_limit_1d,
    rate_limit_7d,
    concurrency,
    allow_claude_family,
    allow_gpt_family,
    usage_5h,
    usage_1d,
    usage_7d,
    window_5h_start,
    window_1d_start,
    window_7d_start
  )
  SELECT
    (SELECT admin_user_id FROM _migration_context),
    m.api_key,
    left(COALESCE(NULLIF(m.policy_json->>'name', ''), NULLIF(m.owner_username, ''), 'CLIProxy ' || m.source_group_id || ' ' || left(m.api_key_hash, 8)), 100),
    m.target_group_id,
    'active',
    m.created_at,
    now(),
    '[]'::jsonb,
    '[]'::jsonb,
    CASE WHEN m.token_package_remaining_usd > 0 THEN m.token_package_total_usd ELSE 0 END,
    CASE WHEN m.token_package_remaining_usd > 0 THEN LEAST(m.token_package_spent_usd, m.token_package_total_usd) ELSE 0 END,
    m.expires_at,
    m.last_used_at,
    0,
    CASE
      WHEN m.token_package_remaining_usd > 0 OR m.source_group_id <> 'legacy-ungrouped' THEN 0
      ELSE m.effective_daily_budget_usd
    END,
    CASE
      WHEN m.token_package_remaining_usd > 0 OR m.source_group_id <> 'legacy-ungrouped' THEN 0
      ELSE m.effective_weekly_budget_usd
    END,
    CASE WHEN m.source_group_id = 'legacy-ungrouped' THEN m.source_concurrency_limit ELSE 0 END,
    NOT EXISTS (
      SELECT 1
      FROM jsonb_array_elements_text(
        CASE
          WHEN jsonb_typeof(m.policy_json->'excluded-models') = 'array'
            THEN m.policy_json->'excluded-models'
          ELSE '[]'::jsonb
        END
      ) AS excluded(pattern)
      WHERE lower(trim(excluded.pattern)) = 'claude-*'
    ),
    NOT EXISTS (
      SELECT 1
      FROM jsonb_array_elements_text(
        CASE
          WHEN jsonb_typeof(m.policy_json->'excluded-models') = 'array'
            THEN m.policy_json->'excluded-models'
          ELSE '[]'::jsonb
        END
      ) AS excluded(pattern)
      WHERE lower(trim(excluded.pattern)) IN ('gpt-*', 'chatgpt-*', 'o1*', 'o3*', 'o4*')
    ),
    0,
    CASE WHEN m.token_package_remaining_usd > 0 THEN 0 ELSE m.daily_usage_usd END,
    CASE WHEN m.token_package_remaining_usd > 0 THEN 0 ELSE m.weekly_usage_usd END,
    NULL,
    m.daily_window_start,
    CASE WHEN m.token_package_remaining_usd > 0 THEN NULL ELSE m.weekly_window_start END
  FROM _src_key_map m
  ON CONFLICT (key) DO UPDATE SET
    user_id = EXCLUDED.user_id,
    name = EXCLUDED.name,
    group_id = EXCLUDED.group_id,
    status = EXCLUDED.status,
    updated_at = now(),
    ip_whitelist = EXCLUDED.ip_whitelist,
    ip_blacklist = EXCLUDED.ip_blacklist,
    quota = EXCLUDED.quota,
    quota_used = EXCLUDED.quota_used,
    expires_at = EXCLUDED.expires_at,
    last_used_at = EXCLUDED.last_used_at,
    rate_limit_5h = EXCLUDED.rate_limit_5h,
    rate_limit_1d = EXCLUDED.rate_limit_1d,
    rate_limit_7d = EXCLUDED.rate_limit_7d,
    concurrency = EXCLUDED.concurrency,
    allow_claude_family = EXCLUDED.allow_claude_family,
    allow_gpt_family = EXCLUDED.allow_gpt_family,
    usage_5h = EXCLUDED.usage_5h,
    usage_1d = EXCLUDED.usage_1d,
    usage_7d = EXCLUDED.usage_7d,
    window_5h_start = EXCLUDED.window_5h_start,
    window_1d_start = EXCLUDED.window_1d_start,
    window_7d_start = EXCLUDED.window_7d_start
  RETURNING id, key, group_id
)
INSERT INTO cliproxy_legacy_api_key_migration (
  source_api_key_hash,
  api_key_id,
  source_group_id,
  target_group_id,
  source_policy_json,
  token_packages,
  token_package_total_usd,
  token_package_spent_usd,
  token_package_remaining_usd,
  source_expires_at,
  source_owner_username,
  source_owner_role,
  source_concurrency_limit,
  effective_daily_budget_usd,
  effective_weekly_budget_usd,
  daily_usage_usd,
  weekly_usage_usd,
  total_usage_usd,
  migration_tag
)
SELECT
  m.api_key_hash,
  u.id,
  m.source_group_id,
  u.group_id,
  m.policy_json,
  m.token_packages,
  m.token_package_total_usd,
  m.token_package_spent_usd,
  m.token_package_remaining_usd,
  m.expires_at,
  m.owner_username,
  m.owner_role,
  m.source_concurrency_limit,
  m.effective_daily_budget_usd,
  m.effective_weekly_budget_usd,
  m.daily_usage_usd,
  m.weekly_usage_usd,
  m.total_usage_usd,
  :'migration_tag'
FROM _src_key_map m
JOIN upserted u ON u.key = m.api_key
ON CONFLICT (source_api_key_hash) DO UPDATE SET
  api_key_id = EXCLUDED.api_key_id,
  source_group_id = EXCLUDED.source_group_id,
  target_group_id = EXCLUDED.target_group_id,
  source_policy_json = EXCLUDED.source_policy_json,
  token_packages = EXCLUDED.token_packages,
  token_package_total_usd = EXCLUDED.token_package_total_usd,
  token_package_spent_usd = EXCLUDED.token_package_spent_usd,
  token_package_remaining_usd = EXCLUDED.token_package_remaining_usd,
  source_expires_at = EXCLUDED.source_expires_at,
  source_owner_username = EXCLUDED.source_owner_username,
  source_owner_role = EXCLUDED.source_owner_role,
  source_concurrency_limit = EXCLUDED.source_concurrency_limit,
  effective_daily_budget_usd = EXCLUDED.effective_daily_budget_usd,
  effective_weekly_budget_usd = EXCLUDED.effective_weekly_budget_usd,
  daily_usage_usd = EXCLUDED.daily_usage_usd,
  weekly_usage_usd = EXCLUDED.weekly_usage_usd,
  total_usage_usd = EXCLUDED.total_usage_usd,
  migrated_at = now(),
  migration_tag = EXCLUDED.migration_tag;

WITH source_rows AS (
  SELECT
    u.id AS source_usage_event_id,
    ak.id AS api_key_id,
    ak.user_id,
    (SELECT account_id FROM _migration_context) AS account_id,
    ak.group_id,
    u.model,
    u.input_tokens,
    u.output_tokens,
    u.reasoning_tokens,
    u.cached_tokens,
    u.cost_micro_usd,
    u.failed,
    u.latency_ms,
    u.requested_at,
    m.api_key_hash
  FROM cliproxy_src_fdw.usage_events u
  JOIN _src_key_map m ON m.api_key = u.api_key
  JOIN api_keys ak ON ak.key = u.api_key
  LEFT JOIN cliproxy_legacy_usage_event_migration seen ON seen.source_usage_event_id = u.id
  WHERE seen.source_usage_event_id IS NULL
), inserted AS (
  INSERT INTO usage_logs (
    user_id,
    api_key_id,
    account_id,
    request_id,
    model,
    requested_model,
    group_id,
    input_tokens,
    output_tokens,
    cache_read_tokens,
    total_cost,
    actual_cost,
    duration_ms,
    created_at,
    stream,
    billing_type,
    rate_multiplier,
    cache_ttl_overridden,
    openai_ws_mode,
    request_type,
    inbound_endpoint
  )
  SELECT
    s.user_id,
    s.api_key_id,
    s.account_id,
    'cliproxy-legacy-' || s.source_usage_event_id::text,
    left(COALESCE(NULLIF(s.model, ''), 'unknown'), 100),
    left(COALESCE(NULLIF(s.model, ''), 'unknown'), 100),
    s.group_id,
    LEAST(GREATEST(COALESCE(s.input_tokens, 0), 0), 2147483647)::integer,
    LEAST(GREATEST(COALESCE(s.output_tokens, 0) + COALESCE(s.reasoning_tokens, 0), 0), 2147483647)::integer,
    LEAST(GREATEST(COALESCE(s.cached_tokens, 0), 0), 2147483647)::integer,
    COALESCE(s.cost_micro_usd, 0)::numeric / 1000000.0,
    COALESCE(s.cost_micro_usd, 0)::numeric / 1000000.0,
    CASE WHEN s.latency_ms IS NULL OR s.latency_ms < 0 OR s.latency_ms > 2147483647 THEN NULL ELSE s.latency_ms::integer END,
    to_timestamp(s.requested_at),
    false,
    0,
    1,
    false,
    false,
    0,
    'cliproxy_legacy'
  FROM source_rows s
  RETURNING id, request_id
)
INSERT INTO cliproxy_legacy_usage_event_migration (
  source_usage_event_id,
  usage_log_id,
  source_api_key_hash,
  migration_tag
)
SELECT
  replace(i.request_id, 'cliproxy-legacy-', '')::bigint,
  i.id,
  s.api_key_hash,
  :'migration_tag'
FROM inserted i
JOIN source_rows s ON s.source_usage_event_id = replace(i.request_id, 'cliproxy-legacy-', '')::bigint
ON CONFLICT (source_usage_event_id) DO NOTHING;

SELECT 'migration_summary.active_source_keys' AS metric, count(*)::text AS value FROM _src_key_map
UNION ALL
SELECT 'migration_summary.target_admin_api_keys', count(*)::text FROM api_keys WHERE user_id = (SELECT admin_user_id FROM _migration_context) AND deleted_at IS NULL
UNION ALL
SELECT 'migration_summary.legacy_group_mappings', count(*)::text FROM cliproxy_legacy_group_migration
UNION ALL
SELECT 'migration_summary.legacy_key_mappings', count(*)::text FROM cliproxy_legacy_api_key_migration
UNION ALL
SELECT 'migration_summary.legacy_usage_event_mappings', count(*)::text FROM cliproxy_legacy_usage_event_migration
UNION ALL
SELECT 'migration_summary.token_package_keys', count(*)::text FROM _src_key_map WHERE token_package_total_usd > 0
UNION ALL
SELECT 'migration_summary.active_token_package_keys', count(*)::text FROM _src_key_map WHERE token_package_remaining_usd > 0;

SELECT
  'group_summary' AS row_type,
  m.source_group_id,
  g.id::text AS target_group_id,
  g.name,
  g.concurrency::text AS target_group_concurrency,
  count(ak.id)::text AS api_key_count,
  COALESCE(sum(ak.rate_limit_1d), 0)::text AS total_daily_limit_usd,
  COALESCE(sum(ak.usage_1d), 0)::text AS total_daily_usage_usd,
  COALESCE(sum(ak.rate_limit_7d), 0)::text AS total_weekly_limit_usd,
  COALESCE(sum(ak.usage_7d), 0)::text AS total_weekly_usage_usd
FROM cliproxy_legacy_group_migration m
JOIN groups g ON g.id = m.target_group_id
LEFT JOIN api_keys ak ON ak.group_id = g.id AND ak.deleted_at IS NULL
GROUP BY m.source_group_id, g.id, g.name, g.concurrency
ORDER BY m.source_group_id;

DROP SCHEMA cliproxy_src_fdw CASCADE;
DROP SERVER cliproxy_src_fdw CASCADE;

\if :commit
COMMIT;
\echo 'COMMITTED migrate_sub2api_admin_api_keys'
\else
ROLLBACK;
\echo 'ROLLED BACK migrate_sub2api_admin_api_keys dry-run'
\endif
