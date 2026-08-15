package sessiondelivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const maxConversionRepairRecords = 10_000

type ConversionRepairOptions struct {
	Since  time.Time
	Before time.Time
	Limit  int
	Apply  bool
}

type ConversionRepairStats struct {
	DryRun         bool             `json:"dry_run"`
	Scanned        int64            `json:"scanned"`
	Repairable     int64            `json:"repairable"`
	Repaired       int64            `json:"repaired"`
	Stale          int64            `json:"stale"`
	Failed         int64            `json:"failed"`
	FailureReasons map[string]int64 `json:"failure_reasons,omitempty"`
}

// RepairRequestConversionRejections re-runs canonicalization for unarchived
// Responses records rejected by an older Session projection. Dry-run is the
// default. Apply updates are guarded by the same per-hour advisory lock used
// by ingest/export, and preserve the complete Original payload and all stable
// envelope identifiers.
func (s *Store) RepairRequestConversionRejections(
	ctx context.Context,
	canonicalizer *Canonicalizer,
	options ConversionRepairOptions,
) (ConversionRepairStats, error) {
	stats := ConversionRepairStats{DryRun: !options.Apply}
	if s == nil || s.db == nil {
		return stats, errors.New("nil Session database")
	}
	if canonicalizer == nil {
		return stats, errors.New("Session canonicalizer is required")
	}
	options.Since = options.Since.UTC()
	options.Before = options.Before.UTC()
	if options.Since.IsZero() || options.Before.IsZero() || !options.Since.Before(options.Before) {
		return stats, errors.New("conversion repair requires a valid since/before range")
	}
	if options.Limit <= 0 {
		options.Limit = 1000
	}
	if options.Limit > maxConversionRepairRecords {
		return stats, fmt.Errorf("conversion repair limit must not exceed %d", maxConversionRepairRecords)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT ingested_at, payload_zstd, payload_sha256
		FROM session_records
		WHERE ingested_at >= $1 AND ingested_at < $2
		  AND source_protocol = $3
		  AND rejection_code = 'request_conversion_failed'
		  AND NOT EXISTS (
			SELECT 1 FROM session_export_batches AS batches
			WHERE batches.export_hour = date_trunc('hour', session_records.ingested_at)
			  AND batches.status IN ('exporting', 'archived', 'verified', 'purged')
		  )
		ORDER BY ingested_at, record_id
		LIMIT $4`, options.Since, options.Before, ProtocolOpenAIResponses, options.Limit)
	if err != nil {
		return stats, fmt.Errorf("query Session conversion rejections: %w", err)
	}
	defer rows.Close()
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(uint64(defaultDecodedEnvelopeMaxBytes)))
	if err != nil {
		return stats, fmt.Errorf("create Session repair decoder: %w", err)
	}
	defer decoder.Close()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return stats, fmt.Errorf("create Session repair encoder: %w", err)
	}
	defer encoder.Close()

	for rows.Next() {
		var ingestedAt time.Time
		var compressed []byte
		var expectedSHA string
		if err := rows.Scan(&ingestedAt, &compressed, &expectedSHA); err != nil {
			return stats, fmt.Errorf("scan Session conversion rejection: %w", err)
		}
		stats.Scanned++
		envelope, err := decodeConversionRepairEnvelope(decoder, compressed, expectedSHA)
		if err != nil {
			recordConversionRepairFailure(&stats, err)
			continue
		}
		repaired, err := recanonicalizeRejectedEnvelope(canonicalizer, envelope)
		if err != nil {
			recordConversionRepairFailure(&stats, err)
			continue
		}
		stats.Repairable++
		if !options.Apply {
			continue
		}
		payload, err := json.Marshal(repaired)
		if err != nil {
			recordConversionRepairFailure(&stats, fmt.Errorf("encode repaired Session envelope: %w", err))
			continue
		}
		digest := sha256.Sum256(payload)
		var tokenMetrics DeliveryTokenMetrics
		tokensCounted := false
		if extracted, extractErr := ExtractDeliveryTokenMetrics(repaired.Delivery); extractErr == nil {
			tokenMetrics = extracted
			tokensCounted = true
		}
		updated, err := s.applyConversionRepair(
			ctx,
			ingestedAt,
			repaired.RecordID,
			expectedSHA,
			encoder.EncodeAll(payload, nil),
			hex.EncodeToString(digest[:]),
			repaired.SchemaVersion,
			tokenMetrics.TotalInputTokens,
			tokenMetrics.OutputTokens,
			tokensCounted,
		)
		if err != nil {
			recordConversionRepairFailure(&stats, err)
			continue
		}
		if !updated {
			stats.Stale++
			continue
		}
		stats.Repaired++
	}
	if err := rows.Err(); err != nil {
		return stats, fmt.Errorf("iterate Session conversion rejections: %w", err)
	}
	return stats, nil
}

func decodeConversionRepairEnvelope(decoder *zstd.Decoder, compressed []byte, expectedSHA string) (*Envelope, error) {
	payload, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("decompress Session repair candidate: %w", err)
	}
	digest := sha256.Sum256(payload)
	if actual := hex.EncodeToString(digest[:]); actual != expectedSHA {
		return nil, fmt.Errorf("Session repair candidate checksum mismatch: expected=%s actual=%s", expectedSHA, actual)
	}
	var envelope Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode Session repair candidate: %w", err)
	}
	return &envelope, nil
}

func recanonicalizeRejectedEnvelope(canonicalizer *Canonicalizer, envelope *Envelope) (*Envelope, error) {
	if envelope == nil || envelope.Rejection == nil || envelope.Rejection.Code != "request_conversion_failed" {
		return nil, errors.New("Session repair candidate is not a request conversion rejection")
	}
	if envelope.Source.Protocol != ProtocolOpenAIResponses {
		return nil, errors.New("Session repair candidate is not a Responses record")
	}
	rebuilt, err := canonicalizer.Build(CaptureInput{
		Protocol:         envelope.Source.Protocol,
		Endpoint:         envelope.Source.Endpoint,
		Scope:            envelope.Source.Scope,
		GatewayRequestID: envelope.GatewayRequestID,
		StartedAt:        envelope.OccurredAt,
		CompletedAt:      envelope.OccurredAt.Add(time.Duration(envelope.DurationMS) * time.Millisecond),
		HTTPStatus:       envelope.HTTPStatus,
		RequestBody:      envelope.Original.Request,
		ResponseBody:     envelope.Original.Response,
	})
	if err != nil {
		return nil, fmt.Errorf("recanonicalize Session rejection: %w", err)
	}
	if rebuilt.Rejection != nil || rebuilt.Delivery == nil {
		if rebuilt.Rejection == nil {
			return nil, errors.New("recanonicalized Session rejection has no delivery")
		}
		return nil, fmt.Errorf("recanonicalized Session rejection remains %s: %s", rebuilt.Rejection.Code, rebuilt.Rejection.Message)
	}

	// Preserve every stable/audit field from the ingested envelope. Only the
	// delivery projection and rejection state are replaced.
	rebuilt.SchemaVersion = envelope.SchemaVersion
	rebuilt.RecordID = envelope.RecordID
	rebuilt.SessionID = envelope.SessionID
	rebuilt.RequestID = envelope.RequestID
	rebuilt.OccurredAt = envelope.OccurredAt
	rebuilt.CapturedAt = envelope.CapturedAt
	rebuilt.GatewayRequestID = envelope.GatewayRequestID
	rebuilt.Source = envelope.Source
	rebuilt.HTTPStatus = envelope.HTTPStatus
	rebuilt.DurationMS = envelope.DurationMS
	rebuilt.Original = envelope.Original
	rebuilt.Rejection = nil
	rebuilt.Delivery.SessionID = envelope.SessionID
	rebuilt.Delivery.RequestID = envelope.RequestID
	rebuilt.Delivery.Timestamp = DeliveryTime{envelope.OccurredAt}
	rebuilt.Delivery.Metadata.HTTPStatus = envelope.HTTPStatus
	rebuilt.Delivery.Metadata.LatencyMS = envelope.DurationMS
	rebuilt.Delivery.Response.StatusCode = envelope.HTTPStatus
	if err := validateEnvelopeForStorage(rebuilt); err != nil {
		return nil, fmt.Errorf("validate repaired Session envelope: %w", err)
	}
	return rebuilt, nil
}

func (s *Store) applyConversionRepair(
	ctx context.Context,
	ingestedAt time.Time,
	recordID, expectedSHA string,
	compressed []byte,
	payloadSHA string,
	schemaVersion int,
	totalInputTokens int64,
	outputTokens int64,
	tokensCounted bool,
) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin Session conversion repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	hour := hourUTC(ingestedAt)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext($1))`, hour.Format(time.RFC3339)); err != nil {
		return false, fmt.Errorf("lock Session conversion repair hour: %w", err)
	}
	if err := ensureHourWritableTx(ctx, tx, hour); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE session_records
		SET deliverable = TRUE,
		    rejection_code = '',
		    schema_version = $1,
		    payload_zstd = $2,
		    payload_sha256 = $3,
		    delivery_total_input_tokens = $4,
		    delivery_output_tokens = $5,
		    delivery_tokens_counted = $6
		WHERE ingested_at = $7
		  AND record_id = $8
		  AND payload_sha256 = $9
		  AND rejection_code = 'request_conversion_failed'`,
		schemaVersion, compressed, payloadSHA,
		totalInputTokens, outputTokens, tokensCounted,
		ingestedAt, recordID, expectedSHA)
	if err != nil {
		return false, fmt.Errorf("update Session conversion repair: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect Session conversion repair: %w", err)
	}
	if rows == 0 {
		return false, nil
	}
	if rows != 1 {
		return false, fmt.Errorf("Session conversion repair updated %d rows", rows)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit Session conversion repair: %w", err)
	}
	return true, nil
}

func recordConversionRepairFailure(stats *ConversionRepairStats, err error) {
	stats.Failed++
	if stats.FailureReasons == nil {
		stats.FailureReasons = make(map[string]int64)
	}
	reason := strings.TrimSpace(sanitizeRejectionMessage(err))
	if reason == "" {
		reason = "unknown conversion repair failure"
	}
	stats.FailureReasons[reason]++
}
