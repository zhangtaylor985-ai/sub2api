package main

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
	"github.com/stretchr/testify/require"
)

func TestParseHour(t *testing.T) {
	hour, err := parseHour("2026-08-13T07")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 8, 13, 7, 0, 0, 0, time.UTC), hour)

	hour, err = parseHour("2026-08-13T15:00:00+08:00")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 8, 13, 7, 0, 0, 0, time.UTC), hour)

	_, err = parseHour("2026-08-13T07:30:00Z")
	require.Error(t, err)
}

func TestExportCutoffAppliesSettlingDelayBeforeTruncation(t *testing.T) {
	now := time.Date(2026, 8, 13, 8, 37, 0, 0, time.UTC)
	require.Equal(t,
		time.Date(2026, 8, 13, 6, 0, 0, 0, time.UTC),
		exportCutoff(now, 2*time.Hour),
	)
	require.Equal(t,
		time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC),
		exportCutoff(now, 0),
	)
}

func TestSplitNonEmptyEndpoints(t *testing.T) {
	require.Equal(t,
		[]string{"http://127.0.0.1:18092", "http://127.0.0.1:18093"},
		splitNonEmpty(" http://127.0.0.1:18092, ,http://127.0.0.1:18093 "),
	)
	require.Empty(t, splitNonEmpty(" , "))
}

func TestVerifyBatchWithBackend(t *testing.T) {
	backend := &testArchiveBackend{name: "drive", durable: true}
	batch := &sessiondelivery.ExportBatch{
		Status:         "verified",
		ArchiveBackend: "drive",
		ArchiveObject:  "gdrive:folder/archive.tar.zst",
		ArchiveSHA256:  "abc",
		ArchiveSize:    123,
	}
	require.NoError(t, verifyBatchWithBackend(context.Background(), batch, backend))
	require.True(t, backend.verified)
	require.Equal(t, int64(123), backend.object.Size)

	backend.name = "other"
	err := verifyBatchWithBackend(context.Background(), batch, backend)
	require.ErrorContains(t, err, "does not match")
}

func TestLocalArchiveCannotAuthorizeAutomaticPurge(t *testing.T) {
	backend, err := newArchiveBackend("local", t.TempDir(), "", "")
	require.NoError(t, err)
	require.False(t, backend.Durable())
}

type testArchiveBackend struct {
	name     string
	durable  bool
	verified bool
	object   sessiondelivery.ArchiveObject
}

func (b *testArchiveBackend) Name() string {
	return b.name
}

func (b *testArchiveBackend) Durable() bool {
	return b.durable
}

func (b *testArchiveBackend) Put(context.Context, string, string) (sessiondelivery.ArchiveObject, error) {
	return sessiondelivery.ArchiveObject{}, nil
}

func (b *testArchiveBackend) Verify(_ context.Context, object sessiondelivery.ArchiveObject) error {
	b.verified = true
	b.object = object
	return nil
}
