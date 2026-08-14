ALTER TABLE session_records
    ADD COLUMN IF NOT EXISTS delivery_total_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS delivery_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS delivery_tokens_counted BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE session_records
    DROP CONSTRAINT IF EXISTS session_records_delivery_token_metrics_nonnegative;

ALTER TABLE session_records
    ADD CONSTRAINT session_records_delivery_token_metrics_nonnegative CHECK (
        delivery_total_input_tokens >= 0 AND delivery_output_tokens >= 0
    ) NOT VALID;

ALTER TABLE session_export_batches
    ADD COLUMN IF NOT EXISTS delivery_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS delivery_cache_creation_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS delivery_cache_read_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS delivery_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS delivery_tokens_counted BIGINT NOT NULL DEFAULT 0;

ALTER TABLE session_export_batches
    DROP CONSTRAINT IF EXISTS session_export_batches_delivery_token_metrics_nonnegative;

ALTER TABLE session_export_batches
    ADD CONSTRAINT session_export_batches_delivery_token_metrics_nonnegative CHECK (
        delivery_input_tokens >= 0 AND
        delivery_cache_creation_input_tokens >= 0 AND
        delivery_cache_read_input_tokens >= 0 AND
        delivery_output_tokens >= 0 AND
        delivery_tokens_counted >= 0 AND
        delivery_tokens_counted <= delivery_count
    ) NOT VALID;
