CREATE TABLE IF NOT EXISTS session_record_keys (
    record_id TEXT PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS session_records (
    ingested_at TIMESTAMPTZ NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    record_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    source_protocol TEXT NOT NULL CHECK (source_protocol IN ('anthropic_messages', 'openai_responses')),
    source_endpoint TEXT NOT NULL,
    api_key_id BIGINT NOT NULL DEFAULT 0,
    user_id BIGINT NOT NULL DEFAULT 0,
    group_id BIGINT NOT NULL DEFAULT 0,
    http_status INTEGER NOT NULL,
    duration_ms BIGINT NOT NULL,
    deliverable BOOLEAN NOT NULL,
    rejection_code TEXT NOT NULL DEFAULT '',
    schema_version INTEGER NOT NULL,
    payload_zstd BYTEA NOT NULL,
    payload_sha256 CHAR(64) NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (ingested_at, record_id)
) PARTITION BY RANGE (ingested_at);

CREATE INDEX IF NOT EXISTS session_records_session_order_idx
    ON session_records (session_id, occurred_at, request_id);

CREATE INDEX IF NOT EXISTS session_records_delivery_day_idx
    ON session_records (ingested_at, deliverable);

CREATE TABLE IF NOT EXISTS session_export_batches (
    export_day DATE PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('exporting', 'archived', 'verified', 'failed', 'purged')),
    attempt_id TEXT NOT NULL DEFAULT '',
    record_count BIGINT NOT NULL DEFAULT 0,
    delivery_count BIGINT NOT NULL DEFAULT 0,
    rejected_count BIGINT NOT NULL DEFAULT 0,
    archive_backend TEXT NOT NULL DEFAULT '',
    archive_object TEXT NOT NULL DEFAULT '',
    archive_sha256 CHAR(64) NOT NULL DEFAULT '',
    archive_size BIGINT NOT NULL DEFAULT 0,
    manifest JSONB,
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    purged_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS session_export_batches_status_idx
    ON session_export_batches (status, export_day);
