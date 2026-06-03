-- Backfill token counters for token package usage rows created before migration 147.
-- Tokens are attributed proportionally by the package-paid cost over the request actual cost.

UPDATE api_key_token_package_usage t
SET
  input_tokens = GREATEST(0, ROUND(u.input_tokens * t.cost_usd / NULLIF(u.actual_cost, 0)))::BIGINT,
  output_tokens = GREATEST(0, ROUND(u.output_tokens * t.cost_usd / NULLIF(u.actual_cost, 0)))::BIGINT,
  cache_creation_tokens = GREATEST(0, ROUND(u.cache_creation_tokens * t.cost_usd / NULLIF(u.actual_cost, 0)))::BIGINT,
  cache_read_tokens = GREATEST(0, ROUND(u.cache_read_tokens * t.cost_usd / NULLIF(u.actual_cost, 0)))::BIGINT,
  total_tokens = GREATEST(0, ROUND(
    (u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens)
    * t.cost_usd / NULLIF(u.actual_cost, 0)
  ))::BIGINT
FROM usage_logs u
WHERE t.request_id IS NOT NULL
  AND t.total_tokens = 0
  AND u.request_id = t.request_id
  AND u.api_key_id = t.api_key_id
  AND u.actual_cost > 0;
