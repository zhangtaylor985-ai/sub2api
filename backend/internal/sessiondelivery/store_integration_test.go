//go:build integration

package sessiondelivery

import (
	"archive/tar"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	response := `{"id":"msg_anthropic","type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`
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
		ResponseBody:     []byte(`{"id":"resp_codex","object":"response","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`),
	})
	require.NoError(t, err)
	return envelope
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
