-- Add dedicated unlimited mode for groups.
-- These groups still record usage, but internal amount and frequency limits are skipped.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS dedicated_unlimited boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_groups_dedicated_unlimited
    ON groups (dedicated_unlimited)
    WHERE deleted_at IS NULL;
