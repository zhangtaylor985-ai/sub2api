package sessiondelivery

import (
	"archive/tar"
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// ProjectionSeedResult describes a one-time continuation-state rebuild from a
// previously verified archive. The operation never rewrites the archive.
type ProjectionSeedResult struct {
	Hour          time.Time `json:"hour"`
	ArchiveSHA256 string    `json:"archive_sha256"`
	Sessions      int64     `json:"sessions"`
	Records       int64     `json:"records"`
	AlreadySeeded bool      `json:"already_seeded"`
}

// SeedProjectionArchive rebuilds hourly export continuation state from an
// archive that was durably verified before projection checkpoints existed.
// The local file must byte-match the archive registered for its manifest hour.
func (s *Store) SeedProjectionArchive(
	ctx context.Context,
	archivePath string,
	publicModel string,
	allow bool,
) (ProjectionSeedResult, error) {
	if !allow {
		return ProjectionSeedResult{}, errors.New("projection seed requires an explicit allow-seed flag")
	}
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" {
		return ProjectionSeedResult{}, errors.New("projection seed archive path is required")
	}
	publicModel = strings.TrimSpace(publicModel)
	if publicModel == "" {
		publicModel = DefaultPublicModel
	}
	validation, err := ValidateArchive(archivePath, publicModel)
	if err != nil {
		return ProjectionSeedResult{}, fmt.Errorf("validate projection seed archive: %w", err)
	}
	hour, err := time.Parse(time.RFC3339, validation.Manifest.ExportHour)
	if err != nil {
		return ProjectionSeedResult{}, fmt.Errorf("parse projection seed hour: %w", err)
	}
	archiveSHA, archiveSize, err := fileSHA256(archivePath)
	if err != nil {
		return ProjectionSeedResult{}, fmt.Errorf("hash projection seed archive: %w", err)
	}
	hour = hourUTC(hour)

	unlock, err := s.LockProjectionExport(ctx)
	if err != nil {
		return ProjectionSeedResult{}, err
	}
	defer unlock()
	if result, found, err := s.projectionSeedResult(ctx, archiveSHA); err != nil {
		return ProjectionSeedResult{}, err
	} else if found {
		result.AlreadySeeded = true
		return result, nil
	}

	batch, err := s.GetExportBatch(ctx, hour)
	if err != nil {
		return ProjectionSeedResult{}, err
	}
	if err := validateProjectionSeedBatch(batch, archiveSHA, archiveSize); err != nil {
		return ProjectionSeedResult{}, err
	}

	checkpoints := make(map[string]projectionCheckpoint)
	var recordCount int64
	err = forEachArchiveSession(archivePath, func(sessionID string, records []*DeliveryRecord) error {
		checkpoint, found, err := s.LoadProjectionCheckpoint(ctx, sessionID, hour)
		if err != nil {
			return err
		}
		if !found {
			checkpoint = projectionCheckpoint{Version: projectionCheckpointVersion}
		}
		echo := &echoRepair{}
		echo.restore(sessionID, checkpoint.Echo)
		usage := &usageProjector{}
		if err := usage.restore(sessionID, checkpoint.Usage); err != nil {
			return err
		}
		for _, record := range records {
			if err := echo.process(record); err != nil {
				return fmt.Errorf("seed echo projection for %s: %w", record.RequestID, err)
			}
			if err := usage.process(record); err != nil {
				return fmt.Errorf("seed usage projection for %s: %w", record.RequestID, err)
			}
			recordCount++
		}
		checkpoints[sessionID] = checkpointFromProjectors(echo, usage)
		return nil
	})
	if err != nil {
		return ProjectionSeedResult{}, fmt.Errorf("read projection seed archive: %w", err)
	}
	if recordCount != validation.Manifest.DeliveryCount {
		return ProjectionSeedResult{}, errors.New("projection seed record count does not match archive manifest")
	}
	result := ProjectionSeedResult{
		Hour:          hour,
		ArchiveSHA256: archiveSHA,
		Sessions:      int64(len(checkpoints)),
		Records:       recordCount,
	}
	alreadySeeded, err := s.commitProjectionSeed(ctx, batch, result, checkpoints)
	if err != nil {
		return ProjectionSeedResult{}, err
	}
	result.AlreadySeeded = alreadySeeded
	return result, nil
}

func validateProjectionSeedBatch(batch *ExportBatch, archiveSHA string, archiveSize int64) error {
	if batch == nil || (batch.Status != "verified" && batch.Status != "purged") {
		return errors.New("projection seed requires a verified or purged durable archive batch")
	}
	if !strings.EqualFold(strings.TrimSpace(batch.ArchiveSHA256), strings.TrimSpace(archiveSHA)) {
		return ErrArchiveHashMismatch
	}
	if batch.ArchiveSize != archiveSize {
		return errors.New("projection seed archive size does not match the verified batch")
	}
	return nil
}

func (s *Store) projectionSeedResult(ctx context.Context, archiveSHA string) (ProjectionSeedResult, bool, error) {
	var result ProjectionSeedResult
	err := s.db.QueryRowContext(ctx, `
		SELECT export_hour, archive_sha256, session_count, record_count
		FROM session_projection_seeded_archives
		WHERE archive_sha256 = $1`, strings.TrimSpace(archiveSHA)).Scan(
		&result.Hour, &result.ArchiveSHA256, &result.Sessions, &result.Records,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectionSeedResult{}, false, nil
	}
	if err != nil {
		return ProjectionSeedResult{}, false, fmt.Errorf("read projection seed state: %w", err)
	}
	return result, true, nil
}

func (s *Store) commitProjectionSeed(
	ctx context.Context,
	batch *ExportBatch,
	result ProjectionSeedResult,
	checkpoints map[string]projectionCheckpoint,
) (bool, error) {
	sessionIDs := make([]string, 0, len(checkpoints))
	for sessionID := range checkpoints {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	encoded := make([]encodedProjectionCheckpoint, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		checkpoint, err := encodeProjectionCheckpoint(sessionID, checkpoints[sessionID], result.Hour)
		if err != nil {
			return false, fmt.Errorf("prepare seeded projection checkpoint %s: %w", sessionID, err)
		}
		encoded = append(encoded, checkpoint)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin projection seed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var status, archiveSHA string
	var archiveSize int64
	if err := tx.QueryRowContext(ctx, `
		SELECT status, archive_sha256, archive_size
		FROM session_export_batches
		WHERE export_hour = $1
		FOR SHARE`, hourUTC(result.Hour)).Scan(&status, &archiveSHA, &archiveSize); err != nil {
		return false, fmt.Errorf("lock projection seed batch: %w", err)
	}
	lockedBatch := &ExportBatch{Status: status, ArchiveSHA256: archiveSHA, ArchiveSize: archiveSize}
	if err := validateProjectionSeedBatch(lockedBatch, result.ArchiveSHA256, batch.ArchiveSize); err != nil {
		return false, err
	}
	var alreadySeeded bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM session_projection_seeded_archives WHERE archive_sha256 = $1
		)`, result.ArchiveSHA256).Scan(&alreadySeeded); err != nil {
		return false, fmt.Errorf("check projection seed idempotency: %w", err)
	}
	if alreadySeeded {
		return true, nil
	}
	var newestCheckpoint sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(last_export_hour) FROM session_projection_checkpoints`).Scan(&newestCheckpoint); err != nil {
		return false, fmt.Errorf("read latest projection checkpoint hour: %w", err)
	}
	if newestCheckpoint.Valid && !newestCheckpoint.Time.UTC().Before(hourUTC(result.Hour)) {
		return false, fmt.Errorf(
			"projection seed hour %s is not after latest checkpoint hour %s",
			hourUTC(result.Hour).Format(time.RFC3339), newestCheckpoint.Time.UTC().Format(time.RFC3339),
		)
	}
	var newestSeed sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(export_hour) FROM session_projection_seeded_archives`).Scan(&newestSeed); err != nil {
		return false, fmt.Errorf("read latest projection seed hour: %w", err)
	}
	if newestSeed.Valid && !newestSeed.Time.UTC().Before(hourUTC(result.Hour)) {
		return false, fmt.Errorf(
			"projection seed hour %s is not after latest seed hour %s",
			hourUTC(result.Hour).Format(time.RFC3339), newestSeed.Time.UTC().Format(time.RFC3339),
		)
	}
	if err := upsertProjectionCheckpointsTx(ctx, tx, encoded); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_projection_seeded_archives (
			archive_sha256, export_hour, session_count, record_count
		) VALUES ($1, $2, $3, $4)`,
		result.ArchiveSHA256, hourUTC(result.Hour), result.Sessions, result.Records,
	); err != nil {
		return false, fmt.Errorf("record projection seed archive: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit projection seed: %w", err)
	}
	return false, nil
}

func forEachArchiveSession(path string, fn func(string, []*DeliveryRecord) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return err
	}
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	seenSessions := make(map[string]struct{})
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !strings.HasPrefix(header.Name, "delivery/") || !strings.HasSuffix(header.Name, ".jsonl") {
			continue
		}
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), int(defaultDecodedEnvelopeMaxBytes))
		var sessionID string
		var previous time.Time
		var records []*DeliveryRecord
		for scanner.Scan() {
			var record DeliveryRecord
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				return err
			}
			if sessionID == "" {
				sessionID = record.SessionID
			} else if record.SessionID != sessionID {
				return errors.New("projection seed entry contains multiple sessions")
			}
			if !previous.IsZero() && record.Timestamp.Before(previous) {
				return errors.New("projection seed session records are not time ordered")
			}
			previous = record.Timestamp.Time
			records = append(records, &record)
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		if sessionID == "" {
			return errors.New("projection seed delivery entry is empty")
		}
		if _, exists := seenSessions[sessionID]; exists {
			return errors.New("projection seed archive repeats a session entry")
		}
		seenSessions[sessionID] = struct{}{}
		if err := fn(sessionID, records); err != nil {
			return err
		}
	}
}
