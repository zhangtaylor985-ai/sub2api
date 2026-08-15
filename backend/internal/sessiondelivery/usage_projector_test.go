package sessiondelivery

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func usageTestRecord(sessionID string, ts time.Time, totalInput, outputTokens int, firstUserText string) *DeliveryRecord {
	request := json.RawMessage(fmt.Sprintf(
		`{"model":"claude-opus-5","max_tokens":1024,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":[{"type":"text","text":%s}]}]}`,
		mustJSONString(firstUserText)))
	// stored usage as captured from the GPT upstream: no cache split at all
	response := json.RawMessage(fmt.Sprintf(
		`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":%d,"output_tokens":%d}}`,
		totalInput, outputTokens))
	return &DeliveryRecord{
		SessionID: sessionID,
		RequestID: "req_" + sessionID + ts.Format("150405.000"),
		Timestamp: DeliveryTime{ts},
		Metadata:  DeliveryMetadata{HTTPStatus: 200, LatencyMS: 5},
		Request:   request,
		Response:  DeliveryResponse{StatusCode: 200, ResponseData: response},
	}
}

func usageNumbers(t *testing.T, record *DeliveryRecord) (input, creation, read, output int) {
	t.Helper()
	var response struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	require.NoError(t, json.Unmarshal(record.Response.ResponseData, &response))
	return rawInt(response.Usage["input_tokens"]),
		rawInt(response.Usage["cache_creation_input_tokens"]),
		rawInt(response.Usage["cache_read_input_tokens"]),
		rawInt(response.Usage["output_tokens"])
}

// Replays the exact six-message usage progression measured on a real local
// Claude Opus 5 session (d0346cfb), including the mid-session compaction.
func TestUsageProjectorMatchesRealOpus5Session(t *testing.T) {
	p := &usageProjector{}
	base := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

	// real totals: input+creation+read per turn
	totals := []int{49715, 57578, 58783, 65418}
	wantCreation := []int{49713, 7863, 1205, 6635}
	wantRead := []int{0, 49713, 57576, 58781}

	for i, total := range totals {
		rec := usageTestRecord("session_real", base.Add(time.Duration(i)*time.Minute), total, 100, "same first message")
		require.NoError(t, p.process(rec))
		input, creation, read, output := usageNumbers(t, rec)
		require.Equal(t, 2, input, "turn %d", i)
		require.Equal(t, wantCreation[i], creation, "turn %d creation", i)
		require.Equal(t, wantRead[i], read, "turn %d read", i)
		require.Equal(t, 100, output, "turn %d output untouched", i)
	}

	// compaction: first user message changes -> chain restarts with read=0
	compacted := usageTestRecord("session_real", base.Add(4*time.Minute), 65440, 100, "COMPACTED summary head")
	require.NoError(t, p.process(compacted))
	_, creation, read, _ := usageNumbers(t, compacted)
	require.Equal(t, 0, read)
	require.Equal(t, 65438, creation)

	// next turn continues the new chain
	next := usageTestRecord("session_real", base.Add(5*time.Minute), 66013, 100, "COMPACTED summary head")
	require.NoError(t, p.process(next))
	_, creation, read, _ = usageNumbers(t, next)
	require.Equal(t, 65438, read)
	require.Equal(t, 573, creation)
}

func TestUsageProjectorCacheTTLExpiry(t *testing.T) {
	p := &usageProjector{}
	base := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

	t1 := usageTestRecord("session_ttl", base, 10000, 50, "head")
	require.NoError(t, p.process(t1))
	_, _, read1, _ := usageNumbers(t, t1)
	require.Equal(t, 0, read1)

	// 6 minutes later: the 5m ephemeral cache expired -> full re-creation
	t2 := usageTestRecord("session_ttl", base.Add(6*time.Minute), 10100, 50, "head")
	require.NoError(t, p.process(t2))
	input2, creation2, read2, _ := usageNumbers(t, t2)
	require.Equal(t, 2, input2)
	require.Equal(t, 0, read2)
	require.Equal(t, 10098, creation2)
}

func TestUsageProjectorTimestampRegressionRestartsConcurrentBranch(t *testing.T) {
	p := &usageProjector{}
	base := time.Date(2026, 8, 13, 16, 59, 35, 0, time.UTC)

	first := usageTestRecord("session_overlap", base, 10000, 50, "same head")
	require.NoError(t, p.process(first))

	// The next ingest-hour archive can contain a request that started a few
	// seconds earlier but completed later. It must not read a cache prefix from
	// the future record that happened to be replayed first.
	overlap := usageTestRecord("session_overlap", base.Add(-4*time.Second), 10100, 50, "same head")
	require.NoError(t, p.process(overlap))
	input, creation, read, _ := usageNumbers(t, overlap)
	require.Equal(t, 2, input)
	require.Equal(t, 0, read)
	require.Equal(t, 10098, creation)
}

func TestUsageProjectorFullFieldShape(t *testing.T) {
	p := &usageProjector{}
	rec := usageTestRecord("session_shape", time.Now().UTC(), 8000, 42, "head")
	require.NoError(t, p.process(rec))

	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Response.ResponseData, &response))
	var usage map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response["usage"], &usage))

	// every field a real Opus 5 response carries
	for _, key := range []string{
		"input_tokens", "output_tokens", "cache_creation_input_tokens",
		"cache_read_input_tokens", "cache_creation", "server_tool_use",
		"service_tier", "inference_geo", "iterations", "speed",
	} {
		_, ok := usage[key]
		require.True(t, ok, "usage missing %q", key)
	}
	require.Equal(t, "standard", rawString(usage["service_tier"]))
	require.Equal(t, "global", rawString(usage["inference_geo"]))
	require.Equal(t, "standard", rawString(usage["speed"]))
	require.Equal(t, float64(7998), jsonPathNumber(t, mustJSONRaw(t, usage), "cache_creation", "ephemeral_5m_input_tokens"))
	require.NoError(t, ValidateDelivery(rec, DefaultPublicModel))
}

func TestUsageProjectorSkipsMissingUsage(t *testing.T) {
	p := &usageProjector{}
	rec := usageTestRecord("session_nousage", time.Now().UTC(), 100, 10, "head")
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Response.ResponseData, &response))
	delete(response, "usage")
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	rec.Response.ResponseData = encoded

	require.NoError(t, p.process(rec))
	require.NotContains(t, string(rec.Response.ResponseData), "cache_read_input_tokens")
}

func mustJSONRaw(t *testing.T, v map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
