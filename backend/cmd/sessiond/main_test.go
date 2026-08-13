package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareIngestTempDirRemovesOnlySessionTempFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "ingest-tmp")
	require.NoError(t, os.MkdirAll(directory, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(directory, ".ingest-stale.json.zst"), []byte("stale"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, ".ingest-unrelated.tmp"), []byte("keep"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "keep.json.zst"), []byte("keep"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(directory, ".ingest-directory.json.zst"), 0o700))

	removed, err := prepareIngestTempDir(directory)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, err = os.Stat(filepath.Join(directory, ".ingest-stale.json.zst"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(directory, ".ingest-unrelated.tmp"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(directory, "keep.json.zst"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(directory, ".ingest-directory.json.zst"))
	require.NoError(t, err)
}
