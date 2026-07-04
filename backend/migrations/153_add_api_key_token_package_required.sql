ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS token_package_required BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_api_keys_token_package_required
  ON api_keys (token_package_required)
  WHERE token_package_required = TRUE AND deleted_at IS NULL;

COMMENT ON COLUMN api_keys.token_package_required IS
  'When true, the API key can only spend from active token packages and is blocked when no package balance remains';
