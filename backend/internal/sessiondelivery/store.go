package sessiondelivery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "github.com/lib/pq"
)

const sessionProjectionExportAdvisoryLock int64 = 7304272051667003

var (
	ErrExportHourPurged    = errors.New("Session export hour has already been purged")
	ErrExportHourFrozen    = errors.New("Session export hour is frozen for export")
	ErrExportNotVerified   = errors.New("Session export batch is not verified by a durable archive backend")
	ErrArchiveHashMismatch = errors.New("Session archive checksum does not match the verified batch")
	ErrInvalidEnvelope     = errors.New("invalid Session envelope")
	ErrExportInProgress    = errors.New("Session export hour is already being processed")
	ErrExportAlreadyDone   = errors.New("Session export hour is already verified")
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

type HourStats struct {
	Records     int64 `json:"records"`
	Deliverable int64 `json:"deliverable"`
	Rejected    int64 `json:"rejected"`
	// ForeignSystemPromptExcluded is the subset of Rejected held back because the
	// system prompt still names tools the foreign-tool conversion removed, so the
	// record instructs the model to use a tool surface it no longer declares.
	ForeignSystemPromptExcluded int64 `json:"foreign_system_prompt_excluded"`
	// ForeignModelSelfClaimExcluded is the subset of Rejected held back because
	// assistant prose identified the model as something other than the one the
	// record is delivered under.
	ForeignModelSelfClaimExcluded int64 `json:"foreign_model_self_claim_excluded"`
	// UndeclaredResponseToolExcluded is the subset of Rejected held back because
	// the response called a tool while the request declared none.
	UndeclaredResponseToolExcluded int64 `json:"undeclared_response_tool_excluded"`
	// NormalizationFailed is the subset of Rejected held back because fidelity
	// normalization could not reshape the record at all.
	NormalizationFailed int64                `json:"normalization_failed"`
	TokenUsage          DeliveryTokenMetrics `json:"token_usage"`
}

type ExportBatch struct {
	Hour           time.Time            `json:"hour"`
	Status         string               `json:"status"`
	RecordCount    int64                `json:"record_count"`
	DeliveryCount  int64                `json:"delivery_count"`
	RejectedCount  int64                `json:"rejected_count"`
	ArchiveBackend string               `json:"archive_backend"`
	ArchiveObject  string               `json:"archive_object"`
	ArchiveSHA256  string               `json:"archive_sha256"`
	ArchiveSize    int64                `json:"archive_size"`
	TokenUsage     DeliveryTokenMetrics `json:"token_usage"`
	Manifest       json.RawMessage      `json:"manifest,omitempty"`
	ErrorMessage   string               `json:"error_message,omitempty"`
	StartedAt      time.Time            `json:"started_at"`
	ArchivedAt     *time.Time           `json:"archived_at,omitempty"`
	VerifiedAt     *time.Time           `json:"verified_at,omitempty"`
	PurgedAt       *time.Time           `json:"purged_at,omitempty"`
}

// StoreStatus is a payload-free operational snapshot of the isolated Session
// database. It is safe to expose to the admin observability API.
type StoreStatus struct {
	DatabaseSizeBytes      int64       `json:"database_size_bytes"`
	ConnectionsActive      int         `json:"connections_active"`
	ConnectionsTotal       int         `json:"connections_total"`
	ConnectionsMax         int         `json:"connections_max"`
	Partitions             int         `json:"partitions"`
	RecordsInDatabase      int64       `json:"records_in_database"`
	DeliverableInDatabase  int64       `json:"deliverable_in_database"`
	RejectedInDatabase     int64       `json:"rejected_in_database"`
	PayloadBytesInDatabase int64       `json:"payload_bytes_in_database"`
	CurrentHourRecords     int64       `json:"current_hour_records"`
	RecordsLast5Minutes    int64       `json:"records_last_5m"`
	FirstIngestedAt        *time.Time  `json:"first_ingested_at,omitempty"`
	LastIngestedAt         *time.Time  `json:"last_ingested_at,omitempty"`
	DatabaseTokenVolume    TokenVolume `json:"database_token_volume"`
	ArchiveFilesVerified   int64       `json:"archive_files_verified"`
	ArchiveBytesUploaded   int64       `json:"archive_bytes_uploaded"`
	RecordsArchived        int64       `json:"records_archived"`
	DeliveriesArchived     int64       `json:"deliveries_archived"`
	RejectedArchived       int64       `json:"rejected_archived"`
	FailedBatches          int64       `json:"failed_batches"`
	ExportingBatches       int64       `json:"exporting_batches"`
	LastVerifiedAt         *time.Time  `json:"last_verified_at,omitempty"`
	ArchiveTokenVolume     TokenVolume `json:"archive_token_volume"`
}

func OpenStore(ctx context.Context, dsn string) (*Store, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, errors.New("SESSION_DATABASE_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open Session database: %w", err)
	}
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping Session database: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("nil Session database")
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("nil Session database")
	}
	return s.db.PingContext(ctx)
}

// LockProjectionExport serializes exports across hours and processes. The
// projection checkpoint for hour N must be committed before hour N+1 reads it.
func (s *Store) LockProjectionExport(ctx context.Context) (func(), error) {
	if s == nil || s.db == nil {
		return nil, errors.New("nil Session database")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve Session projection export connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, sessionProjectionExportAdvisoryLock); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lock Session projection export: %w", err)
	}
	var once sync.Once
	unlock := func() {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, sessionProjectionExportAdvisoryLock)
			_ = conn.Close()
		})
	}
	return unlock, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	return ApplySessionMigrations(ctx, s.db)
}

func (s *Store) Insert(ctx context.Context, envelope *Envelope) (bool, error) {
	if err := validateEnvelopeForStorage(envelope); err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return false, fmt.Errorf("encode Session envelope: %w", err)
	}
	digest := sha256.Sum256(payload)
	payloadSHA := hex.EncodeToString(digest[:])
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return false, fmt.Errorf("create Session payload encoder: %w", err)
	}
	compressed := encoder.EncodeAll(payload, nil)
	encoder.Close()

	deliverable := envelope.Delivery != nil
	rejectionCode := ""
	if envelope.Rejection != nil {
		rejectionCode = envelope.Rejection.Code
	}
	var tokenMetrics DeliveryTokenMetrics
	tokensCounted := false
	if envelope.Delivery != nil {
		if extracted, extractErr := ExtractDeliveryTokenMetrics(envelope.Delivery); extractErr == nil {
			tokenMetrics = extracted
			tokensCounted = true
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin Session insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ingestedAt := s.now().UTC()
	hour := hourUTC(ingestedAt)
	var hourWritable bool
	if err := tx.QueryRowContext(ctx, `
		SELECT pg_try_advisory_xact_lock_shared(hashtext($1))`, hour.Format(time.RFC3339)).Scan(&hourWritable); err != nil {
		return false, fmt.Errorf("lock Session insert hour: %w", err)
	}
	if !hourWritable {
		return false, ErrExportHourFrozen
	}
	var duplicate bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM session_record_keys WHERE record_id = $1)`, envelope.RecordID).Scan(&duplicate); err != nil {
		return false, fmt.Errorf("check Session idempotency key: %w", err)
	}
	if duplicate {
		return false, nil
	}
	if err := ensureHourWritableTx(ctx, tx, hour); err != nil {
		return false, err
	}
	if err := ensurePartitionExec(ctx, tx, hour); err != nil {
		return false, err
	}

	var insertedID string
	err = tx.QueryRowContext(ctx, `
        INSERT INTO session_record_keys (record_id, occurred_at)
        VALUES ($1, $2)
        ON CONFLICT (record_id) DO NOTHING
        RETURNING record_id`, envelope.RecordID, envelope.OccurredAt).Scan(&insertedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert Session idempotency key: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
        INSERT INTO session_records (
            ingested_at, occurred_at, record_id, session_id, request_id,
            source_protocol, source_endpoint, api_key_id, user_id, group_id,
            http_status, duration_ms, deliverable, rejection_code,
			schema_version, payload_zstd, payload_sha256, captured_at,
			delivery_total_input_tokens, delivery_output_tokens, delivery_tokens_counted
        ) VALUES (
            $1, $2, $3, $4, $5,
            $6, $7, $8, $9, $10,
            $11, $12, $13, $14,
			$15, $16, $17, $18,
			$19, $20, $21
        )`,
		ingestedAt, envelope.OccurredAt, envelope.RecordID, envelope.SessionID, envelope.RequestID,
		envelope.Source.Protocol, envelope.Source.Endpoint,
		envelope.Source.Scope.APIKeyID, envelope.Source.Scope.UserID, envelope.Source.Scope.GroupID,
		envelope.HTTPStatus, envelope.DurationMS, deliverable, rejectionCode,
		envelope.SchemaVersion, compressed, payloadSHA, envelope.CapturedAt,
		tokenMetrics.TotalInputTokens, tokenMetrics.OutputTokens, tokensCounted,
	)
	if err != nil {
		return false, fmt.Errorf("insert Session record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit Session record: %w", err)
	}
	return true, nil
}

func (s *Store) EnsurePartition(ctx context.Context, ingestedAt time.Time) error {
	return ensurePartitionExec(ctx, s.db, hourUTC(ingestedAt))
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ensurePartitionExec(ctx context.Context, exec sqlExecer, hour time.Time) error {
	hour = hourUTC(hour)
	next := hour.Add(time.Hour)
	name := partitionName(hour)
	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF session_records FOR VALUES FROM ('%s') TO ('%s')`,
		quoteIdentifier(name), hour.Format(time.RFC3339), next.Format(time.RFC3339),
	)
	if _, err := exec.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("ensure Session partition %s: %w", name, err)
	}
	return nil
}

func (s *Store) ForEachHour(ctx context.Context, hour time.Time, fn func(*Envelope) error) error {
	if fn == nil {
		return errors.New("Session hour iterator callback is required")
	}
	start := hourUTC(hour)
	end := start.Add(time.Hour)
	partition, found, err := s.hourPartitionForRead(ctx, start)
	if err != nil {
		return fmt.Errorf("find Session hour partition: %w", err)
	}
	if !found {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
        SELECT payload_zstd, payload_sha256
		FROM %s
		WHERE ingested_at >= $1 AND ingested_at < $2
		ORDER BY session_id, occurred_at, request_id`, partition), start, end)
	if err != nil {
		return fmt.Errorf("query Session hour: %w", err)
	}
	defer rows.Close()
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return fmt.Errorf("create Session payload decoder: %w", err)
	}
	defer decoder.Close()
	for rows.Next() {
		var compressed []byte
		var expectedSHA string
		if err := rows.Scan(&compressed, &expectedSHA); err != nil {
			return fmt.Errorf("scan Session record: %w", err)
		}
		payload, err := decoder.DecodeAll(compressed, nil)
		if err != nil {
			return fmt.Errorf("decompress Session record: %w", err)
		}
		digest := sha256.Sum256(payload)
		if actual := hex.EncodeToString(digest[:]); actual != expectedSHA {
			return fmt.Errorf("Session payload checksum mismatch: expected=%s actual=%s", expectedSHA, actual)
		}
		var envelope Envelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return fmt.Errorf("decode stored Session envelope: %w", err)
		}
		if err := fn(&envelope); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate Session hour: %w", err)
	}
	return nil
}

// ForEachHourProjection streams only the fields needed by the delivery
// exporter. Stored envelopes can contain very large Original request/response
// audit payloads which are deliberately excluded from vendor archives. A
// streaming JSON decoder skips those fields without materializing a second
// in-memory copy, while the tee still verifies the complete uncompressed
// envelope checksum and decoded-size limit.
func (s *Store) ForEachHourProjection(
	ctx context.Context,
	hour time.Time,
	fn func(recordID string, delivery *DeliveryRecord, rejection *Rejection) error,
) error {
	if fn == nil {
		return errors.New("Session hour projection iterator callback is required")
	}
	start := hourUTC(hour)
	end := start.Add(time.Hour)
	partition, found, err := s.hourPartitionForRead(ctx, start)
	if err != nil {
		return fmt.Errorf("find Session projection hour partition: %w", err)
	}
	if !found {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT payload_zstd, payload_sha256
		FROM %s
		WHERE ingested_at >= $1 AND ingested_at < $2
		ORDER BY session_id, occurred_at, request_id`, partition), start, end)
	if err != nil {
		return fmt.Errorf("query Session projection hour: %w", err)
	}
	defer rows.Close()
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(uint64(defaultDecodedEnvelopeMaxBytes)))
	if err != nil {
		return fmt.Errorf("create Session projection payload decoder: %w", err)
	}
	defer decoder.Close()
	for rows.Next() {
		var compressed []byte
		var expectedSHA string
		if err := rows.Scan(&compressed, &expectedSHA); err != nil {
			return fmt.Errorf("scan Session projection record: %w", err)
		}
		record, err := decodeStoredProjectionEnvelope(
			decoder, compressed, expectedSHA, defaultDecodedEnvelopeMaxBytes,
		)
		if err != nil {
			return err
		}
		if err := fn(record.RecordID, record.Delivery, record.Rejection); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate Session projection hour: %w", err)
	}
	return nil
}

type storedProjectionEnvelope struct {
	RecordID  string          `json:"record_id"`
	Delivery  *DeliveryRecord `json:"delivery"`
	Rejection *Rejection      `json:"rejection"`
}

func (s *Store) StatsForHour(ctx context.Context, hour time.Time) (HourStats, error) {
	start := hourUTC(hour)
	end := start.Add(time.Hour)
	partition, found, err := s.hourPartitionForRead(ctx, start)
	if err != nil {
		return HourStats{}, fmt.Errorf("find Session statistics hour partition: %w", err)
	}
	if !found {
		return HourStats{}, nil
	}
	var stats HourStats
	err = s.db.QueryRowContext(ctx, fmt.Sprintf(`
        SELECT COUNT(*), COUNT(*) FILTER (WHERE deliverable), COUNT(*) FILTER (WHERE NOT deliverable)
		FROM %s
		WHERE ingested_at >= $1 AND ingested_at < $2`, partition), start, end).
		Scan(&stats.Records, &stats.Deliverable, &stats.Rejected)
	if err != nil {
		return HourStats{}, fmt.Errorf("count Session hour: %w", err)
	}
	return stats, nil
}

// Status returns aggregate metadata only. It never reads or decompresses the
// captured Session payloads.
func (s *Store) Status(ctx context.Context) (StoreStatus, error) {
	var status StoreStatus
	var databaseTotalInputTokens int64
	var databaseOutputTokens int64
	var databaseTokensCounted int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT pg_database_size(current_database()),
		       COUNT(*) FILTER (WHERE state = 'active'),
		       COUNT(*),
		       current_setting('max_connections')::integer
		FROM pg_stat_activity
		WHERE datname = current_database()`).Scan(
		&status.DatabaseSizeBytes,
		&status.ConnectionsActive,
		&status.ConnectionsTotal,
		&status.ConnectionsMax,
	); err != nil {
		return StoreStatus{}, fmt.Errorf("read Session database status: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pg_inherits
		WHERE inhparent = 'session_records'::regclass`).Scan(&status.Partitions); err != nil {
		return StoreStatus{}, fmt.Errorf("count Session partitions: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE deliverable),
		       COUNT(*) FILTER (WHERE NOT deliverable),
		       COALESCE(SUM(octet_length(payload_zstd)), 0),
		       COUNT(*) FILTER (WHERE ingested_at >= date_trunc('hour', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       COUNT(*) FILTER (WHERE ingested_at >= NOW() - INTERVAL '5 minutes'),
		       MIN(ingested_at),
		       MAX(ingested_at),
		       COALESCE(SUM(delivery_total_input_tokens) FILTER (
		           WHERE deliverable AND delivery_tokens_counted
		       ), 0),
		       COALESCE(SUM(delivery_output_tokens) FILTER (
		           WHERE deliverable AND delivery_tokens_counted
		       ), 0),
		       COUNT(*) FILTER (WHERE deliverable AND delivery_tokens_counted)
		FROM session_records`).Scan(
		&status.RecordsInDatabase,
		&status.DeliverableInDatabase,
		&status.RejectedInDatabase,
		&status.PayloadBytesInDatabase,
		&status.CurrentHourRecords,
		&status.RecordsLast5Minutes,
		&status.FirstIngestedAt,
		&status.LastIngestedAt,
		&databaseTotalInputTokens,
		&databaseOutputTokens,
		&databaseTokensCounted,
	); err != nil {
		return StoreStatus{}, fmt.Errorf("read Session record status: %w", err)
	}
	databaseTokenVolume, err := tokenVolumeFromPending(
		databaseTotalInputTokens,
		databaseOutputTokens,
		databaseTokensCounted,
		status.DeliverableInDatabase,
	)
	if err != nil {
		return StoreStatus{}, fmt.Errorf("build Session database token status: %w", err)
	}
	status.DatabaseTokenVolume = databaseTokenVolume
	var archiveTokenUsage DeliveryTokenMetrics
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ),
		       COALESCE(SUM(archive_size) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0),
		       COALESCE(SUM(record_count) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0),
		       COALESCE(SUM(delivery_count) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0),
		       COALESCE(SUM(rejected_count) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0),
		       COUNT(*) FILTER (WHERE status = 'failed'),
		       COUNT(*) FILTER (WHERE status = 'exporting'),
		       MAX(verified_at) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ),
		       COALESCE(SUM(delivery_input_tokens) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0),
		       COALESCE(SUM(delivery_cache_creation_input_tokens) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0),
		       COALESCE(SUM(delivery_cache_read_input_tokens) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0),
		       COALESCE(SUM(delivery_output_tokens) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0),
		       COALESCE(SUM(delivery_tokens_counted) FILTER (
		           WHERE archive_backend = $1 AND status IN ('verified', 'purged')
		       ), 0)
		FROM session_export_batches`, rcloneArchiveBackendName).Scan(
		&status.ArchiveFilesVerified,
		&status.ArchiveBytesUploaded,
		&status.RecordsArchived,
		&status.DeliveriesArchived,
		&status.RejectedArchived,
		&status.FailedBatches,
		&status.ExportingBatches,
		&status.LastVerifiedAt,
		&archiveTokenUsage.InputTokens,
		&archiveTokenUsage.CacheCreationInputTokens,
		&archiveTokenUsage.CacheReadInputTokens,
		&archiveTokenUsage.OutputTokens,
		&archiveTokenUsage.CountedDeliveries,
	); err != nil {
		return StoreStatus{}, fmt.Errorf("read Session archive status: %w", err)
	}
	archiveTokenUsage.TotalInputTokens, err = addTokenValues(
		archiveTokenUsage.InputTokens,
		archiveTokenUsage.CacheCreationInputTokens,
		archiveTokenUsage.CacheReadInputTokens,
	)
	if err != nil {
		return StoreStatus{}, fmt.Errorf("build Session archive input token status: %w", err)
	}
	archiveTokenUsage.TotalTokens, err = addTokenValues(archiveTokenUsage.TotalInputTokens, archiveTokenUsage.OutputTokens)
	if err != nil {
		return StoreStatus{}, fmt.Errorf("build Session archive total token status: %w", err)
	}
	archiveTokenVolume, err := tokenVolumeFromArchived(archiveTokenUsage, status.DeliveriesArchived)
	if err != nil {
		return StoreStatus{}, fmt.Errorf("build Session archive token status: %w", err)
	}
	status.ArchiveTokenVolume = archiveTokenVolume
	return status, nil
}

func (s *Store) RecentExportBatches(ctx context.Context, limit int) ([]ExportBatch, error) {
	if limit <= 0 {
		limit = 12
	}
	if limit > 48 {
		limit = 48
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT export_hour, status, record_count, delivery_count, rejected_count,
		       archive_backend, archive_size,
		       delivery_input_tokens, delivery_cache_creation_input_tokens,
		       delivery_cache_read_input_tokens, delivery_output_tokens,
		       delivery_tokens_counted,
		       started_at, archived_at, verified_at, purged_at
		FROM session_export_batches
		ORDER BY export_hour DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent Session export batches: %w", err)
	}
	defer rows.Close()
	batches := make([]ExportBatch, 0, limit)
	for rows.Next() {
		var batch ExportBatch
		if err := rows.Scan(
			&batch.Hour, &batch.Status, &batch.RecordCount, &batch.DeliveryCount, &batch.RejectedCount,
			&batch.ArchiveBackend, &batch.ArchiveSize,
			&batch.TokenUsage.InputTokens, &batch.TokenUsage.CacheCreationInputTokens,
			&batch.TokenUsage.CacheReadInputTokens, &batch.TokenUsage.OutputTokens,
			&batch.TokenUsage.CountedDeliveries,
			&batch.StartedAt, &batch.ArchivedAt, &batch.VerifiedAt, &batch.PurgedAt,
		); err != nil {
			return nil, fmt.Errorf("scan recent Session export batch: %w", err)
		}
		batch.TokenUsage.TotalInputTokens, err = addTokenValues(
			batch.TokenUsage.InputTokens,
			batch.TokenUsage.CacheCreationInputTokens,
			batch.TokenUsage.CacheReadInputTokens,
		)
		if err != nil {
			return nil, fmt.Errorf("aggregate recent Session export input tokens: %w", err)
		}
		batch.TokenUsage.TotalTokens, err = addTokenValues(batch.TokenUsage.TotalInputTokens, batch.TokenUsage.OutputTokens)
		if err != nil {
			return nil, fmt.Errorf("aggregate recent Session export total tokens: %w", err)
		}
		batches = append(batches, batch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent Session export batches: %w", err)
	}
	return batches, nil
}

func (s *Store) NextExportableHour(ctx context.Context, before time.Time, includeVerified bool) (time.Time, error) {
	before = hourUTC(before)
	query := `
		SELECT MIN(candidate_hour)
		FROM (
			SELECT date_trunc('hour', records.ingested_at) AS candidate_hour
			FROM session_records AS records
			WHERE records.ingested_at < $1
			  AND ($2 OR NOT EXISTS (
				SELECT 1 FROM session_export_batches AS batches
				WHERE batches.export_hour = date_trunc('hour', records.ingested_at)
				  AND batches.status IN ('verified', 'purged')
			  ))
			UNION ALL
			SELECT batches.export_hour AS candidate_hour
			FROM session_export_batches AS batches
			WHERE $2 AND batches.export_hour < $1 AND batches.status = 'verified'
		) AS candidates`
	var hour sql.NullTime
	if err := s.db.QueryRowContext(ctx, query, before, includeVerified).Scan(&hour); err != nil {
		return time.Time{}, fmt.Errorf("find next Session export hour: %w", err)
	}
	if !hour.Valid {
		return time.Time{}, sql.ErrNoRows
	}
	return hourUTC(hour.Time), nil
}

func (s *Store) StartExport(ctx context.Context, hour time.Time, attemptID string) error {
	hour = hourUTC(hour)
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		return errors.New("Session export attempt ID is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Session export freeze: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, hour.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("lock Session export hour: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO session_export_batches (export_hour, status, attempt_id)
		VALUES ($1, 'exporting', $2)
		ON CONFLICT (export_hour) DO UPDATE SET
			status = 'exporting', attempt_id = EXCLUDED.attempt_id,
			error_message = '', started_at = NOW(), updated_at = NOW()
		WHERE session_export_batches.status IN ('archived', 'failed')
		   OR (session_export_batches.status = 'exporting'
		       AND session_export_batches.updated_at < NOW() - INTERVAL '30 minutes')`, hour, attemptID)
	if err != nil {
		return fmt.Errorf("start Session export batch: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect Session export transition: %w", err)
	}
	if rows == 0 {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM session_export_batches WHERE export_hour = $1`, hour).Scan(&status); err != nil {
			return fmt.Errorf("read Session export state: %w", err)
		}
		if status == "purged" {
			return ErrExportHourPurged
		}
		if status == "verified" {
			return ErrExportAlreadyDone
		}
		if status == "exporting" {
			return ErrExportInProgress
		}
		return fmt.Errorf("cannot start Session export from state %q", status)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Session export freeze: %w", err)
	}
	return nil
}

func (s *Store) HeartbeatExport(ctx context.Context, hour time.Time, attemptID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE session_export_batches
		SET updated_at = NOW()
		WHERE export_hour = $1 AND status = 'exporting' AND attempt_id = $2`, hourUTC(hour), strings.TrimSpace(attemptID))
	if err != nil {
		return fmt.Errorf("heartbeat Session export: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("Session export attempt no longer owns the batch")
	}
	return nil
}

func (s *Store) MarkExportArchived(
	ctx context.Context,
	hour time.Time,
	attemptID string,
	stats HourStats,
	backend, object, archiveSHA string,
	archiveSize int64,
	manifest json.RawMessage,
	durable bool,
	checkpoints map[string]projectionCheckpoint,
) error {
	if err := stats.TokenUsage.Validate(); err != nil {
		return fmt.Errorf("validate Session export token metrics: %w", err)
	}
	if stats.TokenUsage.CountedDeliveries != stats.Deliverable {
		return errors.New("Session export token coverage does not match delivery count")
	}
	status := "archived"
	if durable {
		status = "verified"
	}
	encodedCheckpoints := make([]encodedProjectionCheckpoint, 0, len(checkpoints))
	if durable {
		sessionIDs := make([]string, 0, len(checkpoints))
		for sessionID := range checkpoints {
			sessionIDs = append(sessionIDs, sessionID)
		}
		sort.Strings(sessionIDs)
		for _, sessionID := range sessionIDs {
			encoded, err := encodeProjectionCheckpoint(sessionID, checkpoints[sessionID], hour)
			if err != nil {
				return fmt.Errorf("prepare projection checkpoint %s: %w", sessionID, err)
			}
			encodedCheckpoints = append(encodedCheckpoints, encoded)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Session export completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
        UPDATE session_export_batches SET
            status = $2,
			attempt_id = '',
			record_count = $3,
			delivery_count = $4,
			rejected_count = $5,
			archive_backend = $6,
			archive_object = $7,
			archive_sha256 = $8,
			archive_size = $9,
			delivery_input_tokens = $10,
			delivery_cache_creation_input_tokens = $11,
			delivery_cache_read_input_tokens = $12,
			delivery_output_tokens = $13,
			delivery_tokens_counted = $14,
			manifest = $15,
            archived_at = NOW(),
            verified_at = CASE WHEN $2 = 'verified' THEN NOW() ELSE NULL END,
            error_message = '',
            updated_at = NOW()
		WHERE export_hour = $1 AND status = 'exporting' AND attempt_id = $16`,
		hourUTC(hour), status, stats.Records, stats.Deliverable, stats.Rejected,
		backend, object, archiveSHA, archiveSize,
		stats.TokenUsage.InputTokens, stats.TokenUsage.CacheCreationInputTokens,
		stats.TokenUsage.CacheReadInputTokens, stats.TokenUsage.OutputTokens,
		stats.TokenUsage.CountedDeliveries,
		nullableJSON(manifest), strings.TrimSpace(attemptID),
	)
	if err != nil {
		return fmt.Errorf("mark Session export archived: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("Session export batch is not in exporting state")
	}
	if err := upsertProjectionCheckpointsTx(ctx, tx, encodedCheckpoints); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Session export completion: %w", err)
	}
	return nil
}

func upsertProjectionCheckpointsTx(ctx context.Context, tx *sql.Tx, checkpoints []encodedProjectionCheckpoint) error {
	for _, checkpoint := range checkpoints {
		result, err := tx.ExecContext(ctx, `
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
			WHERE session_projection_checkpoints.last_export_hour < EXCLUDED.last_export_hour`,
			checkpoint.SessionID, checkpoint.Version, checkpoint.Compressed,
			checkpoint.SHA256, checkpoint.LastExportHour,
		)
		if err != nil {
			return fmt.Errorf("store projection checkpoint %s: %w", checkpoint.SessionID, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect projection checkpoint %s: %w", checkpoint.SessionID, err)
		}
		if rows != 1 {
			return fmt.Errorf("projection checkpoint %s is not older than export hour", checkpoint.SessionID)
		}
	}
	return nil
}

func (s *Store) LoadProjectionCheckpoint(
	ctx context.Context,
	sessionID string,
	exportHour time.Time,
) (projectionCheckpoint, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return projectionCheckpoint{}, false, errors.New("projection checkpoint session ID is required")
	}
	var version int
	var compressed []byte
	var expectedSHA string
	var lastExportHour time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT checkpoint_version, checkpoint_zstd, checkpoint_sha256, last_export_hour
		FROM session_projection_checkpoints
		WHERE session_id = $1`, sessionID).Scan(&version, &compressed, &expectedSHA, &lastExportHour)
	if errors.Is(err, sql.ErrNoRows) {
		return projectionCheckpoint{}, false, nil
	}
	if err != nil {
		return projectionCheckpoint{}, false, fmt.Errorf("load projection checkpoint %s: %w", sessionID, err)
	}
	exportHour = hourUTC(exportHour)
	if !lastExportHour.UTC().Before(exportHour) {
		return projectionCheckpoint{}, false, fmt.Errorf(
			"projection checkpoint %s at %s is not before export hour %s",
			sessionID, lastExportHour.UTC().Format(time.RFC3339), exportHour.Format(time.RFC3339),
		)
	}
	checkpoint, err := decodeProjectionCheckpoint(compressed, expectedSHA, version)
	if err != nil {
		return projectionCheckpoint{}, false, fmt.Errorf("decode projection checkpoint %s: %w", sessionID, err)
	}
	return checkpoint, true, nil
}

func (s *Store) MarkExportFailed(ctx context.Context, hour time.Time, attemptID string, cause error) error {
	message := "unknown export failure"
	if cause != nil {
		message = sanitizeRejectionMessage(cause)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE session_export_batches
		SET status = 'failed', attempt_id = '', error_message = $3, updated_at = NOW()
		WHERE export_hour = $1 AND status = 'exporting' AND attempt_id = $2`,
		hourUTC(hour), strings.TrimSpace(attemptID), message)
	if err != nil {
		return fmt.Errorf("mark Session export failed: %w", err)
	}
	return nil
}

func (s *Store) GetExportBatch(ctx context.Context, hour time.Time) (*ExportBatch, error) {
	var batch ExportBatch
	var manifest []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT export_hour, status, record_count, delivery_count, rejected_count,
		       archive_backend, archive_object, archive_sha256, archive_size,
		       delivery_input_tokens, delivery_cache_creation_input_tokens,
		       delivery_cache_read_input_tokens, delivery_output_tokens,
		       delivery_tokens_counted,
		       manifest, error_message, started_at, archived_at, verified_at, purged_at
		FROM session_export_batches WHERE export_hour = $1`, hourUTC(hour)).Scan(
		&batch.Hour, &batch.Status, &batch.RecordCount, &batch.DeliveryCount, &batch.RejectedCount,
		&batch.ArchiveBackend, &batch.ArchiveObject, &batch.ArchiveSHA256, &batch.ArchiveSize,
		&batch.TokenUsage.InputTokens, &batch.TokenUsage.CacheCreationInputTokens,
		&batch.TokenUsage.CacheReadInputTokens, &batch.TokenUsage.OutputTokens,
		&batch.TokenUsage.CountedDeliveries,
		&manifest,
		&batch.ErrorMessage, &batch.StartedAt, &batch.ArchivedAt, &batch.VerifiedAt, &batch.PurgedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get Session export batch: %w", err)
	}
	batch.TokenUsage.TotalInputTokens, err = addTokenValues(
		batch.TokenUsage.InputTokens,
		batch.TokenUsage.CacheCreationInputTokens,
		batch.TokenUsage.CacheReadInputTokens,
	)
	if err != nil {
		return nil, fmt.Errorf("aggregate Session export batch input tokens: %w", err)
	}
	batch.TokenUsage.TotalTokens, err = addTokenValues(batch.TokenUsage.TotalInputTokens, batch.TokenUsage.OutputTokens)
	if err != nil {
		return nil, fmt.Errorf("aggregate Session export batch total tokens: %w", err)
	}
	batch.Manifest = manifest
	return &batch, nil
}

func (s *Store) PurgeHour(ctx context.Context, hour time.Time, expectedArchiveSHA string, allow bool) error {
	if !allow {
		return errors.New("Session purge requires an explicit allow-purge flag")
	}
	hour = hourUTC(hour)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Session purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, hour.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("lock Session purge hour: %w", err)
	}
	var status, archiveSHA string
	err = tx.QueryRowContext(ctx, `
        SELECT status, archive_sha256
        FROM session_export_batches
		WHERE export_hour = $1
		FOR UPDATE`, hour).Scan(&status, &archiveSHA)
	if err != nil {
		return fmt.Errorf("load Session purge batch: %w", err)
	}
	if status != "verified" {
		return fmt.Errorf("%w: status=%s", ErrExportNotVerified, status)
	}
	if expectedArchiveSHA == "" || archiveSHA != expectedArchiveSHA {
		return fmt.Errorf("%w: expected=%s verified=%s", ErrArchiveHashMismatch, expectedArchiveSHA, archiveSHA)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+quoteIdentifier(partitionName(hour))); err != nil {
		return fmt.Errorf("drop Session hour partition: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
        UPDATE session_export_batches
        SET status = 'purged', purged_at = NOW(), updated_at = NOW()
		WHERE export_hour = $1`, hour); err != nil {
		return fmt.Errorf("mark Session hour purged: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Session purge: %w", err)
	}
	return nil
}

func ensureHourWritableTx(ctx context.Context, tx *sql.Tx, hour time.Time) error {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM session_export_batches WHERE export_hour = $1`, hour).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check Session hour state: %w", err)
	}
	if status == "purged" {
		return ErrExportHourPurged
	}
	if status == "exporting" || status == "archived" || status == "verified" {
		return fmt.Errorf("%w: status=%s", ErrExportHourFrozen, status)
	}
	return nil
}

func validateEnvelopeForStorage(envelope *Envelope) error {
	if envelope == nil {
		return errors.New("nil Session envelope")
	}
	if envelope.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported Session envelope schema version %d", envelope.SchemaVersion)
	}
	if envelope.RecordID == "" || envelope.SessionID == "" || envelope.RequestID == "" {
		return errors.New("Session envelope IDs are required")
	}
	if envelope.OccurredAt.IsZero() || envelope.CapturedAt.IsZero() {
		return errors.New("Session envelope timestamps are required")
	}
	if envelope.Delivery != nil && envelope.Rejection != nil {
		return errors.New("Session envelope cannot be both deliverable and rejected")
	}
	if envelope.Delivery == nil && envelope.Rejection == nil {
		return errors.New("Session envelope must contain delivery or rejection")
	}
	if envelope.Delivery != nil {
		if err := ValidateDelivery(envelope.Delivery, DefaultPublicModel); err != nil {
			return fmt.Errorf("validate Session delivery before storage: %w", err)
		}
	}
	return nil
}

func hourUTC(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), utc.Hour(), 0, 0, 0, time.UTC)
}

func partitionName(hour time.Time) string {
	return "session_records_" + hourUTC(hour).Format("20060102_15")
}

// hourPartitionForRead resolves a single known ingest hour to its concrete
// partition. Long-running exporter callbacks must not scan session_records:
// PostgreSQL holds an AccessShareLock on that parent for the cursor lifetime,
// which blocks the next hour's CREATE TABLE ... PARTITION OF at the boundary.
//
// A missing partition represents an empty hour and retains the former parent
// query behavior without creating a partition from a read path. The returned
// identifier is derived solely from hourUTC and is quoted before interpolation.
func (s *Store) hourPartitionForRead(ctx context.Context, hour time.Time) (string, bool, error) {
	hour = hourUTC(hour)
	name := partitionName(hour)
	var found bool
	if err := s.db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&found); err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	return quoteIdentifier(name), true, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}
