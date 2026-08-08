-- Require every API key to have an expiration time.
-- Deleted legacy rows are backfilled with a past timestamp so they remain expired.
UPDATE api_keys
SET expires_at = COALESCE(deleted_at, updated_at, created_at, NOW())
WHERE expires_at IS NULL;

ALTER TABLE api_keys
    ALTER COLUMN expires_at SET DEFAULT (CURRENT_TIMESTAMP + INTERVAL '30 days');

ALTER TABLE api_keys
    ALTER COLUMN expires_at SET NOT NULL;
