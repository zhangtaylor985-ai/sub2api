-- Add API key-level OpenAI Messages dispatch model override.
-- Empty JSON means the key inherits its group's dispatch mapping.

ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS messages_dispatch_model_config JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN api_keys.messages_dispatch_model_config IS
  'API key-level OpenAI Messages dispatch model override; empty object inherits group mapping';
