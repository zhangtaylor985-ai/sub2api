//go:build integration

package sessiondelivery

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/thinkingsig"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const sessionPostgresImage = "postgres:18.1-alpine3.23"

func TestStoreExportVerifyAndPurgeLifecycle(t *testing.T) {
	ctx := context.Background()
	store := startSessionDeliveryPostgres(t, ctx)
	require.NoError(t, store.Migrate(ctx))
	require.NoError(t, store.Migrate(ctx), "Session migrations must be idempotent")

	hour := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return hour.Add(30 * time.Minute) }
	canonicalizer := newTestCanonicalizer(t)
	envelopes := []*Envelope{
		buildIntegrationAnthropicEnvelope(t, canonicalizer, hour.Add(5*time.Minute), "request-1", 200),
		buildIntegrationResponsesEnvelope(t, canonicalizer, hour.Add(10*time.Minute), "request-2"),
		buildIntegrationAnthropicEnvelope(t, canonicalizer, hour.Add(15*time.Minute), "request-3", 500),
	}
	spool, err := NewSpool(filepath.Join(t.TempDir(), "gateway-spool"), 8<<20)
	require.NoError(t, err)
	_, err = spool.Write(envelopes[0])
	require.NoError(t, err)
	ingestHandler, err := NewIngestHandler(store, IngestHandlerConfig{
		Secret:  testHMACSecret,
		TempDir: filepath.Join(t.TempDir(), "ingest-tmp"),
	})
	require.NoError(t, err)
	ingestServer := httptest.NewServer(ingestHandler)
	t.Cleanup(ingestServer.Close)
	forwarder, err := NewForwarder(spool, ForwarderConfig{
		Endpoint: ingestServer.URL + "/v1/records",
		Secret:   testHMACSecret,
	})
	require.NoError(t, err)
	forwardStats, err := forwarder.ForwardOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, ForwardStats{Attempted: 1, Inserted: 1, Pending: 0}, forwardStats)
	_, err = spool.Write(envelopes[0])
	require.NoError(t, err)
	forwardStats, err = forwarder.ForwardOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, ForwardStats{Attempted: 1, Duplicates: 1, Pending: 0}, forwardStats)
	invalidEnvelope := *envelopes[0]
	invalidEnvelope.RecordID = "rec_invalid_schema"
	invalidEnvelope.SchemaVersion = SchemaVersion + 1
	_, err = spool.Write(&invalidEnvelope)
	require.NoError(t, err)
	forwardStats, err = forwarder.ForwardOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, ForwardStats{Attempted: 1, Quarantined: 1, Pending: 0}, forwardStats)

	for _, envelope := range envelopes[1:] {
		inserted, err := store.Insert(ctx, envelope)
		require.NoError(t, err)
		require.True(t, inserted)
	}
	inserted, err := store.Insert(ctx, envelopes[0])
	require.NoError(t, err)
	require.False(t, inserted, "duplicate spool delivery must be idempotent")

	lockTx, err := store.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = lockTx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, hour.Format(time.RFC3339))
	require.NoError(t, err)
	blockedEnvelope := buildIntegrationAnthropicEnvelope(t, canonicalizer, hour.Add(20*time.Minute), "request-blocked", 200)
	inserted, err = store.Insert(ctx, blockedEnvelope)
	require.ErrorIs(t, err, ErrExportHourFrozen)
	require.False(t, inserted)
	require.NoError(t, lockTx.Rollback())
	inserted, err = store.Insert(ctx, blockedEnvelope)
	require.NoError(t, err)
	require.True(t, inserted)

	stats, err := store.StatsForHour(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, HourStats{Records: 4, Deliverable: 3, Rejected: 1}, stats)
	nextHour, err := store.NextExportableHour(ctx, hour.Add(time.Hour), false)
	require.NoError(t, err)
	require.Equal(t, hour, nextHour)
	require.NoError(t, store.StartExport(ctx, hour, "attempt-owner"))
	require.ErrorIs(t, store.StartExport(ctx, hour, "attempt-concurrent"), ErrExportInProgress)
	require.NoError(t, store.HeartbeatExport(ctx, hour, "attempt-owner"))
	require.NoError(t, store.MarkExportFailed(ctx, hour, "attempt-owner", errors.New("test retry")))

	localBackend, err := NewLocalArchiveBackend(filepath.Join(t.TempDir(), "local-archive"))
	require.NoError(t, err)
	localExporter, err := NewExporter(store, localBackend, ExporterConfig{
		PublicModel: DefaultPublicModel,
		TempDir:     filepath.Join(t.TempDir(), "local-export-tmp"),
	})
	require.NoError(t, err)
	localResult, err := localExporter.ExportHour(ctx, hour)
	require.NoError(t, err)
	require.False(t, localResult.Durable)
	require.Contains(t, filepath.Base(localResult.Archive.Name), localResult.Archive.SHA256[:16])
	require.Equal(t, int64(3), localResult.Manifest.RecordCount)
	require.Equal(t, int64(3), localResult.Manifest.DeliveryCount)
	require.Equal(t, int64(1), localResult.Manifest.ExcludedCount)
	requireArchiveHasOnlyBlackBoxDelivery(t, localResult.Archive.Name)
	batch, err := store.GetExportBatch(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, "archived", batch.Status)
	err = store.PurgeHour(ctx, hour, localResult.Archive.SHA256, true)
	require.ErrorIs(t, err, ErrExportNotVerified)

	durableLocal, err := NewLocalArchiveBackend(filepath.Join(t.TempDir(), "durable-drive-fixture"))
	require.NoError(t, err)
	durableBackend := &integrationDurableArchive{LocalArchiveBackend: durableLocal}
	durableExporter, err := NewExporter(store, durableBackend, ExporterConfig{
		PublicModel: DefaultPublicModel,
		TempDir:     filepath.Join(t.TempDir(), "durable-export-tmp"),
	})
	require.NoError(t, err)
	durableResult, err := durableExporter.ExportHour(ctx, hour)
	require.NoError(t, err)
	require.True(t, durableResult.Durable)
	batch, err = store.GetExportBatch(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, "verified", batch.Status)
	require.NotNil(t, batch.VerifiedAt)
	storeStatus, err := store.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), storeStatus.ArchiveFilesVerified)
	require.Equal(t, durableResult.Archive.Size, storeStatus.ArchiveBytesUploaded)
	require.Equal(t, int64(4), storeStatus.RecordsArchived)
	require.Equal(t, int64(3), storeStatus.DeliveriesArchived)
	require.Equal(t, int64(1), storeStatus.RejectedArchived)
	require.NotNil(t, storeStatus.LastVerifiedAt)
	_, err = store.NextExportableHour(ctx, hour.Add(time.Hour), false)
	require.ErrorIs(t, err, sql.ErrNoRows)
	nextHour, err = store.NextExportableHour(ctx, hour.Add(time.Hour), true)
	require.NoError(t, err)
	require.Equal(t, hour, nextHour)

	err = store.PurgeHour(ctx, hour, strings.Repeat("0", 64), true)
	require.ErrorIs(t, err, ErrArchiveHashMismatch)
	err = store.PurgeHour(ctx, hour, durableResult.Archive.SHA256, false)
	require.ErrorContains(t, err, "explicit allow-purge")
	require.NoError(t, store.PurgeHour(ctx, hour, durableResult.Archive.SHA256, true))

	stats, err = store.StatsForHour(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, HourStats{}, stats)
	batch, err = store.GetExportBatch(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, "purged", batch.Status)

	store.now = func() time.Time { return hour.Add(time.Hour).Add(5 * time.Minute) }
	inserted, err = store.Insert(ctx, envelopes[0])
	require.NoError(t, err)
	require.False(t, inserted, "purged duplicate remains idempotent")
	lateEnvelope := buildIntegrationAnthropicEnvelope(t, canonicalizer, hour.Add(time.Hour), "request-late", 200)
	inserted, err = store.Insert(ctx, lateEnvelope)
	require.NoError(t, err)
	require.True(t, inserted, "late arrivals must roll into the current ingest-hour partition")
	lateStats, err := store.StatsForHour(ctx, hour.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, HourStats{Records: 1, Deliverable: 1}, lateStats)
}

func TestHourlyExportPreservesProjectionStateAfterPurge(t *testing.T) {
	ctx := context.Background()
	store := startSessionDeliveryPostgres(t, ctx)
	require.NoError(t, store.Migrate(ctx))

	firstHour := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	secondHour := firstHour.Add(time.Hour)
	first := buildCrossHourEnvelope(t, firstHour.Add(58*time.Minute), 1, 100)
	second := buildCrossHourEnvelope(t, secondHour.Add(time.Minute), 2, 130)
	firstSignature := responseThinkingSignature(t, first.Delivery)

	store.now = func() time.Time { return firstHour.Add(10 * time.Minute) }
	inserted, err := store.Insert(ctx, first)
	require.NoError(t, err)
	require.True(t, inserted)

	archiveBackend, err := NewLocalArchiveBackend(filepath.Join(t.TempDir(), "durable-archive"))
	require.NoError(t, err)
	exporter, err := NewExporter(store, &integrationDurableArchive{LocalArchiveBackend: archiveBackend}, ExporterConfig{
		PublicModel: DefaultPublicModel,
		TempDir:     filepath.Join(t.TempDir(), "export-tmp"),
	})
	require.NoError(t, err)

	firstResult, err := exporter.ExportHour(ctx, firstHour)
	require.NoError(t, err)
	require.True(t, firstResult.Durable)
	require.NoError(t, store.PurgeHour(ctx, firstHour, firstResult.Archive.SHA256, true))

	// Simulate an upgrade from an exporter that predates durable projection
	// checkpoints, then rebuild from the already verified archive.
	_, err = store.db.ExecContext(ctx, `DELETE FROM session_projection_checkpoints`)
	require.NoError(t, err)
	_, err = store.SeedProjectionArchive(ctx, firstResult.Archive.Name, DefaultPublicModel, false)
	require.ErrorContains(t, err, "allow-seed")
	seed, err := store.SeedProjectionArchive(ctx, firstResult.Archive.Name, DefaultPublicModel, true)
	require.NoError(t, err)
	require.False(t, seed.AlreadySeeded)
	require.Equal(t, firstHour, seed.Hour)
	require.Equal(t, int64(1), seed.Sessions)
	require.Equal(t, int64(1), seed.Records)
	repeatedSeed, err := store.SeedProjectionArchive(ctx, firstResult.Archive.Name, DefaultPublicModel, true)
	require.NoError(t, err)
	require.True(t, repeatedSeed.AlreadySeeded)

	checkpoint, found, err := store.LoadProjectionCheckpoint(ctx, first.Delivery.SessionID, secondHour)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, checkpoint.Echo, 1)
	require.Equal(t, 98, checkpoint.Usage.PreviousPrefix)

	store.now = func() time.Time { return secondHour.Add(10 * time.Minute) }
	inserted, err = store.Insert(ctx, second)
	require.NoError(t, err)
	require.True(t, inserted)

	secondResult, err := exporter.ExportHour(ctx, secondHour)
	require.NoError(t, err)
	require.True(t, secondResult.Durable)
	require.NoError(t, store.PurgeHour(ctx, secondHour, secondResult.Archive.SHA256, true))

	validation, err := ValidateArchive(secondResult.Archive.Name, DefaultPublicModel)
	require.NoError(t, err)
	require.Equal(t, int64(1), validation.Manifest.DeliveryCount)

	records := readDeliveryRecordsFromArchive(t, secondResult.Archive.Name)
	require.Len(t, records, 1)
	require.Equal(t, firstSignature, requestAssistantThinkingSignature(t, &records[0]))
	input, creation, read, output := usageNumbers(t, &records[0])
	require.Equal(t, 2, input)
	require.Equal(t, 30, creation)
	require.Equal(t, 98, read)
	require.Equal(t, 10, output)
}

func TestProjectionReseedArchivesDryRunAndApply(t *testing.T) {
	ctx := context.Background()
	store := startSessionDeliveryPostgres(t, ctx)
	require.NoError(t, store.Migrate(ctx))

	firstHour := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	secondHour := firstHour.Add(time.Hour)
	thirdHour := secondHour.Add(time.Hour)
	first := buildCrossHourEnvelope(t, firstHour.Add(58*time.Minute), 1, 100)
	second := buildCrossHourEnvelope(t, secondHour.Add(time.Minute), 2, 130)

	archiveDir := filepath.Join(t.TempDir(), "projection-reseed-archives")
	archiveBackend, err := NewLocalArchiveBackend(archiveDir)
	require.NoError(t, err)
	exporter, err := NewExporter(store, &integrationDurableArchive{LocalArchiveBackend: archiveBackend}, ExporterConfig{
		PublicModel: DefaultPublicModel,
		TempDir:     filepath.Join(t.TempDir(), "projection-reseed-export-tmp"),
	})
	require.NoError(t, err)

	store.now = func() time.Time { return firstHour.Add(10 * time.Minute) }
	inserted, err := store.Insert(ctx, first)
	require.NoError(t, err)
	require.True(t, inserted)
	firstResult, err := exporter.ExportHour(ctx, firstHour)
	require.NoError(t, err)
	require.NoError(t, store.PurgeHour(ctx, firstHour, firstResult.Archive.SHA256, true))

	store.now = func() time.Time { return secondHour.Add(10 * time.Minute) }
	inserted, err = store.Insert(ctx, second)
	require.NoError(t, err)
	require.True(t, inserted)
	secondResult, err := exporter.ExportHour(ctx, secondHour)
	require.NoError(t, err)
	require.NoError(t, store.PurgeHour(ctx, secondHour, secondResult.Archive.SHA256, true))

	fullCheckpoint, found, err := store.LoadProjectionCheckpoint(ctx, first.Delivery.SessionID, thirdHour)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, fullCheckpoint.Echo, 2)
	partialCheckpoint := fullCheckpoint
	partialCheckpoint.Echo = append([]projectionEchoTurn(nil), fullCheckpoint.Echo[1:]...)
	encodedPartial, err := encodeProjectionCheckpoint(first.Delivery.SessionID, partialCheckpoint, secondHour)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		UPDATE session_projection_checkpoints SET
			checkpoint_version = $2, checkpoint_zstd = $3,
			checkpoint_sha256 = $4, last_export_hour = $5
		WHERE session_id = $1`,
		encodedPartial.SessionID, encodedPartial.Version, encodedPartial.Compressed,
		encodedPartial.SHA256, encodedPartial.LastExportHour,
	)
	require.NoError(t, err)
	dryRun, err := store.ReseedProjectionArchives(ctx, archiveDir, DefaultPublicModel, false)
	require.NoError(t, err)
	require.False(t, dryRun.Applied)
	require.Equal(t, int64(1), dryRun.Sessions)
	require.Equal(t, int64(2), dryRun.Records)
	var checkpointCount int
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_projection_checkpoints`).Scan(&checkpointCount))
	require.Equal(t, 1, checkpointCount)
	checkpoint, found, err := store.LoadProjectionCheckpoint(ctx, first.Delivery.SessionID, thirdHour)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, checkpoint.Echo, 1, "dry-run must preserve the partial checkpoint")

	applied, err := store.ReseedProjectionArchives(ctx, archiveDir, DefaultPublicModel, true)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	require.False(t, applied.AlreadyApplied)
	checkpoint, found, err = store.LoadProjectionCheckpoint(ctx, first.Delivery.SessionID, thirdHour)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, checkpoint.Echo, 2)
	require.Equal(t, 128, checkpoint.Usage.PreviousPrefix)

	repeated, err := store.ReseedProjectionArchives(ctx, archiveDir, DefaultPublicModel, true)
	require.NoError(t, err)
	require.True(t, repeated.Applied)
	require.True(t, repeated.AlreadyApplied)
	var reseedCount int
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_projection_reseeds`).Scan(&reseedCount))
	require.Equal(t, 1, reseedCount)
	var backupCount int
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_projection_reseed_backups`).Scan(&backupCount))
	require.Equal(t, 1, backupCount)
}

func TestStoreRepairsRequestConversionRejectionBeforeExport(t *testing.T) {
	ctx := context.Background()
	store := startSessionDeliveryPostgres(t, ctx)
	require.NoError(t, store.Migrate(ctx))

	hour := time.Date(2026, 8, 9, 5, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return hour.Add(30 * time.Minute) }
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{UserID: 10, APIKeyID: 20, GroupID: 30},
		GatewayRequestID: "request-repair-array-output",
		StartedAt:        hour.Add(5 * time.Minute),
		CompletedAt:      hour.Add(5*time.Minute + time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","reasoning":{"effort":"high"},
			"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
				{"type":"function_call","call_id":"call_1","name":"wait","arguments":"{}"},
				{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"done"}]}
			]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_repair","object":"response","model":"gpt-5.6-sol","status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}
		}`),
	})
	require.NoError(t, err)
	require.NotNil(t, envelope.Delivery)
	envelope.Delivery = nil
	envelope.Rejection = &Rejection{
		Code:    "request_conversion_failed",
		Message: "json: cannot unmarshal array into Go struct field ResponsesInputItem.output of type string",
	}
	inserted, err := store.Insert(ctx, envelope)
	require.NoError(t, err)
	require.True(t, inserted)
	var storedBeforeRepair *Envelope
	require.NoError(t, store.ForEachHour(ctx, hour, func(candidate *Envelope) error {
		storedBeforeRepair = candidate
		return nil
	}))
	require.NotNil(t, storedBeforeRepair)
	originalRequest := append([]byte(nil), storedBeforeRepair.Original.Request...)
	originalResponse := append([]byte(nil), storedBeforeRepair.Original.Response...)

	dryRun, err := store.RepairRequestConversionRejections(ctx, canonicalizer, ConversionRepairOptions{
		Since: hour, Before: hour.Add(time.Hour), Limit: 10,
	})
	require.NoError(t, err)
	require.Equal(t, ConversionRepairStats{DryRun: true, Scanned: 1, Repairable: 1}, dryRun)
	stats, err := store.StatsForHour(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, HourStats{Records: 1, Rejected: 1}, stats)

	applied, err := store.RepairRequestConversionRejections(ctx, canonicalizer, ConversionRepairOptions{
		Since: hour, Before: hour.Add(time.Hour), Limit: 10, Apply: true,
	})
	require.NoError(t, err)
	require.Equal(t, ConversionRepairStats{Scanned: 1, Repairable: 1, Repaired: 1}, applied)
	stats, err = store.StatsForHour(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, HourStats{Records: 1, Deliverable: 1}, stats)

	var repaired *Envelope
	require.NoError(t, store.ForEachHour(ctx, hour, func(candidate *Envelope) error {
		repaired = candidate
		return nil
	}))
	require.NotNil(t, repaired)
	require.Nil(t, repaired.Rejection)
	require.NotNil(t, repaired.Delivery)
	require.True(t, bytes.Equal(originalRequest, repaired.Original.Request))
	require.True(t, bytes.Equal(originalResponse, repaired.Original.Response))
	require.Equal(t, envelope.RecordID, repaired.RecordID)
	require.Equal(t, envelope.SessionID, repaired.SessionID)
	require.Equal(t, envelope.RequestID, repaired.RequestID)

	archiveBackend, err := NewLocalArchiveBackend(filepath.Join(t.TempDir(), "repaired-archive"))
	require.NoError(t, err)
	exporter, err := NewExporter(store, archiveBackend, ExporterConfig{
		PublicModel: DefaultPublicModel,
		TempDir:     filepath.Join(t.TempDir(), "repaired-export-tmp"),
	})
	require.NoError(t, err)
	result, err := exporter.ExportHour(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Manifest.DeliveryCount)
	_, err = AuditArchivesFidelity(ctx, filepath.Dir(result.Archive.Name), DefaultPublicModel)
	require.NoError(t, err)

	repeated, err := store.RepairRequestConversionRejections(ctx, canonicalizer, ConversionRepairOptions{
		Since: hour, Before: hour.Add(time.Hour), Limit: 10, Apply: true,
	})
	require.NoError(t, err)
	require.Equal(t, ConversionRepairStats{}, repeated)
}

func TestHourlyExportUpgradesPreDeploymentThinkingShape(t *testing.T) {
	ctx := context.Background()
	store := startSessionDeliveryPostgres(t, ctx)
	require.NoError(t, store.Migrate(ctx))

	hour := time.Date(2026, 8, 9, 4, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return hour.Add(30 * time.Minute) }
	canonicalizer := newTestCanonicalizer(t)
	envelope := buildIntegrationResponsesEnvelope(t, canonicalizer, hour.Add(5*time.Minute), "pre-deployment-thinking")
	var request map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope.Delivery.Request, &request))
	request["thinking"] = json.RawMessage(`{"type":"adaptive"}`)
	request["output_config"] = json.RawMessage(`{"effort":"high"}`)
	request["messages"] = json.RawMessage(`[
		{"role":"user","content":[{"type":"text","text":"earlier question"}]},
		{"role":"assistant","content":[{"type":"thinking","thinking":"legacy visible reasoning","signature":""},{"type":"text","text":"earlier answer"}]},
		{"role":"user","content":[{"type":"text","text":"current question"}]}
	]`)
	envelope.Delivery.Request, _ = json.Marshal(request)
	require.NoError(t, ValidateDelivery(envelope.Delivery, DefaultPublicModel))
	inserted, err := store.Insert(ctx, envelope)
	require.NoError(t, err)
	require.True(t, inserted)

	archiveBackend, err := NewLocalArchiveBackend(filepath.Join(t.TempDir(), "legacy-thinking-archive"))
	require.NoError(t, err)
	exporter, err := NewExporter(store, archiveBackend, ExporterConfig{
		PublicModel: DefaultPublicModel,
		TempDir:     filepath.Join(t.TempDir(), "legacy-thinking-export-tmp"),
	})
	require.NoError(t, err)
	result, err := exporter.ExportHour(ctx, hour)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Manifest.DeliveryCount)
	report, err := AuditArchivesFidelity(ctx, filepath.Dir(result.Archive.Name), DefaultPublicModel)
	require.NoError(t, err)
	require.True(t, report.Passed)
	require.Zero(t, report.ViolationCount)

	records := readDeliveryRecordsFromArchive(t, result.Archive.Name)
	require.Len(t, records, 1)
	require.Equal(t, "adaptive", jsonPathString(t, records[0].Request, "thinking", "type"))
	require.Equal(t, "omitted", jsonPathString(t, records[0].Request, "thinking", "display"))
	require.NotEmpty(t, requestAssistantThinkingSignature(t, &records[0]))
	require.NotContains(t, string(records[0].Request), "legacy visible reasoning")
}

type integrationDurableArchive struct {
	*LocalArchiveBackend
}

func (b *integrationDurableArchive) Name() string {
	return rcloneArchiveBackendName
}

func (b *integrationDurableArchive) Durable() bool {
	return true
}

func (b *integrationDurableArchive) Put(ctx context.Context, name, sourcePath string) (ArchiveObject, error) {
	object, err := b.LocalArchiveBackend.Put(ctx, name, sourcePath)
	object.Backend = b.Name()
	return object, err
}

func buildIntegrationAnthropicEnvelope(
	t *testing.T,
	canonicalizer *Canonicalizer,
	started time.Time,
	requestID string,
	status int,
) *Envelope {
	t.Helper()
	response := `{"id":"msg_anthropic","type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`
	if status != 200 {
		response = `{"type":"error","error":{"type":"api_error","message":"failed"}}`
	}
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolAnthropicMessages,
		Endpoint:         "/v1/messages",
		Scope:            Scope{UserID: 10, APIKeyID: 20, GroupID: 30},
		GatewayRequestID: requestID,
		SessionHeader:    "anthropic-session",
		StartedAt:        started,
		CompletedAt:      started.Add(time.Second),
		HTTPStatus:       status,
		RequestBody:      []byte(`{"model":"claude-opus-4-8","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
		ResponseBody:     []byte(response),
	})
	require.NoError(t, err)
	return envelope
}

func buildIntegrationResponsesEnvelope(t *testing.T, canonicalizer *Canonicalizer, started time.Time, requestID string) *Envelope {
	t.Helper()
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{UserID: 10, APIKeyID: 20, GroupID: 30},
		GatewayRequestID: requestID,
		SessionHeader:    "codex-session",
		StartedAt:        started,
		CompletedAt:      started.Add(2 * time.Second),
		HTTPStatus:       200,
		RequestBody:      []byte(`{"model":"gpt-5.6-sol","instructions":"Be helpful.","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`),
		ResponseBody:     []byte(`{"id":"resp_codex","object":"response","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":10,"output_tokens":1}}`),
	})
	require.NoError(t, err)
	return envelope
}

func buildCrossHourEnvelope(t *testing.T, started time.Time, turn, totalInput int) *Envelope {
	t.Helper()
	request := `{
		"model":"claude-opus-5",
		"max_tokens":1024,
		"thinking":{"type":"adaptive","display":"omitted"},
		"output_config":{"effort":"low"},
		"messages":[{"role":"user","content":[{"type":"text","text":"first question"}]}]
	}`
	answer := "answer one"
	signature := thinkingsig.Generate(DefaultPublicModel, 1480)
	if turn == 2 {
		request = `{
			"model":"claude-opus-5",
			"max_tokens":1024,
			"thinking":{"type":"adaptive","display":"omitted"},
			"output_config":{"effort":"low"},
			"messages":[
				{"role":"user","content":[{"type":"text","text":"first question"}]},
				{"role":"assistant","content":[{"type":"text","text":"answer one"}]},
				{"role":"user","content":[{"type":"text","text":"second question"}]}
			]
		}`
		answer = "answer two"
		signature = thinkingsig.Generate(DefaultPublicModel, 1480)
	}
	response := json.RawMessage(
		`{"id":"msg_cross_hour_` + strconv.Itoa(turn) + `","type":"message","role":"assistant","model":"claude-opus-5",` +
			`"content":[{"type":"thinking","thinking":"","signature":` + mustJSONString(signature) + `},{"type":"text","text":` + mustJSONString(answer) + `}],` +
			`"stop_reason":"end_turn","usage":{"input_tokens":` + strconv.Itoa(totalInput) + `,"output_tokens":10}}`,
	)
	recordID := "rec_cross_hour_" + strconv.Itoa(turn)
	requestID := "req_cross_hour_" + strconv.Itoa(turn)
	delivery := &DeliveryRecord{
		SessionID: "session_cross_hour",
		RequestID: requestID,
		Timestamp: started,
		Metadata:  DeliveryMetadata{HTTPStatus: 200, LatencyMS: 100},
		Request:   json.RawMessage(request),
		Response:  DeliveryResponse{StatusCode: 200, ResponseData: response},
	}
	require.NoError(t, ValidateDelivery(delivery, DefaultPublicModel))
	return &Envelope{
		SchemaVersion:    SchemaVersion,
		RecordID:         recordID,
		SessionID:        delivery.SessionID,
		RequestID:        requestID,
		OccurredAt:       started,
		CapturedAt:       started.Add(100 * time.Millisecond),
		GatewayRequestID: "gateway-cross-hour-" + strconv.Itoa(turn),
		Source: SourceInfo{
			Protocol: ProtocolAnthropicMessages,
			Endpoint: "/v1/messages",
			Scope:    Scope{UserID: 10, APIKeyID: 20, GroupID: 30},
		},
		HTTPStatus: 200,
		DurationMS: 100,
		Delivery:   delivery,
	}
}

func readDeliveryRecordsFromArchive(t *testing.T, archivePath string) []DeliveryRecord {
	t.Helper()
	file, err := os.Open(archivePath)
	require.NoError(t, err)
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	require.NoError(t, err)
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	var records []DeliveryRecord
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if !strings.HasPrefix(header.Name, "delivery/") {
			continue
		}
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), int(defaultDecodedEnvelopeMaxBytes))
		for scanner.Scan() {
			var record DeliveryRecord
			require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
			records = append(records, record)
		}
		require.NoError(t, scanner.Err())
	}
	return records
}

func requireArchiveHasOnlyBlackBoxDelivery(t *testing.T, archivePath string) {
	t.Helper()
	file, err := os.Open(archivePath)
	require.NoError(t, err)
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	require.NoError(t, err)
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	var deliveryFiles int
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		content, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NotContains(t, string(content), "gpt-5.6")
		require.NotContains(t, string(content), "openai_responses")
		require.NotContains(t, string(content), `"original"`)
		require.NotContains(t, header.Name, "audit")
		if strings.HasPrefix(header.Name, "delivery/") {
			deliveryFiles++
		}
	}
	require.Equal(t, 2, deliveryFiles)
}

func startSessionDeliveryPostgres(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("Docker is unavailable; skipping Session PostgreSQL integration test")
	}
	container, err := tcpostgres.Run(
		ctx,
		sessionPostgresImage,
		tcpostgres.WithDatabase("session_delivery_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	store, err := OpenStore(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}
