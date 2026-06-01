ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS concurrency INT NOT NULL DEFAULT 0;

COMMENT ON COLUMN api_keys.concurrency IS 'API key concurrency limit (0 = inherit from group/user)';

ALTER TABLE groups
  ADD COLUMN IF NOT EXISTS concurrency INT NOT NULL DEFAULT 0;

COMMENT ON COLUMN groups.concurrency IS 'Group-level API key concurrency limit (0 = fallback to user concurrency)';

CREATE INDEX IF NOT EXISTS idx_api_keys_concurrency_active
  ON api_keys(concurrency)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_groups_concurrency_active
  ON groups(concurrency)
  WHERE deleted_at IS NULL;
