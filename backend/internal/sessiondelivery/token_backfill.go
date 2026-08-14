package sessiondelivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const maxTokenBackfillRecords = 100_000

// ArchiveTokenBackfillResult reports a full archive validation pass. Apply is
// intentionally opt-in; dry-run still verifies every JSONL record, manifest,
// file hash, and matching durable batch before reporting eligibility.
type ArchiveTokenBackfillResult struct {
	DryRun                bool                 `json:"dry_run"`
	ArchivesScanned       int64                `json:"archives_scanned"`
	BatchesEligible       int64                `json:"batches_eligible"`
	BatchesAlreadyCounted int64                `json:"batches_already_counted"`
	BatchesUpdated        int64                `json:"batches_updated"`
	TokenUsage            DeliveryTokenMetrics `json:"token_usage"`
}

// RecordTokenBackfillResult reports the bounded pending-record pass. The
// operation is resumable because each update is guarded by the immutable
// record key and delivery_tokens_counted=false.
type RecordTokenBackfillResult struct {
	DryRun       bool                 `json:"dry_run"`
	Limit        int                  `json:"limit"`
	LimitReached bool                 `json:"limit_reached"`
	Scanned      int64                `json:"scanned"`
	Eligible     int64                `json:"eligible"`
	Updated      int64                `json:"updated"`
	Stale        int64                `json:"stale"`
	TokenUsage   DeliveryTokenMetrics `json:"token_usage"`
}

func (s *Store) BackfillArchiveTokenMetrics(
	ctx context.Context,
	inputDir string,
	publicModel string,
	apply bool,
) (ArchiveTokenBackfillResult, error) {
	result := ArchiveTokenBackfillResult{DryRun: !apply}
	if s == nil || s.db == nil {
		return result, errors.New("nil Session database")
	}
	publicModel = strings.TrimSpace(publicModel)
	if publicModel == "" {
		publicModel = DefaultPublicModel
	}
	inputDir = strings.TrimSpace(inputDir)
	if inputDir == "" {
		return result, errors.New("archive token backfill input directory is required")
	}
	inputs, err := inventoryRebuildInputs(ctx, inputDir, publicModel)
	if err != nil {
		return result, err
	}
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.ArchivesScanned++
		batch, err := s.GetExportBatch(ctx, input.hour)
		if err != nil {
			return result, fmt.Errorf("load Session token backfill batch %s: %w", input.hour.Format(time.RFC3339), err)
		}
		if batch.Status != "verified" && batch.Status != "purged" {
			return result, fmt.Errorf("Session token backfill batch %s is not verified or purged", input.hour.Format(time.RFC3339))
		}
		if batch.ArchiveSHA256 != input.sha256 || batch.ArchiveSize != input.size {
			return result, fmt.Errorf("Session token backfill archive %s does not match the verified batch object", input.hour.Format(time.RFC3339))
		}
		metrics := input.validation.TokenUsage
		if err := metrics.Validate(); err != nil {
			return result, fmt.Errorf("validate Session token backfill metrics %s: %w", input.hour.Format(time.RFC3339), err)
		}
		if metrics.CountedDeliveries != batch.DeliveryCount || input.validation.Manifest.DeliveryCount != batch.DeliveryCount {
			return result, fmt.Errorf("Session token backfill delivery count mismatch for %s", input.hour.Format(time.RFC3339))
		}
		if err := result.TokenUsage.Add(metrics); err != nil {
			return result, fmt.Errorf("aggregate Session archive token backfill metrics: %w", err)
		}
		if batch.DeliveryCount == 0 || batch.TokenUsage.CountedDeliveries == batch.DeliveryCount {
			if batch.TokenUsage != metrics {
				return result, fmt.Errorf("Session token backfill batch %s already has different metrics", input.hour.Format(time.RFC3339))
			}
			result.BatchesAlreadyCounted++
			continue
		}
		if batch.TokenUsage.CountedDeliveries != 0 {
			return result, fmt.Errorf("Session token backfill batch %s has partial existing metrics", input.hour.Format(time.RFC3339))
		}
		result.BatchesEligible++
		if !apply {
			continue
		}
		updated, err := s.applyArchiveTokenMetrics(ctx, input.hour, input.sha256, metrics)
		if err != nil {
			return result, err
		}
		if !updated {
			return result, fmt.Errorf("Session token backfill batch %s changed during apply", input.hour.Format(time.RFC3339))
		}
		result.BatchesUpdated++
	}
	return result, nil
}

func (s *Store) applyArchiveTokenMetrics(
	ctx context.Context,
	hour time.Time,
	archiveSHA string,
	metrics DeliveryTokenMetrics,
) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE session_export_batches
		SET delivery_input_tokens = $3,
		    delivery_cache_creation_input_tokens = $4,
		    delivery_cache_read_input_tokens = $5,
		    delivery_output_tokens = $6,
		    delivery_tokens_counted = $7,
		    updated_at = NOW()
		WHERE export_hour = $1
		  AND archive_sha256 = $2
		  AND status IN ('verified', 'purged')
		  AND delivery_tokens_counted = 0`,
		hourUTC(hour), archiveSHA,
		metrics.InputTokens, metrics.CacheCreationInputTokens,
		metrics.CacheReadInputTokens, metrics.OutputTokens,
		metrics.CountedDeliveries,
	)
	if err != nil {
		return false, fmt.Errorf("apply Session archive token metrics: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect Session archive token metric update: %w", err)
	}
	return rows == 1, nil
}

func (s *Store) BackfillPendingRecordTokenMetrics(
	ctx context.Context,
	limit int,
	apply bool,
) (RecordTokenBackfillResult, error) {
	result := RecordTokenBackfillResult{DryRun: !apply, Limit: limit}
	if s == nil || s.db == nil {
		return result, errors.New("nil Session database")
	}
	if limit <= 0 {
		limit = 1000
	}
	if limit > maxTokenBackfillRecords {
		return result, fmt.Errorf("record token backfill limit must not exceed %d", maxTokenBackfillRecords)
	}
	result.Limit = limit
	rows, err := s.db.QueryContext(ctx, `
		SELECT records.ingested_at, records.record_id,
		       records.payload_zstd, records.payload_sha256
		FROM session_records AS records
		WHERE records.deliverable
		  AND NOT records.delivery_tokens_counted
		  AND NOT EXISTS (
		      SELECT 1
		      FROM session_export_batches AS batches
		      WHERE batches.export_hour = date_trunc('hour', records.ingested_at)
		        AND batches.status IN ('exporting', 'archived', 'verified', 'purged')
		  )
		ORDER BY records.ingested_at, records.record_id
		LIMIT $1`, limit)
	if err != nil {
		return result, fmt.Errorf("query pending Session token backfill records: %w", err)
	}
	defer rows.Close()
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(uint64(defaultDecodedEnvelopeMaxBytes)))
	if err != nil {
		return result, fmt.Errorf("create Session token backfill decoder: %w", err)
	}
	defer decoder.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var ingestedAt time.Time
		var recordID string
		var compressed []byte
		var expectedSHA string
		if err := rows.Scan(&ingestedAt, &recordID, &compressed, &expectedSHA); err != nil {
			return result, fmt.Errorf("scan pending Session token backfill record: %w", err)
		}
		result.Scanned++
		envelope, err := decodeStoredProjectionEnvelope(decoder, compressed, expectedSHA, defaultDecodedEnvelopeMaxBytes)
		if err != nil {
			return result, fmt.Errorf("decode Session token backfill record %s: %w", recordID, err)
		}
		if envelope.RecordID != recordID || envelope.Delivery == nil {
			return result, fmt.Errorf("Session token backfill record %s payload identity is inconsistent", recordID)
		}
		metrics, err := ExtractDeliveryTokenMetrics(envelope.Delivery)
		if err != nil {
			return result, fmt.Errorf("extract Session token backfill record %s: %w", recordID, err)
		}
		if err := result.TokenUsage.Add(metrics); err != nil {
			return result, fmt.Errorf("aggregate pending Session token backfill metrics: %w", err)
		}
		result.Eligible++
		if !apply {
			continue
		}
		updated, err := s.applyPendingRecordTokenMetrics(ctx, ingestedAt, recordID, metrics)
		if err != nil {
			return result, err
		}
		if updated {
			result.Updated++
		} else {
			result.Stale++
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate pending Session token backfill records: %w", err)
	}
	result.LimitReached = result.Scanned == int64(limit)
	return result, nil
}

func (s *Store) applyPendingRecordTokenMetrics(
	ctx context.Context,
	ingestedAt time.Time,
	recordID string,
	metrics DeliveryTokenMetrics,
) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE session_records AS records
		SET delivery_total_input_tokens = $3,
		    delivery_output_tokens = $4,
		    delivery_tokens_counted = TRUE
		WHERE records.ingested_at = $1
		  AND records.record_id = $2
		  AND records.deliverable
		  AND NOT records.delivery_tokens_counted
		  AND NOT EXISTS (
		      SELECT 1
		      FROM session_export_batches AS batches
		      WHERE batches.export_hour = date_trunc('hour', records.ingested_at)
		        AND batches.status IN ('exporting', 'archived', 'verified', 'purged')
		  )`,
		ingestedAt, recordID, metrics.TotalInputTokens, metrics.OutputTokens,
	)
	if err != nil {
		return false, fmt.Errorf("apply pending Session token metrics for %s: %w", recordID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect pending Session token metric update for %s: %w", recordID, err)
	}
	return rows == 1, nil
}
