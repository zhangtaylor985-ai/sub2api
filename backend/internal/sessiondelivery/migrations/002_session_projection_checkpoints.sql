CREATE TABLE IF NOT EXISTS session_projection_checkpoints (
    session_id TEXT PRIMARY KEY,
    checkpoint_version INTEGER NOT NULL,
    checkpoint_zstd BYTEA NOT NULL,
    checkpoint_sha256 CHAR(64) NOT NULL,
    last_export_hour TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS session_projection_checkpoints_export_hour_idx
    ON session_projection_checkpoints (last_export_hour);

CREATE TABLE IF NOT EXISTS session_projection_seeded_archives (
    archive_sha256 TEXT PRIMARY KEY,
    export_hour TIMESTAMPTZ NOT NULL UNIQUE,
    session_count BIGINT NOT NULL CHECK (session_count >= 0),
    record_count BIGINT NOT NULL CHECK (record_count >= 0),
    seeded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
