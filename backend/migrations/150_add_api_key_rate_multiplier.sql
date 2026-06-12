-- Add an admin-only API key billing multiplier.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS rate_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1.0000;

ALTER TABLE api_keys
    ALTER COLUMN rate_multiplier SET DEFAULT 1.0000;

UPDATE api_keys
SET rate_multiplier = 1.0000
WHERE rate_multiplier IS NULL OR rate_multiplier <= 0;

ALTER TABLE api_keys
    ALTER COLUMN rate_multiplier SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'api_keys_rate_multiplier_positive'
    ) THEN
        ALTER TABLE api_keys
            ADD CONSTRAINT api_keys_rate_multiplier_positive CHECK (rate_multiplier > 0);
    END IF;
END $$;

COMMENT ON COLUMN api_keys.rate_multiplier IS 'Admin-only API key billing multiplier; user-facing APIs expose only multiplied consumption.';
