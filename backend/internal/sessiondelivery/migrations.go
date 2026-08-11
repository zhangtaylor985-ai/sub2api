package sessiondelivery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var sessionMigrations embed.FS

const sessionMigrationAdvisoryLock int64 = 7304272051667002

const sessionSchemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS session_schema_migrations (
    filename TEXT PRIMARY KEY,
    checksum CHAR(64) NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

func ApplySessionMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("nil Session database")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve Session migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, sessionMigrationAdvisoryLock); err != nil {
		return fmt.Errorf("lock Session migrations: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, sessionMigrationAdvisoryLock)
	}()
	if _, err := conn.ExecContext(ctx, sessionSchemaMigrationsDDL); err != nil {
		return fmt.Errorf("create session_schema_migrations: %w", err)
	}

	files, err := fs.Glob(sessionMigrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list Session migrations: %w", err)
	}
	sort.Strings(files)
	for _, name := range files {
		content, err := fs.ReadFile(sessionMigrations, name)
		if err != nil {
			return fmt.Errorf("read Session migration %s: %w", name, err)
		}
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			continue
		}
		digest := sha256.Sum256([]byte(trimmed))
		checksum := hex.EncodeToString(digest[:])

		var existing string
		err = conn.QueryRowContext(ctx, `SELECT checksum FROM session_schema_migrations WHERE filename = $1`, name).Scan(&existing)
		if err == nil {
			if existing != checksum {
				return fmt.Errorf("Session migration %s checksum mismatch (db=%s file=%s)", name, existing, checksum)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check Session migration %s: %w", name, err)
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin Session migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, trimmed); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply Session migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO session_schema_migrations (filename, checksum) VALUES ($1, $2)`, name, checksum); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record Session migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit Session migration %s: %w", name, err)
		}
	}
	return nil
}
