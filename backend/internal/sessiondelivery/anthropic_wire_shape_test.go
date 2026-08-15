package sessiondelivery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAnthropicPublicIDShape(t *testing.T) {
	id := anthropicPublicID("msg_", "gateway-source-id")
	require.True(t, strings.HasPrefix(id, "msg_01"), id)
	require.Len(t, id, len("msg_")+2+22)
	require.True(t, hasAnthropicIDShape("msg_", id))

	// base58: none of the visually ambiguous glyphs may appear, matching every
	// real Claude Code identifier measured locally.
	for _, glyph := range []string{"0", "O", "I", "l"} {
		require.NotContains(t, id[len("msg_01"):], glyph)
	}

	// Deterministic, and preserved once it already has the shape.
	require.Equal(t, id, anthropicPublicID("msg_", "gateway-source-id"))
	require.Equal(t, id, anthropicResponseID(id))
	require.NotEqual(t, id, anthropicPublicID("msg_", "other-source-id"))
}

// A freshly captured OpenAI identifier and the same identifier as already
// projected into an existing archive must converge, otherwise ingest and
// offline rebuild would disagree and every replay would rewrite the archive.
func TestAnthropicToolIDConvergesAcrossPipelineStages(t *testing.T) {
	fresh := anthropicToolID("call_abc123", false)
	archived := anthropicToolID("toolu_abc123", false)
	require.Equal(t, fresh, archived)
	require.True(t, hasAnthropicIDShape("toolu_", fresh), fresh)

	freshServer := anthropicToolID("ws_00d7feed", true)
	archivedServer := anthropicToolID("srvtoolu_ws_00d7feed", true)
	require.Equal(t, freshServer, archivedServer)
	require.True(t, hasAnthropicIDShape("srvtoolu_", freshServer), freshServer)

	// A real Anthropic identifier passes through untouched.
	real := "toolu_01FJRWUxN4oq13uZjFSsHj27"
	require.Equal(t, real, anthropicToolID(real, false))
}

func TestNormalizeAnthropicWireShapeCompletesResponseEnvelope(t *testing.T) {
	request := json.RawMessage(`{
		"model":"claude-opus-5","max_tokens":1024,
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)
	response := json.RawMessage(`{
		"id":"msg_internal","type":"message","role":"assistant","model":"claude-opus-5",
		"content":[{"type":"tool_use","id":"call_x","name":"Read","input":{}}],
		"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}
	}`)

	_, normalized, stats, err := normalizeProjectionFidelity(request, response, fidelityNormalizationOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.ResponseIDsReshaped)
	require.Equal(t, int64(1), stats.ToolCallersCompleted)
	require.Equal(t, int64(1), stats.StopFieldsCompleted)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(normalized, &decoded))
	require.Equal(t, "null", string(decoded["stop_sequence"]))
	require.Equal(t, "null", string(decoded["stop_details"]))
	require.True(t, hasAnthropicIDShape("msg_", rawString(decoded["id"])))

	var blocks []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(decoded["content"], &blocks))
	require.JSONEq(t, `{"type":"direct"}`, string(blocks[0]["caller"]))
}

// Member order is asserted against the order measured in real Claude Code
// transcripts, not merely against "not alphabetical".
func TestFinalizeDeliveryRecordAppliesMeasuredMemberOrder(t *testing.T) {
	record := usageTestRecord("session_order", fixedTestTime(), 100, 10, "hi")
	record.Request = json.RawMessage(`{
		"max_tokens":1024,
		"messages":[{"content":[{"text":"hi","type":"text"}],"role":"user"}],
		"model":"claude-opus-5"
	}`)
	record.Response.ResponseData = json.RawMessage(`{
		"content":[{"signature":"s","thinking":"","type":"thinking"}],
		"id":"msg_01AAAAAAAAAAAAAAAAAAAA","model":"claude-opus-5","role":"assistant",
		"stop_details":null,"stop_reason":"end_turn","stop_sequence":null,"type":"message",
		"usage":{"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":0},
			"cache_creation_input_tokens":1,"cache_read_input_tokens":0,"input_tokens":2,
			"output_tokens":3,"server_tool_use":{"web_fetch_requests":0,"web_search_requests":0}}
	}`)

	require.NoError(t, finalizeDeliveryRecord(record))

	require.True(t, strings.HasPrefix(string(record.Request), `{"model":"claude-opus-5","max_tokens":1024,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]`), string(record.Request))
	require.True(t, strings.HasPrefix(string(record.Response.ResponseData),
		`{"id":"msg_01AAAAAAAAAAAAAAAAAAAA","type":"message","role":"assistant","model":"claude-opus-5","content":[{"type":"thinking","thinking":"","signature":"s"}],"stop_reason":"end_turn","stop_sequence":null,"stop_details":null,"usage":{"input_tokens":2,"cache_creation_input_tokens":1,"cache_read_input_tokens":0,"output_tokens":3,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0}`),
		string(record.Response.ResponseData))

	// Re-finalizing is a no-op, which is what keeps archive replays stable.
	before := append(json.RawMessage(nil), record.Response.ResponseData...)
	require.NoError(t, finalizeDeliveryRecord(record))
	require.Equal(t, string(before), string(record.Response.ResponseData))
}

// An unrecognized block type keeps its original bytes rather than being
// reordered on a guess.
func TestFinalizeDeliveryRecordLeavesUnknownBlocksUntouched(t *testing.T) {
	record := usageTestRecord("session_unknown", fixedTestTime(), 100, 10, "hi")
	record.Request = json.RawMessage(`{"model":"claude-opus-5","max_tokens":1,"messages":[{"role":"user","content":[{"zeta":1,"type":"future_block","alpha":2}]}]}`)
	require.NoError(t, finalizeDeliveryRecord(record))
	require.Contains(t, string(record.Request), `{"zeta":1,"type":"future_block","alpha":2}`)
}

func TestDeliveryUserAgentUsesClientReportedVersion(t *testing.T) {
	request, err := decodeJSONObject(json.RawMessage(
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.220.7a9"}]}`,
	), "request")
	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.220", deliveryUserAgent(request))

	// No version reported: the member stays absent instead of being invented.
	bare, err := decodeJSONObject(json.RawMessage(`{"system":[{"type":"text","text":"hello"}]}`), "request")
	require.NoError(t, err)
	require.Empty(t, deliveryUserAgent(bare))
}

func TestDeliveryTimeMarshalsFixedMilliseconds(t *testing.T) {
	encoded, err := json.Marshal(DeliveryTime{time.Date(2026, 8, 13, 5, 58, 0, 0, time.UTC)})
	require.NoError(t, err)
	require.Equal(t, `"2026-08-13T05:58:00.000Z"`, string(encoded))

	encoded, err = json.Marshal(DeliveryTime{time.Date(2026, 8, 13, 5, 58, 0, 123456789, time.UTC)})
	require.NoError(t, err)
	require.Equal(t, `"2026-08-13T05:58:00.123Z"`, string(encoded))

	var decoded DeliveryTime
	require.NoError(t, json.Unmarshal([]byte(`"2026-08-13T05:58:00.123Z"`), &decoded))
	require.Equal(t, time.Date(2026, 8, 13, 5, 58, 0, 123000000, time.UTC), decoded.Time)
}

// The parameter must be recognized regardless of the punctuation that follows
// it. An allow list of terminators silently kept the parameter after a comma,
// semicolon or slash, which is exactly how a model cites a URL inline in prose.
func TestStripOpenAISearchTrackingHandlesProsePunctuation(t *testing.T) {
	stripped := map[string]string{
		"sole":         "https://e.com?utm_source=openai",
		"leading":      "https://e.com?utm_source=openai&a=1",
		"trailing":     "https://e.com?a=1&utm_source=openai",
		"comma":        "see https://e.com?utm_source=openai, then",
		"semicolon":    "see https://e.com?utm_source=openai; then",
		"slash":        "see https://e.com?utm_source=openai/sub",
		"pipe":         "see https://e.com?utm_source=openai|next",
		"markdown":     "[x](https://e.com?utm_source=openai)",
		"fragment":     "https://e.com?utm_source=openai#top",
		"sentence dot": "see https://e.com?utm_source=openai. Then",
		"upper case":   "https://e.com?UTM_SOURCE=OPENAI",
	}
	for name, input := range stripped {
		t.Run("strips "+name, func(t *testing.T) {
			out, count := stripOpenAISearchTracking(input)
			require.Positive(t, count)
			require.NotContains(t, strings.ToLower(out), "utm_source=openai")
		})
	}

	// A different parameter that merely starts the same way must survive, so the
	// pass never rewrites an unrelated URL.
	preserved := map[string]string{
		"longer value":  "https://e.com?utm_source=openai2",
		"longer name":   "https://e.com?xutm_source=openai",
		"dotted value":  "https://e.com?utm_source=openai.internal",
		"encoded value": "https://e.com?utm_source=openai%20x",
	}
	for name, input := range preserved {
		t.Run("preserves "+name, func(t *testing.T) {
			out, count := stripOpenAISearchTracking(input)
			require.Zero(t, count)
			require.Equal(t, input, out)
		})
	}

	// Adjacent duplicates collapse without leaving a stray separator.
	out, count := stripOpenAISearchTracking("https://e.com?utm_source=openai&utm_source=openai&a=1")
	require.Equal(t, int64(2), count)
	require.Equal(t, "https://e.com?a=1", out)
}

// Search result blocks carry the cited URLs themselves, so cleaning only the
// prose that cites them leaves the parameter in the delivered result set.
func TestNormalizeProjectionFidelityStripsServerToolResultURLs(t *testing.T) {
	tracked := "https://e.com/a?acc=1&utm_source=openai"
	request := mustJSON(map[string]any{
		"model": DefaultPublicModel, "max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "search"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "web_search_tool_result", "tool_use_id": "srvtoolu_ws_1", "content": []any{
					map[string]any{"type": "web_search_result", "url": tracked, "title": "T"},
				}},
			}},
		},
	})
	response := mustJSON(map[string]any{
		"id": "msg_r", "type": "message", "role": "assistant", "model": DefaultPublicModel,
		"content": []any{
			map[string]any{"type": "server_tool_use", "id": "ws_1", "name": "web_search", "input": map[string]any{"query": "e"}},
			map[string]any{"type": "web_search_tool_result", "tool_use_id": "ws_1", "content": []any{
				map[string]any{"type": "web_search_result", "url": tracked, "title": "T"},
			}},
		},
		"stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
	})

	normalizedRequest, normalizedResponse, stats, err := normalizeProjectionFidelity(
		request, response, fidelityNormalizationOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, int64(2), stats.AssistantToolTrackingStripped)
	require.NotContains(t, string(normalizedResponse), "utm_source=openai")
	require.NotContains(t, string(normalizedRequest), "utm_source=openai")
	require.Contains(t, string(normalizedResponse), "acc=1")
}

func TestValidateDeliveryFidelityRejectsNonAnthropicWireShape(t *testing.T) {
	base := func() *DeliveryRecord {
		record := usageTestRecord("session_shape_gate", fixedTestTime(), 100, 10, "hi")
		record.Request = json.RawMessage(`{"model":"claude-opus-5","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`)
		record.Response.ResponseData = mustJSON(map[string]any{
			"id": anthropicPublicID("msg_", "gate"), "type": "message",
			"role": "assistant", "model": DefaultPublicModel,
			"content": []any{map[string]any{
				"type": "tool_use", "id": anthropicPublicID("toolu_", "gate"),
				"name": "Read", "input": map[string]any{}, "caller": map[string]any{"type": "direct"},
			}},
			"stop_reason": "tool_use", "stop_sequence": nil, "stop_details": nil,
			"usage": map[string]any{
				"input_tokens": 2, "output_tokens": 1,
				"cache_creation_input_tokens": 10, "cache_read_input_tokens": 0,
				"cache_creation":  map[string]any{"ephemeral_5m_input_tokens": 10, "ephemeral_1h_input_tokens": 0},
				"server_tool_use": map[string]any{"web_search_requests": 0, "web_fetch_requests": 0},
				"service_tier":    "standard", "inference_geo": "global", "iterations": []any{}, "speed": "standard",
			},
		})
		return record
	}
	require.NoError(t, ValidateDeliveryFidelity(base(), DefaultPublicModel))

	for name, mutate := range map[string]struct {
		apply   func(response map[string]json.RawMessage)
		message string
	}{
		"opaque message id": {
			apply:   func(r map[string]json.RawMessage) { r["id"] = mustJSON("msg_deadbeef") },
			message: "not an Anthropic message identifier",
		},
		"openai tool id": {
			apply: func(r map[string]json.RawMessage) {
				r["content"] = mustJSON([]any{map[string]any{
					"type": "tool_use", "id": "toolu_jcsDwCBdJAPPxmkgNsnA2sIH",
					"name": "Read", "input": map[string]any{}, "caller": map[string]any{"type": "direct"},
				}})
			},
			message: "not an Anthropic tool_use identifier",
		},
		"missing caller": {
			apply: func(r map[string]json.RawMessage) {
				r["content"] = mustJSON([]any{map[string]any{
					"type": "tool_use", "id": anthropicPublicID("toolu_", "gate"),
					"name": "Read", "input": map[string]any{},
				}})
			},
			message: "caller is missing",
		},
		"missing stop details": {
			apply:   func(r map[string]json.RawMessage) { delete(r, "stop_details") },
			message: "response.stop_details is missing",
		},
	} {
		t.Run(name, func(t *testing.T) {
			record := base()
			response, err := decodeJSONObject(record.Response.ResponseData, "response")
			require.NoError(t, err)
			mutate.apply(response)
			record.Response.ResponseData = mustJSON(response)
			require.ErrorContains(t, ValidateDeliveryFidelity(record, DefaultPublicModel), mutate.message)
		})
	}
}
