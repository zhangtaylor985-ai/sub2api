package sessiondelivery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestForwarderHonorsConcurrencyAndAcknowledgesBatch(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 8<<20)
	require.NoError(t, err)
	for index := range 4 {
		_, err := spool.Write(&Envelope{
			SchemaVersion: SchemaVersion,
			RecordID:      fmt.Sprintf("rec_concurrent_%d", index),
			CapturedAt:    time.Date(2026, 8, 13, 7, 10, index, 0, time.UTC),
		})
		require.NoError(t, err)
	}

	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var inFlight atomic.Int64
	var maximum atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		_, _ = io.Copy(io.Discard, request.Body)
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"status":"inserted"}`))
	}))
	t.Cleanup(server.Close)

	forwarder, err := NewForwarder(spool, ForwarderConfig{
		Endpoint:    server.URL,
		Secret:      testHMACSecret,
		BatchLimit:  4,
		Concurrency: 2,
	})
	require.NoError(t, err)
	result := make(chan struct {
		stats ForwardStats
		err   error
	}, 1)
	go func() {
		stats, forwardErr := forwarder.ForwardOnce(context.Background())
		result <- struct {
			stats ForwardStats
			err   error
		}{stats: stats, err: forwardErr}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first upload did not start")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("second concurrent upload did not start")
	}
	require.Equal(t, int64(2), maximum.Load())
	close(release)

	select {
	case output := <-result:
		require.NoError(t, output.err)
		require.Equal(t, ForwardStats{Attempted: 4, Inserted: 4, Pending: 0}, output.stats)
	case <-time.After(5 * time.Second):
		t.Fatal("forwarder did not finish")
	}
	paths, err := spool.ListPending()
	require.NoError(t, err)
	require.Empty(t, paths)
}

func TestForwarderDistributesWorkersAcrossIndependentEndpoints(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 8<<20)
	require.NoError(t, err)
	for index := range 4 {
		_, err := spool.Write(&Envelope{
			SchemaVersion: SchemaVersion,
			RecordID:      fmt.Sprintf("rec_endpoint_%d", index),
			CapturedAt:    time.Date(2026, 8, 13, 7, 15, index, 0, time.UTC),
		})
		require.NoError(t, err)
	}

	var firstCalls atomic.Int64
	var secondCalls atomic.Int64
	newServer := func(calls *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			_, _ = io.Copy(io.Discard, request.Body)
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"status":"inserted"}`))
		}))
	}
	first := newServer(&firstCalls)
	defer first.Close()
	second := newServer(&secondCalls)
	defer second.Close()

	forwarder, err := NewForwarder(spool, ForwarderConfig{
		Endpoints:   []string{first.URL, second.URL},
		Secret:      testHMACSecret,
		BatchLimit:  4,
		Concurrency: 2,
	})
	require.NoError(t, err)
	stats, err := forwarder.ForwardOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 4, stats.Inserted)
	require.Positive(t, firstCalls.Load())
	require.Positive(t, secondCalls.Load())
}

func TestForwarderKeepsOtherInFlightUploadsAfterTransientFailure(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 8<<20)
	require.NoError(t, err)
	for index := range 2 {
		_, err := spool.Write(&Envelope{
			SchemaVersion: SchemaVersion,
			RecordID:      fmt.Sprintf("rec_partial_%d", index),
			CapturedAt:    time.Date(2026, 8, 13, 7, 20, index, 0, time.UTC),
		})
		require.NoError(t, err)
	}

	secondStarted := make(chan struct{})
	firstFailed := make(chan struct{})
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if calls.Add(1) == 1 {
			<-secondStarted
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"status":"error","code":"ingest_busy"}`))
			close(firstFailed)
			return
		}
		close(secondStarted)
		<-firstFailed
		time.Sleep(100 * time.Millisecond)
		_, _ = io.Copy(io.Discard, request.Body)
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"status":"inserted"}`))
	}))
	t.Cleanup(server.Close)

	forwarder, err := NewForwarder(spool, ForwarderConfig{
		Endpoint:    server.URL,
		Secret:      testHMACSecret,
		BatchLimit:  2,
		Concurrency: 2,
	})
	require.NoError(t, err)
	stats, err := forwarder.ForwardOnce(context.Background())
	require.ErrorContains(t, err, "temporarily unavailable")
	require.Equal(t, ForwardStats{Attempted: 2, Inserted: 1, Pending: 1}, stats)
	paths, err := spool.ListPending()
	require.NoError(t, err)
	require.Len(t, paths, 1)
}

func TestIngestHandlerBoundsDecodeConcurrencySeparately(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	require.NoError(t, err)
	handler, err := NewIngestHandler(store, IngestHandlerConfig{
		Secret:              testHMACSecret,
		TempDir:             t.TempDir(),
		MaxConcurrent:       4,
		MaxDecodeConcurrent: 5,
	})
	require.NoError(t, err)
	require.Equal(t, 4, cap(handler.semaphore))
	require.Equal(t, 4, cap(handler.decodeSemaphore), "decode concurrency must not exceed upload concurrency")

	handler, err = NewIngestHandler(store, IngestHandlerConfig{
		Secret:        testHMACSecret,
		TempDir:       t.TempDir(),
		MaxConcurrent: 16,
	})
	require.NoError(t, err)
	require.Equal(t, 16, cap(handler.semaphore))
	require.Equal(t, 1, cap(handler.decodeSemaphore), "large envelope decoding defaults to one lane")
}
