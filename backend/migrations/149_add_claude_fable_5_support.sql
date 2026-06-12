-- Add Claude Fable 5 passthrough support to native Anthropic accounts.
--
-- Anthropic accounts with a non-empty model_mapping use it as a whitelist.
-- Keep existing per-account mappings intact and add only the new passthrough key.
UPDATE accounts
SET credentials = jsonb_set(
    COALESCE(credentials, '{}'::jsonb),
    '{model_mapping}',
    COALESCE(credentials->'model_mapping', '{}'::jsonb) || jsonb_build_object('claude-fable-5', 'claude-fable-5'),
    true
  ),
  updated_at = NOW()
WHERE deleted_at IS NULL
  AND platform = 'anthropic'
  AND jsonb_typeof(COALESCE(credentials->'model_mapping', '{}'::jsonb)) = 'object';
