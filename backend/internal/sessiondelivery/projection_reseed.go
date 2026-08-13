package sessiondelivery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

// ProjectionReseedArchive identifies one immutable input in a checkpoint
// reseed. It intentionally excludes local paths from the persistent audit row.
type ProjectionReseedArchive struct {
	Hour    time.Time `json:"hour"`
	SHA256  string    `json:"sha256"`
	Size    int64     `json:"size"`
	Records int64     `json:"records"`
}

// ProjectionReseedResult is a content-free report for a dry-run or applied
// full-history checkpoint rebuild.
type ProjectionReseedResult struct {
	InputDir       string                    `json:"input_dir"`
	InputDigest    string                    `json:"input_digest"`
	PublicModel    string                    `json:"public_model"`
	FirstHour      time.Time                 `json:"first_hour"`
	LastHour       time.Time                 `json:"last_hour"`
	Archives       []ProjectionReseedArchive `json:"archives"`
	Sessions       int64                     `json:"sessions"`
	Records        int64                     `json:"records"`
	Applied        bool                      `json:"applied"`
	AlreadyApplied bool                      `json:"already_applied"`
}

type projectionReseedSession struct {
	echo          *echoRepair
	usage         *usageProjector
	lastTimestamp time.Time
}

// ReseedProjectionArchives rebuilds continuation checkpoints from a complete,
// strictly audited archive sequence. The default dry-run performs no database
// writes. Apply mode serializes with exporters, keeps transactionally linked
// copies of every replaced checkpoint, and refuses to regress newer state.
func (s *Store) ReseedProjectionArchives(
	ctx context.Context,
	inputValue string,
	publicModel string,
	apply bool,
) (*ProjectionReseedResult, error) {
	inputValue = strings.TrimSpace(inputValue)
	if inputValue == "" {
		return nil, errors.New("projection reseed input directory is required")
	}
	inputDir, err := filepath.Abs(inputValue)
	if err != nil {
		return nil, fmt.Errorf("resolve projection reseed input directory: %w", err)
	}
	publicModel = strings.TrimSpace(publicModel)
	if publicModel == "" {
		publicModel = DefaultPublicModel
	}
	inputs, err := inventoryRebuildInputs(ctx, inputDir, publicModel)
	if err != nil {
		return nil, err
	}
	audit, err := AuditArchivesFidelity(ctx, inputDir, publicModel)
	if err != nil {
		return nil, fmt.Errorf("audit projection reseed inputs: %w", err)
	}
	if !audit.Passed {
		return nil, fmt.Errorf("projection reseed inputs have %d fidelity violation(s)", audit.ViolationCount)
	}

	result, states, err := buildProjectionReseedState(ctx, inputDir, publicModel, inputs)
	if err != nil {
		return nil, err
	}
	if !apply {
		return result, nil
	}

	unlock, err := s.LockProjectionExport(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := verifyProjectionReseedInputs(inputs); err != nil {
		return nil, err
	}
	alreadyApplied, err := s.commitProjectionReseed(ctx, result, states)
	if err != nil {
		return nil, err
	}
	result.Applied = true
	result.AlreadyApplied = alreadyApplied
	return result, nil
}

func buildProjectionReseedState(
	ctx context.Context,
	inputDir, publicModel string,
	inputs []rebuildArchiveInput,
) (*ProjectionReseedResult, map[string]*projectionReseedSession, error) {
	states := make(map[string]*projectionReseedSession)
	digest := sha256.New()
	result := &ProjectionReseedResult{
		InputDir:    inputDir,
		PublicModel: publicModel,
		FirstHour:   inputs[0].hour,
		LastHour:    inputs[len(inputs)-1].hour,
	}
	for _, input := range inputs {
		appendProjectionReseedDigest(digest, input)
		result.Archives = append(result.Archives, ProjectionReseedArchive{
			Hour: input.hour, SHA256: input.sha256, Size: input.size,
			Records: input.validation.Manifest.DeliveryCount,
		})
		err := forEachArchiveSession(input.path, func(sessionID string, records []*DeliveryRecord) error {
			state := states[sessionID]
			if state == nil {
				state = &projectionReseedSession{echo: &echoRepair{}, usage: &usageProjector{}}
				states[sessionID] = state
			}
			for _, record := range records {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !state.lastTimestamp.IsZero() && record.Timestamp.Before(state.lastTimestamp) {
					return fmt.Errorf("projection reseed session %s is not ordered", sessionID)
				}
				if err := state.echo.process(record); err != nil {
					return fmt.Errorf("reseed echo projection for %s: %w", record.RequestID, err)
				}
				if err := state.usage.process(record); err != nil {
					return fmt.Errorf("reseed usage projection for %s: %w", record.RequestID, err)
				}
				state.lastTimestamp = record.Timestamp
				result.Records++
			}
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("read projection reseed archive %s: %w", filepath.Base(input.path), err)
		}
	}
	result.InputDigest = hex.EncodeToString(digest.Sum(nil))
	result.Sessions = int64(len(states))
	return result, states, nil
}

func appendProjectionReseedDigest(digest hash.Hash, input rebuildArchiveInput) {
	_, _ = fmt.Fprintf(digest, "%s|%s|%d\n", input.hour.Format(time.RFC3339), input.sha256, input.size)
}

func verifyProjectionReseedInputs(inputs []rebuildArchiveInput) error {
	for _, input := range inputs {
		sha, size, err := fileSHA256(input.path)
		if err != nil {
			return fmt.Errorf("re-hash projection reseed input %s: %w", filepath.Base(input.path), err)
		}
		if sha != input.sha256 || size != input.size {
			return fmt.Errorf("projection reseed input %s changed after validation", filepath.Base(input.path))
		}
	}
	return nil
}

func (s *Store) commitProjectionReseed(
	ctx context.Context,
	result *ProjectionReseedResult,
	states map[string]*projectionReseedSession,
) (bool, error) {
	sessionIDs := make([]string, 0, len(states))
	for sessionID := range states {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	encoded := make([]encodedProjectionCheckpoint, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		checkpoint := checkpointFromProjectors(states[sessionID].echo, states[sessionID].usage)
		value, err := encodeProjectionCheckpoint(sessionID, checkpoint, result.LastHour)
		if err != nil {
			return false, fmt.Errorf("prepare reseeded projection checkpoint %s: %w", sessionID, err)
		}
		encoded = append(encoded, value)
	}
	archiveJSON, err := json.Marshal(result.Archives)
	if err != nil {
		return false, fmt.Errorf("encode projection reseed archive audit: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin projection reseed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var alreadyApplied bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM session_projection_reseeds WHERE input_digest = $1)`,
		result.InputDigest,
	).Scan(&alreadyApplied); err != nil {
		return false, fmt.Errorf("check projection reseed idempotency: %w", err)
	}
	if alreadyApplied {
		return true, nil
	}

	var newestCheckpoint, oldestBatch, newestBatch sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(last_export_hour) FROM session_projection_checkpoints`,
	).Scan(&newestCheckpoint); err != nil {
		return false, fmt.Errorf("read latest projection checkpoint hour: %w", err)
	}
	if newestCheckpoint.Valid && newestCheckpoint.Time.UTC().After(result.LastHour) {
		return false, fmt.Errorf(
			"projection reseed ending at %s cannot replace newer checkpoint hour %s",
			result.LastHour.Format(time.RFC3339), newestCheckpoint.Time.UTC().Format(time.RFC3339),
		)
	}
	var durableBatchCount int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(export_hour), MAX(export_hour)
		FROM session_export_batches WHERE status IN ('verified', 'purged')`,
	).Scan(&durableBatchCount, &oldestBatch, &newestBatch); err != nil {
		return false, fmt.Errorf("read durable export range: %w", err)
	}
	if !oldestBatch.Valid || !newestBatch.Valid ||
		!hourUTC(oldestBatch.Time).Equal(result.FirstHour) ||
		!hourUTC(newestBatch.Time).Equal(result.LastHour) ||
		durableBatchCount != int64(len(result.Archives)) {
		return false, errors.New("projection reseed inputs must cover every durable export hour")
	}
	for _, archive := range result.Archives {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM session_export_batches
				WHERE export_hour = $1 AND status IN ('verified', 'purged')
			)`, archive.Hour).Scan(&exists); err != nil {
			return false, fmt.Errorf("verify durable export hour %s: %w", archive.Hour.Format(time.RFC3339), err)
		}
		if !exists {
			return false, fmt.Errorf("projection reseed input hour %s has no durable export batch", archive.Hour.Format(time.RFC3339))
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_projection_reseeds (
			input_digest, public_model, first_export_hour, last_export_hour,
			archive_count, session_count, record_count, source_archives,
			previous_latest_checkpoint_hour
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		result.InputDigest, result.PublicModel, result.FirstHour, result.LastHour,
		len(result.Archives), result.Sessions, result.Records, string(archiveJSON), newestCheckpoint,
	)
	if err != nil {
		return false, fmt.Errorf("record projection reseed audit: %w", err)
	}
	if len(sessionIDs) > 0 {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO session_projection_reseed_backups (
				input_digest, session_id, checkpoint_version, checkpoint_zstd,
				checkpoint_sha256, last_export_hour, updated_at
			)
			SELECT $1, session_id, checkpoint_version, checkpoint_zstd,
			       checkpoint_sha256, last_export_hour, updated_at
			FROM session_projection_checkpoints
			WHERE session_id = ANY($2::text[])`, result.InputDigest, pq.Array(sessionIDs))
		if err != nil {
			return false, fmt.Errorf("back up projection checkpoints before reseed: %w", err)
		}
	}
	for _, checkpoint := range encoded {
		rows, err := tx.ExecContext(ctx, `
			INSERT INTO session_projection_checkpoints (
				session_id, checkpoint_version, checkpoint_zstd,
				checkpoint_sha256, last_export_hour, updated_at
			) VALUES ($1, $2, $3, $4, $5, NOW())
			ON CONFLICT (session_id) DO UPDATE SET
				checkpoint_version = EXCLUDED.checkpoint_version,
				checkpoint_zstd = EXCLUDED.checkpoint_zstd,
				checkpoint_sha256 = EXCLUDED.checkpoint_sha256,
				last_export_hour = EXCLUDED.last_export_hour,
				updated_at = NOW()
			WHERE session_projection_checkpoints.last_export_hour <= EXCLUDED.last_export_hour`,
			checkpoint.SessionID, checkpoint.Version, checkpoint.Compressed,
			checkpoint.SHA256, checkpoint.LastExportHour,
		)
		if err != nil {
			return false, fmt.Errorf("replace projection checkpoint %s: %w", checkpoint.SessionID, err)
		}
		count, err := rows.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("inspect replaced projection checkpoint %s: %w", checkpoint.SessionID, err)
		}
		if count != 1 {
			return false, fmt.Errorf("projection checkpoint %s advanced during reseed", checkpoint.SessionID)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit projection reseed: %w", err)
	}
	return false, nil
}
