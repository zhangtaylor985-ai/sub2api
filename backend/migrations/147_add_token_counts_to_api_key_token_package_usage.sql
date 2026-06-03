-- Add token counters to API key token package usage rows.
-- The counters are proportional to the portion of the request cost paid by packages.

ALTER TABLE api_key_token_package_usage
  ADD COLUMN IF NOT EXISTS input_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS output_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cache_read_tokens BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS total_tokens BIGINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN api_key_token_package_usage.input_tokens IS 'Input tokens attributed to this token package debit';
COMMENT ON COLUMN api_key_token_package_usage.output_tokens IS 'Output tokens attributed to this token package debit';
COMMENT ON COLUMN api_key_token_package_usage.cache_creation_tokens IS 'Cache creation tokens attributed to this token package debit';
COMMENT ON COLUMN api_key_token_package_usage.cache_read_tokens IS 'Cache read tokens attributed to this token package debit';
COMMENT ON COLUMN api_key_token_package_usage.total_tokens IS 'Total tokens attributed to this token package debit';
