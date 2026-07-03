-- Add API key-level image generation policy.
ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS allow_image_generation BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN api_keys.allow_image_generation IS
  'Whether this API key may use image generation endpoints and tools';

CREATE INDEX IF NOT EXISTS idx_api_keys_allow_image_generation
  ON api_keys (allow_image_generation);
