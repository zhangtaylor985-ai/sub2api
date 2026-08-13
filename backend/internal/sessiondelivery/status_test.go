package sessiondelivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type fixedHostStatusCollector struct {
	status HostStatus
	err    error
}

func (c fixedHostStatusCollector) Collect(context.Context, string) (HostStatus, error) {
	return c.status, c.err
}

func TestStatusEndpointRequiresSignatureAndReturnsSanitizedSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	require.NoError(t, err)
	now := time.Date(2026, 8, 13, 6, 30, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT pg_database_size\(current_database\(\)\).*FROM pg_stat_activity`).
		WillReturnRows(sqlmock.NewRows([]string{"size", "active", "total", "max"}).AddRow(int64(40<<20), 1, 3, 100))
	mock.ExpectQuery(`SELECT COUNT\(\*\).*FROM pg_inherits`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
	mock.ExpectQuery(`SELECT COUNT\(\*\).*FROM session_records`).
		WillReturnRows(sqlmock.NewRows([]string{"records", "deliverable", "rejected", "bytes", "hour", "recent", "first", "last"}).
			AddRow(int64(12), int64(10), int64(2), int64(2048), int64(4), int64(2), now.Add(-time.Hour), now))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FILTER.*FROM session_export_batches`).
		WillReturnRows(sqlmock.NewRows([]string{"files", "bytes", "records", "deliveries", "rejected", "failed", "exporting", "last"}).
			AddRow(int64(2), int64(4096), int64(8), int64(7), int64(1), int64(0), int64(0), now.Add(-time.Minute)))
	mock.ExpectQuery(`SELECT export_hour, status, record_count.*FROM session_export_batches`).
		WithArgs(12).
		WillReturnRows(sqlmock.NewRows([]string{
			"export_hour", "status", "record_count", "delivery_count", "rejected_count",
			"archive_backend", "archive_size", "started_at", "archived_at", "verified_at", "purged_at",
		}).AddRow(
			now.Add(-time.Hour), "purged", int64(8), int64(7), int64(1),
			"rclone", int64(4096), now.Add(-time.Hour), now.Add(-50*time.Minute), now.Add(-49*time.Minute), now.Add(-48*time.Minute),
		))

	handler := &IngestHandler{
		store:           store,
		secret:          []byte(testHMACSecret),
		allowedSkew:     5 * time.Minute,
		diskPath:        t.TempDir(),
		rejectDiskUsage: 75,
		hostCollector: fixedHostStatusCollector{status: HostStatus{
			Hostname: "session-db", CPUCount: 2, DiskUsedPercent: 16,
		}},
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	timestamp := time.Now().UTC().Format(time.RFC3339)
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Header.Set(ingestTimestampHeader, timestamp)
	request.Header.Set(ingestSignatureHeader, signStatus([]byte(testHMACSecret), timestamp))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var snapshot StatusSnapshot
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &snapshot))
	require.Equal(t, "healthy", snapshot.Status)
	require.Equal(t, int64(12), snapshot.Sessions.RecordsInDatabase)
	require.Equal(t, int64(2), snapshot.Delivery.ArchiveFilesVerified)
	require.Len(t, snapshot.RecentBatches, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStatusClientSignsRequestAndNormalizesEndpoint(t *testing.T) {
	secret := strings.Repeat("s", 32)
	observedAt := time.Date(2026, 8, 13, 7, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/v1/status", request.URL.Path)
		timestamp := request.Header.Get(ingestTimestampHeader)
		require.Equal(t, signStatus([]byte(secret), timestamp), request.Header.Get(ingestSignatureHeader))
		_ = json.NewEncoder(writer).Encode(StatusSnapshot{Status: "healthy", ObservedAt: observedAt, Warnings: []string{}})
	}))
	t.Cleanup(server.Close)
	client, err := NewStatusClient(StatusClientConfig{Endpoint: server.URL + "/v1/records", Secret: secret})
	require.NoError(t, err)
	snapshot, err := client.Fetch(context.Background())
	require.NoError(t, err)
	require.Equal(t, observedAt, snapshot.ObservedAt)
	require.Equal(t, "healthy", snapshot.Status)

	_, err = NewStatusClient(StatusClientConfig{Endpoint: "http://example.com", Secret: secret})
	require.ErrorContains(t, err, "loopback")
}

func TestLinuxStatusParsersAndCPUPercentage(t *testing.T) {
	dir := t.TempDir()
	loadPath := filepath.Join(dir, "loadavg")
	memoryPath := filepath.Join(dir, "meminfo")
	uptimePath := filepath.Join(dir, "uptime")
	cpuPath := filepath.Join(dir, "stat")
	require.NoError(t, os.WriteFile(loadPath, []byte("0.10 0.20 0.30 1/100 1\n"), 0o600))
	require.NoError(t, os.WriteFile(memoryPath, []byte("MemTotal: 2048 kB\nMemAvailable: 512 kB\nSwapTotal: 1024 kB\nSwapFree: 256 kB\n"), 0o600))
	require.NoError(t, os.WriteFile(uptimePath, []byte("123.45 0.00\n"), 0o600))
	require.NoError(t, os.WriteFile(cpuPath, []byte("cpu  100 20 30 400 50 0 0 0\n"), 0o600))

	load, err := readLoadAverage(loadPath)
	require.NoError(t, err)
	require.Equal(t, [3]float64{0.1, 0.2, 0.3}, load)
	memory, err := readMemoryStatus(memoryPath)
	require.NoError(t, err)
	require.Equal(t, int64(2048*1024), memory.total)
	require.Equal(t, int64(1536*1024), memory.used)
	require.Equal(t, int64(768*1024), memory.swapUsed)
	uptime, err := readUptime(uptimePath)
	require.NoError(t, err)
	require.Equal(t, int64(123), uptime)
	cpu, err := readCPUSample(cpuPath)
	require.NoError(t, err)
	require.Equal(t, uint64(450), cpu.idle)
	require.InDelta(t, 50, cpuUsedPercent(cpuSample{total: 100, idle: 40}, cpuSample{total: 200, idle: 90}), 0.001)
}

func TestInspectSpoolUsesMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"pending", "quarantine", "tmp"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pending", "one.json.zst"), []byte("1234"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "quarantine", "bad.json.zst"), []byte("12"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tmp", "temp.json.zst"), []byte("1"), 0o600))
	stats, err := InspectSpool(dir, 10)
	require.NoError(t, err)
	require.Equal(t, 1, stats.PendingRecords)
	require.Equal(t, int64(4), stats.PendingBytes)
	require.Equal(t, int64(2), stats.QuarantinedBytes)
	require.Equal(t, int64(6), stats.UsedBytes)
	require.InDelta(t, 60, stats.UsedPercent, 0.001)
	require.NotNil(t, stats.OldestPendingAt)
}
