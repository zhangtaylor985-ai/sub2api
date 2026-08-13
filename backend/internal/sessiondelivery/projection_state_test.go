package sessiondelivery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProjectionCheckpointRoundTrip(t *testing.T) {
	hour := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	checkpoint := projectionCheckpoint{
		Version: projectionCheckpointVersion,
		Echo: []projectionEchoTurn{
			{
				Key: "text:" + strings.Repeat("a", 64),
				Thinking: []json.RawMessage{
					json.RawMessage(`{"type":"thinking","thinking":"","signature":"sig-checkpoint"}`),
				},
			},
		},
		Usage: projectionUsageCheckpoint{
			PreviousPrefix:    49713,
			FirstMessageSHA:   strings.Repeat("b", 64),
			PreviousOccurred:  hour.Add(58 * time.Minute),
			HasPreviousRecord: true,
		},
	}

	encoded, err := encodeProjectionCheckpoint("session_checkpoint", checkpoint, hour)
	require.NoError(t, err)
	require.Equal(t, projectionCheckpointVersion, encoded.Version)
	require.Equal(t, hour, encoded.LastExportHour)
	require.NotEmpty(t, encoded.Compressed)
	require.Len(t, encoded.SHA256, 64)

	decoded, err := decodeProjectionCheckpoint(encoded.Compressed, encoded.SHA256, encoded.Version)
	require.NoError(t, err)
	require.Equal(t, checkpoint, decoded)
}

func TestProjectionCheckpointRejectsChecksumMismatch(t *testing.T) {
	checkpoint := projectionCheckpoint{Version: projectionCheckpointVersion}
	encoded, err := encodeProjectionCheckpoint(
		"session_checkpoint",
		checkpoint,
		time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	_, err = decodeProjectionCheckpoint(encoded.Compressed, strings.Repeat("0", 64), encoded.Version)
	require.ErrorContains(t, err, "checksum mismatch")
}
