CREATE TABLE IF NOT EXISTS session_projection_reseeds (
    input_digest TEXT PRIMARY KEY CHECK (length(input_digest) = 64),
    public_model TEXT NOT NULL,
    first_export_hour TIMESTAMPTZ NOT NULL,
    last_export_hour TIMESTAMPTZ NOT NULL,
    archive_count BIGINT NOT NULL CHECK (archive_count > 0),
    session_count BIGINT NOT NULL CHECK (session_count >= 0),
    record_count BIGINT NOT NULL CHECK (record_count >= 0),
    source_archives JSONB NOT NULL,
    previous_latest_checkpoint_hour TIMESTAMPTZ,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (last_export_hour >= first_export_hour)
);

CREATE TABLE IF NOT EXISTS session_projection_reseed_backups (
    input_digest TEXT NOT NULL REFERENCES session_projection_reseeds(input_digest),
    session_id TEXT NOT NULL,
    checkpoint_version INTEGER NOT NULL,
    checkpoint_zstd BYTEA NOT NULL,
    checkpoint_sha256 CHAR(64) NOT NULL,
    last_export_hour TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (input_digest, session_id)
);

