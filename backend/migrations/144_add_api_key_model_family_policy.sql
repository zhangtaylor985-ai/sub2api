-- Add API-key level model family policy.
-- The policy is evaluated against the client-requested endpoint/model family,
-- not against internal upstream routing targets.

ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS allow_claude_family BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS allow_gpt_family BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN api_keys.allow_claude_family IS
  'Whether this API key may request Claude-family models from user-facing endpoints';
COMMENT ON COLUMN api_keys.allow_gpt_family IS
  'Whether this API key may request GPT/OpenAI-family models from user-facing endpoints';

CREATE INDEX IF NOT EXISTS idx_api_keys_model_family_policy_active
  ON api_keys (allow_claude_family, allow_gpt_family)
  WHERE deleted_at IS NULL;

DO $$
BEGIN
  IF to_regclass('public.cliproxy_legacy_api_key_migration') IS NOT NULL THEN
    UPDATE api_keys k
    SET
      allow_claude_family = NOT EXISTS (
        SELECT 1
        FROM jsonb_array_elements_text(
          CASE
            WHEN jsonb_typeof(m.source_policy_json->'excluded-models') = 'array'
              THEN m.source_policy_json->'excluded-models'
            ELSE '[]'::jsonb
          END
        ) AS excluded(pattern)
        WHERE lower(trim(excluded.pattern)) = 'claude-*'
      ),
      allow_gpt_family = NOT EXISTS (
        SELECT 1
        FROM jsonb_array_elements_text(
          CASE
            WHEN jsonb_typeof(m.source_policy_json->'excluded-models') = 'array'
              THEN m.source_policy_json->'excluded-models'
            ELSE '[]'::jsonb
          END
        ) AS excluded(pattern)
        WHERE lower(trim(excluded.pattern)) IN ('gpt-*', 'chatgpt-*', 'o1*', 'o3*', 'o4*')
      )
    FROM cliproxy_legacy_api_key_migration m
    WHERE m.api_key_id = k.id
      AND k.deleted_at IS NULL
      AND m.source_policy_json IS NOT NULL;
  END IF;
END $$;
