package main

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
	"github.com/stretchr/testify/require"
)

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
