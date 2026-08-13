package sessiondelivery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestDecodeStoredProjectionEnvelopeSkipsOriginalAuditPayload(t *testing.T) {
	envelope := Envelope{
		SchemaVersion: SchemaVersion,
		RecordID:      "rec_stream_projection",
		Original: OriginalPayload{
			Request:  mustJSON(strings.Repeat("request-audit-", 1<<16)),
			Response: mustJSON(strings.Repeat("response-audit-", 1<<16)),
		},
		Delivery: echoTestRecord(
			"session_stream_projection", "question", "", true,
			"answer", "sig-stream-projection",
		),
	}
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)
	digest := sha256.Sum256(payload)
	compressed := compressProjectionFixture(t, payload)
	decoder, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer decoder.Close()

	projected, err := decodeStoredProjectionEnvelope(
		decoder, compressed, hex.EncodeToString(digest[:]), int64(len(payload)+1),
	)
	require.NoError(t, err)
	require.Equal(t, envelope.RecordID, projected.RecordID)
	require.Equal(t, envelope.Delivery, projected.Delivery)
	require.Nil(t, projected.Rejection)
}

func TestDecodeStoredProjectionEnvelopeValidatesChecksumSizeAndTrailingData(t *testing.T) {
	payload := []byte(`{"record_id":"rec_projection","rejection":{"code":"test","message":"test"}}`)
	digest := sha256.Sum256(payload)
	compressed := compressProjectionFixture(t, payload)
	decoder, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer decoder.Close()

	_, err = decodeStoredProjectionEnvelope(decoder, compressed, strings.Repeat("0", 64), int64(len(payload)+1))
	require.ErrorContains(t, err, "checksum mismatch")

	_, err = decodeStoredProjectionEnvelope(decoder, compressed, hex.EncodeToString(digest[:]), int64(len(payload)-1))
	require.ErrorContains(t, err, "exceeds")

	trailing := append(append([]byte(nil), payload...), []byte(` {}`)...)
	trailingDigest := sha256.Sum256(trailing)
	_, err = decodeStoredProjectionEnvelope(
		decoder,
		compressProjectionFixture(t, trailing),
		hex.EncodeToString(trailingDigest[:]),
		int64(len(trailing)+1),
	)
	require.ErrorContains(t, err, "trailing byte")
}

func compressProjectionFixture(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	require.NoError(t, err)
	_, err = encoder.Write(payload)
	require.NoError(t, err)
	require.NoError(t, encoder.Close())
	return compressed.Bytes()
}

func BenchmarkDecodeStoredProjectionEnvelopeLargeOriginal(b *testing.B) {
	envelope := Envelope{
		SchemaVersion: SchemaVersion,
		RecordID:      "rec_projection_benchmark",
		Original: OriginalPayload{
			Request:  mustJSON(strings.Repeat("large-request-audit-", 1<<20)),
			Response: mustJSON(strings.Repeat("large-response-audit-", 1<<20)),
		},
		Rejection: &Rejection{Code: "benchmark", Message: "not delivered"},
	}
	payload, err := json.Marshal(envelope)
	require.NoError(b, err)
	digest := sha256.Sum256(payload)
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	require.NoError(b, err)
	_, err = encoder.Write(payload)
	require.NoError(b, err)
	require.NoError(b, encoder.Close())
	decoder, err := zstd.NewReader(nil)
	require.NoError(b, err)
	defer decoder.Close()

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for range b.N {
		_, err := decodeStoredProjectionEnvelope(
			decoder,
			compressed.Bytes(),
			hex.EncodeToString(digest[:]),
			int64(len(payload)+1),
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}
