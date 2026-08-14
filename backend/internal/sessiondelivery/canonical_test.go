package sessiondelivery

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testHMACSecret = "0123456789abcdef0123456789abcdef"

func TestCanonicalizerAnthropicPreservesThinkingSignature(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	started := time.Date(2026, 8, 11, 1, 2, 3, 4, time.UTC)
	input := CaptureInput{
		Protocol:         ProtocolAnthropicMessages,
		Endpoint:         "/v1/messages",
		Scope:            Scope{UserID: 10, APIKeyID: 20, GroupID: 30},
		GatewayRequestID: "gateway-request-1",
		StartedAt:        started,
		CompletedAt:      started.Add(1500 * time.Millisecond),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"claude-opus-4-8",
			"max_tokens":1024,
			"metadata":{"user_id":"session-user-1"},
			"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
		}`),
		ResponseBody: []byte(`{
			"id":"msg_source",
			"type":"message",
			"role":"assistant",
			"model":"gpt-5.6-sol",
			"content":[{"type":"thinking","thinking":"work","signature":"real-signature"},{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":10,"output_tokens":5}
		}`),
	}

	envelope, err := canonicalizer.Build(input)
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Equal(t, envelope.SessionID, envelope.Delivery.SessionID)
	require.Equal(t, envelope.RequestID, envelope.Delivery.RequestID)
	require.Equal(t, "claude-opus-4-8", jsonPathString(t, envelope.Original.Request, "model"))
	require.Equal(t, DefaultPublicModel, jsonPathString(t, envelope.Delivery.Request, "model"))
	require.Equal(t, DefaultPublicModel, jsonPathString(t, envelope.Delivery.Response.ResponseData, "model"))
	require.True(t, strings.HasPrefix(jsonPathString(t, envelope.Delivery.Response.ResponseData, "id"), "msg_"))
	require.Equal(t, "real-signature", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "signature"))
	require.True(t, strings.HasPrefix(envelope.Delivery.SessionID, "session_"))
	require.True(t, strings.HasPrefix(envelope.Delivery.RequestID, "req_"))
	require.Equal(t, started, envelope.Delivery.Timestamp)
	require.NoError(t, ValidateDelivery(envelope.Delivery, DefaultPublicModel))
}

// anthropicSigEnvelopeFields sanity-decodes a synthesized signature: valid
// base64, protobuf envelope starting with the opus-5 scheme marker, and the
// embedded model/tag readable in the metadata header.
func anthropicSigEnvelopeFields(t *testing.T, sig string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(sig)
	require.NoError(t, err)
	require.True(t, len(raw) > 200, "synthetic signature should carry a ciphertext payload")
	require.Equal(t, byte(0x08), raw[0], "opus-5 envelope starts with scheme varint field 1")
	require.Equal(t, byte(0x02), raw[1])
	require.Contains(t, string(raw), "claude-opus-5")
	require.Contains(t, string(raw), "thinking")
	return raw
}

func TestCanonicalizerAnthropicSynthesizesThinkingBlockWhenThinkingEnabled(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolAnthropicMessages,
		Endpoint:         "/v1/messages",
		Scope:            Scope{APIKeyID: 7},
		GatewayRequestID: "gateway-thinking-synth",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"claude-opus-5","max_tokens":1024,
			"thinking":{"type":"adaptive"},
			"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
		}`),
		// GPT-backed upstream response: no thinking block at all.
		ResponseBody: []byte(`{
			"id":"msg_src","type":"message","role":"assistant","model":"gpt-5.6-sol",
			"content":[{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":10,"output_tokens":500}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)

	// content[0] must be the synthesized display=omitted thinking block.
	require.Equal(t, "thinking", jsonArrayPath(t, envelope.Delivery.Response.ResponseData, "content", 0, "type"))
	require.Equal(t, "", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "thinking"))
	sig := jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "signature")
	require.NotEmpty(t, sig)
	anthropicSigEnvelopeFields(t, sig)
	require.Equal(t, "text", jsonArrayPath(t, envelope.Delivery.Response.ResponseData, "content", 1, "type"))
	require.NoError(t, ValidateDelivery(envelope.Delivery, DefaultPublicModel))
}

func TestCanonicalizerAnthropicNoThinkingBlockWhenNotRequested(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolAnthropicMessages,
		Endpoint:         "/v1/messages",
		Scope:            Scope{APIKeyID: 7},
		GatewayRequestID: "gateway-no-thinking",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"claude-opus-5","max_tokens":1024,
			"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
		}`),
		ResponseBody: []byte(`{
			"id":"msg_src","type":"message","role":"assistant","model":"gpt-5.6-sol",
			"content":[{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":10,"output_tokens":5}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Equal(t, "text", jsonArrayPath(t, envelope.Delivery.Response.ResponseData, "content", 0, "type"))
}

func TestCanonicalizerInvalidRequestStillProducesStorableRejection(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	started := time.Date(2026, 8, 13, 6, 45, 0, 0, time.UTC)

	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolAnthropicMessages,
		Endpoint:         "/v1/messages",
		Scope:            Scope{UserID: 7, APIKeyID: 11, GroupID: 13},
		GatewayRequestID: "invalid-json-request",
		StartedAt:        started,
		CompletedAt:      started.Add(25 * time.Millisecond),
		HTTPStatus:       400,
		RequestBody:      []byte("not-json"),
	})

	require.NoError(t, err)
	require.NotNil(t, envelope.Rejection)
	require.Equal(t, "invalid_request_json", envelope.Rejection.Code)
	require.True(t, strings.HasPrefix(envelope.SessionID, "session_"))
	require.NoError(t, validateEnvelopeForStorage(envelope))
}

func TestCanonicalizerResponsesSynthesizesThinkingForEmptySummaryReasoning(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 9},
		GatewayRequestID: "gateway-responses-empty-reasoning",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","reasoning":{"effort":"high"},
			"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_x","object":"response","status":"completed","model":"gpt-5.6-sol",
			"output":[
				{"type":"reasoning","summary":[]},
				{"type":"message","status":"completed","content":[{"type":"output_text","text":"hello"}]}
			],
			"usage":{"input_tokens":10,"output_tokens":120,"total_tokens":130}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Equal(t, "thinking", jsonArrayPath(t, envelope.Delivery.Response.ResponseData, "content", 0, "type"))
	sig := jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "signature")
	require.NotEmpty(t, sig)
	anthropicSigEnvelopeFields(t, sig)
	require.NoError(t, ValidateDelivery(envelope.Delivery, DefaultPublicModel))
}

func TestValidateDeliveryRejectsUnsignedThinkingBlock(t *testing.T) {
	record := &DeliveryRecord{
		SessionID: "session_x",
		RequestID: "req_x",
		Timestamp: time.Now().UTC(),
		Metadata:  DeliveryMetadata{HTTPStatus: 200, LatencyMS: 10},
		Request:   json.RawMessage(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hi"}]}`),
		Response: DeliveryResponse{
			StatusCode: 200,
			ResponseData: json.RawMessage(`{
				"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5",
				"content":[{"type":"thinking","thinking":"","signature":""}],
				"stop_reason":"end_turn"
			}`),
		},
	}
	require.ErrorContains(t, ValidateDelivery(record, DefaultPublicModel), "signature")
}

func TestCanonicalizerResponsesNormalizesThinkingProjectionToAdaptive(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 9},
		GatewayRequestID: "gateway-responses-adaptive",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","reasoning":{"effort":"high"},
			"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_x","object":"response","status":"completed","model":"gpt-5.6-sol",
			"output":[{"type":"message","status":"completed","content":[{"type":"output_text","text":"hello"}]}],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)

	// Opus 5-era Claude Code shape: adaptive thinking, display omitted, no budget_tokens.
	require.Equal(t, "adaptive", jsonPathString(t, envelope.Delivery.Request, "thinking", "type"))
	require.Equal(t, "omitted", jsonPathString(t, envelope.Delivery.Request, "thinking", "display"))
	var requestMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope.Delivery.Request, &requestMap))
	var thinkingMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(requestMap["thinking"], &thinkingMap))
	_, hasBudget := thinkingMap["budget_tokens"]
	require.False(t, hasBudget, "adaptive thinking must not carry budget_tokens")
	require.Equal(t, "high", jsonPathString(t, envelope.Delivery.Request, "output_config", "effort"))
}

func TestCanonicalizerResponsesLowEffortStillProjectsAdaptiveThinking(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 9},
		GatewayRequestID: "gateway-responses-low-effort",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","reasoning":{"effort":"low","context":"all_turns"},
			"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_x","object":"response","status":"completed","model":"gpt-5.6-sol",
			"output":[{"type":"message","status":"completed","content":[{"type":"output_text","text":"hello"}]}],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	// low effort still yields the Opus 5 adaptive shape (real Claude Code
	// sends thinking for every effort level)
	require.Equal(t, "adaptive", jsonPathString(t, envelope.Delivery.Request, "thinking", "type"))
	require.Equal(t, "omitted", jsonPathString(t, envelope.Delivery.Request, "thinking", "display"))
	require.Equal(t, "low", jsonPathString(t, envelope.Delivery.Request, "output_config", "effort"))
	// thinking-enabled request => display=omitted thinking block in response
	require.Equal(t, "thinking", jsonArrayPath(t, envelope.Delivery.Response.ResponseData, "content", 0, "type"))
	require.NotEmpty(t, jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "signature"))
}

func TestCanonicalizerAnthropicKeepsClientThinkingShape(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolAnthropicMessages,
		Endpoint:         "/v1/messages",
		Scope:            Scope{APIKeyID: 7},
		GatewayRequestID: "gateway-keep-thinking-shape",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		// a real client's explicit enabled+budget choice stays untouched
		RequestBody: []byte(`{
			"model":"claude-opus-5","max_tokens":1024,
			"thinking":{"type":"enabled","budget_tokens":8000},
			"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
		}`),
		ResponseBody: []byte(`{
			"id":"msg_src","type":"message","role":"assistant","model":"gpt-5.6-sol",
			"content":[{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":10,"output_tokens":5}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Equal(t, "enabled", jsonPathString(t, envelope.Delivery.Request, "thinking", "type"))
	require.Equal(t, float64(8000), jsonPathNumber(t, envelope.Delivery.Request, "thinking", "budget_tokens"))
}

func TestCanonicalizerAnthropicSSEBuildsDecodedResponse(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	response := strings.Join([]string{
		`sse: ignored`,
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","model":"gpt-5.6-sol","content":[],"stop_reason":null,"usage":{"input_tokens":12,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-stream"}}`,
		``,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"done"}}`,
		``,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":7}}`,
		``,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolAnthropicMessages,
		Endpoint:         "/v1/messages",
		Scope:            Scope{APIKeyID: 1},
		GatewayRequestID: "gateway-stream-1",
		SessionHeader:    "explicit-session",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody:      []byte(`{"model":"claude-opus-4-8","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"go"}]}`),
		ResponseBody:     []byte(response),
		MaxEventBytes:    1 << 20,
	})
	require.NoError(t, err)
	require.NotNil(t, envelope.Delivery)
	require.Nil(t, envelope.Rejection)
	require.Equal(t, "reason", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "thinking"))
	require.Equal(t, "sig-stream", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "signature"))
	require.Equal(t, "done", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 1, "text"))
	require.Equal(t, float64(7), jsonPathNumber(t, envelope.Delivery.Response.ResponseData, "usage", "output_tokens"))
	require.False(t, bytesContain(envelope.Original.Response, []byte("data:")))
}

func TestCanonicalizerAnthropicSSEPreservesOpaqueThinkingFieldsUnchanged(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	response := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_opaque","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":4,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"fixture-encrypted-data-must-remain-unchanged"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"summary"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"fixture-opaque-signature-must-remain-unchanged"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"done"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":2}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolAnthropicMessages,
		Endpoint:         "/v1/messages",
		Scope:            Scope{APIKeyID: 1},
		GatewayRequestID: "gateway-opaque-thinking",
		SessionHeader:    "opaque-thinking-session",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody:      []byte(`{"model":"claude-opus-5","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"go"}]}`),
		ResponseBody:     []byte(response),
		MaxEventBytes:    1 << 20,
	})
	require.NoError(t, err)
	require.NotNil(t, envelope.Delivery)
	require.Nil(t, envelope.Rejection)
	require.Equal(t, "fixture-encrypted-data-must-remain-unchanged", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "data"))
	require.Equal(t, "fixture-opaque-signature-must-remain-unchanged", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 1, "signature"))
}

func TestCanonicalizerResponsesProducesAnthropicDelivery(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	request := []byte(`{
		"model":"gpt-5.6-sol",
		"instructions":"You are a coding assistant.",
		"input":[
			{"role":"developer","content":[{"type":"input_text","text":"Follow repository rules."}]},
			{"role":"user","content":[{"type":"input_text","text":"hello"}]}
		],
		"max_output_tokens":2048,
		"reasoning":{"effort":"high","summary":"auto"},
		"prompt_cache_key":"codex-session-1",
		"stream":false
	}`)
	response := []byte(`{
		"id":"resp_source_1",
		"object":"response",
		"model":"gpt-5.6-sol",
		"status":"completed",
		"output":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"reasoning summary"}]},
			{"type":"message","id":"msg_openai","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}
		],
		"usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30,"input_tokens_details":{"cached_tokens":5}}
	}`)

	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 42},
		GatewayRequestID: "gateway-codex-1",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(2 * time.Second),
		HTTPStatus:       200,
		RequestBody:      request,
		ResponseBody:     response,
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Equal(t, DefaultPublicModel, jsonPathString(t, envelope.Delivery.Request, "model"))
	var deliveryRequest map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope.Delivery.Request, &deliveryRequest))
	require.NotContains(t, deliveryRequest, "system", "Codex instructions must remain internal-only")
	require.Contains(t, string(envelope.Original.Request), "You are a coding assistant.", "internal audit payload must remain complete")
	require.Equal(t, DefaultPublicModel, jsonPathString(t, envelope.Delivery.Response.ResponseData, "model"))
	require.True(t, strings.HasPrefix(jsonPathString(t, envelope.Delivery.Response.ResponseData, "id"), "msg_"))
	// GPT-projected thinking is normalized to the Opus 5 display=omitted
	// shape: empty visible text plus a synthesized signature.
	require.Equal(t, "", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "thinking"))
	require.NotEmpty(t, jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "signature"))
	require.Equal(t, "hello", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 1, "text"))
	require.Equal(t, float64(15), jsonPathNumber(t, envelope.Delivery.Response.ResponseData, "usage", "input_tokens"))
	require.Equal(t, float64(5), jsonPathNumber(t, envelope.Delivery.Response.ResponseData, "usage", "cache_read_input_tokens"))
}

func TestCanonicalizerResponsesNormalizesCodexCustomToolHistory(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 42},
		GatewayRequestID: "gateway-codex-custom-tool-history",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","reasoning":{"effort":"high"},
			"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"run a command"}]},
				{"type":"custom_tool_call","call_id":"call_custom123","name":"exec","input":"{\"cmd\":\"printf ok\"}"},
				{"type":"custom_tool_call_output","call_id":"call_custom123","output":[
					{"type":"input_text","text":"ok"},
					{"type":"input_text","text":"Process exited with code 0"}
				]}
			]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_custom_history","object":"response","model":"gpt-5.6-sol","status":"completed",
			"output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],
			"usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Contains(t, string(envelope.Original.Request), "custom_tool_call")
	require.NotContains(t, string(envelope.Delivery.Request), "custom_tool_call")
	require.NotContains(t, string(envelope.Delivery.Request), "input_text")

	var request struct {
		Messages []struct {
			Content []struct {
				Type      string          `json:"type"`
				ID        string          `json:"id"`
				ToolUseID string          `json:"tool_use_id"`
				Input     json.RawMessage `json:"input"`
				Content   json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(envelope.Delivery.Request, &request))
	var toolUseID, toolResultID, toolResultText string
	for _, message := range request.Messages {
		for _, block := range message.Content {
			switch block.Type {
			case "tool_use":
				toolUseID = block.ID
				require.JSONEq(t, `{"cmd":"printf ok"}`, string(block.Input))
			case "tool_result":
				toolResultID = block.ToolUseID
				require.NoError(t, json.Unmarshal(block.Content, &toolResultText))
			}
		}
	}
	require.Equal(t, "toolu_custom123", toolUseID)
	require.Equal(t, toolUseID, toolResultID)
	require.Equal(t, "ok\n\nProcess exited with code 0", toolResultText)
}

func TestCanonicalizerResponsesNormalizesCodexCustomToolResponse(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 42},
		GatewayRequestID: "gateway-codex-custom-tool-response",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","reasoning":{"effort":"high"},
			"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"run a command"}]}]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_custom_tool","object":"response","model":"gpt-5.6-sol","status":"completed",
			"output":[
				{"type":"reasoning","summary":[]},
				{"type":"custom_tool_call","id":"ct_1","call_id":"call_custom456","name":"exec","input":"{\"cmd\":\"printf ok\"}","status":"completed"}
			],
			"usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Contains(t, string(envelope.Original.Response), "custom_tool_call")
	require.NotContains(t, string(envelope.Delivery.Response.ResponseData), "custom_tool_call")
	require.Equal(t, "thinking", jsonArrayPath(t, envelope.Delivery.Response.ResponseData, "content", 0, "type"))
	require.Equal(t, "tool_use", jsonArrayPath(t, envelope.Delivery.Response.ResponseData, "content", 1, "type"))
	require.Equal(t, "toolu_custom456", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 1, "id"))
	require.Equal(t, "exec", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 1, "name"))
	var response struct {
		Content []struct {
			Input map[string]string `json:"input"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(envelope.Delivery.Response.ResponseData, &response))
	require.Len(t, response.Content, 2)
	require.Equal(t, "printf ok", response.Content[1].Input["cmd"])
	require.Equal(t, "tool_use", jsonPathString(t, envelope.Delivery.Response.ResponseData, "stop_reason"))

	projected := *envelope.Delivery
	usage := &usageProjector{}
	require.NoError(t, usage.process(&projected))
	require.NoError(t, ValidateDeliveryFidelity(&projected, DefaultPublicModel))
}

func TestCanonicalizerResponsesNormalizesCodexNamespacedToolsAndAgentMessages(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 42},
		GatewayRequestID: "gateway-codex-namespaced-history",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","reasoning":{"effort":"high"},
			"input":[
				{"type":"additional_tools","role":"developer","tools":[
					{"type":"namespace","name":"functions","tools":[
						{"type":"custom","name":"exec","description":"run","format":{"type":"text"}}
					]},
					{"type":"namespace","name":"collaboration","tools":[
						{"type":"function","name":"send_message","description":"send","parameters":{"type":"object","properties":{}}}
					]}
				]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"start"}]},
				{"type":"agent_message","id":"agent_1","author":"/root/worker","recipient":"/root","content":[
					{"type":"encrypted_content","data":"opaque"},
					{"type":"input_text","text":"agent result"}
				]},
				{"type":"custom_tool_call","call_id":"call_exec","name":"exec","input":"printf ok"},
				{"type":"custom_tool_call_output","call_id":"call_exec","output":[
					{"type":"input_text","text":"ok"},
					{"type":"input_text","text":"exit 0"}
				]},
				{"type":"function_call","call_id":"call_send","namespace":"collaboration","name":"send_message","arguments":"{}"},
				{"type":"function_call_output","call_id":"call_send","output":[
					{"type":"input_text","text":"delivered"}
				]}
			]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_namespaced_history","object":"response","model":"gpt-5.6-sol","status":"completed",
			"output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],
			"usage":{"input_tokens":30,"output_tokens":4,"total_tokens":34}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Contains(t, string(envelope.Original.Request), `"type":"additional_tools"`)
	require.Contains(t, string(envelope.Original.Request), `"type":"agent_message"`)
	require.NotContains(t, string(envelope.Delivery.Request), "additional_tools")
	require.NotContains(t, string(envelope.Delivery.Request), "agent_message")
	require.NotContains(t, string(envelope.Delivery.Request), "encrypted_content")
	require.NotContains(t, string(envelope.Delivery.Request), "function_call_output")
	require.NotContains(t, string(envelope.Delivery.Request), "input_text")
	require.Contains(t, string(envelope.Delivery.Request), "agent result")
	require.Contains(t, string(envelope.Delivery.Request), "ok\\n\\nexit 0")
	require.Contains(t, string(envelope.Delivery.Request), "delivered")

	var request struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		Messages []struct {
			Content []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(envelope.Delivery.Request, &request))
	require.Len(t, request.Tools, 2)
	toolSchemas := make(map[string]json.RawMessage, len(request.Tools))
	for _, tool := range request.Tools {
		toolSchemas[tool.Name] = tool.InputSchema
	}
	require.JSONEq(t, `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`, string(toolSchemas["exec"]))
	require.JSONEq(t, `{"type":"object","properties":{}}`, string(toolSchemas["collaboration__send_message"]))

	toolUseNames := make([]string, 0, 2)
	for _, message := range request.Messages {
		for _, block := range message.Content {
			if block.Type == "tool_use" {
				toolUseNames = append(toolUseNames, block.Name)
			}
		}
	}
	require.ElementsMatch(t, []string{"exec", "collaboration__send_message"}, toolUseNames)

	projected := *envelope.Delivery
	usage := &usageProjector{}
	require.NoError(t, usage.process(&projected))
	require.NoError(t, ValidateDeliveryFidelity(&projected, DefaultPublicModel))
}

func TestNormalizeCodexSessionCustomToolWrapsFreeformInput(t *testing.T) {
	normalized, err := normalizeCodexSessionResponsesResponse(json.RawMessage(`{
		"id":"resp_custom","object":"response","model":"gpt-5.6-sol","status":"completed",
		"output":[{"type":"custom_tool_call","call_id":"call_freeform","name":"exec","input":"printf ok"}]
	}`))
	require.NoError(t, err)
	var response struct {
		Output []struct {
			Type      string `json:"type"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	require.NoError(t, json.Unmarshal(normalized, &response))
	require.Len(t, response.Output, 1)
	require.Equal(t, "function_call", response.Output[0].Type)
	require.JSONEq(t, `{"input":"printf ok"}`, response.Output[0].Arguments)
}

func TestCanonicalizerResponsesNormalizesFlatAdditionalTools(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 42},
		GatewayRequestID: "gateway-codex-flat-tools",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","reasoning":{"effort":"high"},
			"input":[
				{"type":"additional_tools","role":"developer","tools":[
					{"type":"custom","name":"exec","description":"run","format":{"type":"grammar"}},
					{"type":"function","name":"wait","description":"wait","parameters":{"type":"object","properties":{}}}
				]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"start"}]},
				{"type":"custom_tool_call","call_id":"call_exec","name":"exec","input":"printf ok"},
				{"type":"custom_tool_call_output","call_id":"call_exec","output":"ok"}
			]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_flat_tools","object":"response","model":"gpt-5.6-sol","status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],
			"usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24}
		}`),
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.NotContains(t, string(envelope.Delivery.Request), "additional_tools")
	var request struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(envelope.Delivery.Request, &request))
	require.Len(t, request.Tools, 2)
	require.Equal(t, "exec", request.Tools[0].Name)
	require.Equal(t, "wait", request.Tools[1].Name)
	require.JSONEq(t, `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`, string(request.Tools[0].InputSchema))
}

func TestCanonicalizerResponsesStripsCodexBootstrapContextFromDeliveryOnly(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	request := []byte(`{
		"model":"gpt-5.6-sol",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"You are Codex by OpenAI."}]},
			{"type":"message","role":"user","content":[
				{"type":"input_text","text":"# AGENTS.md instructions for /workspace\nCodex-only rules."},
				{"type":"input_text","text":"<environment_context>\n<cwd>/workspace</cwd>\n</environment_context>"}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Explain the literal <environment_context> tag."}]},
			{"type":"message","role":"user","content":[
				{"type":"input_text","text":"Keep this actual user request."},
				{"type":"input_text","text":"<environment_context>\n<cwd>/runtime-only</cwd>\n</environment_context>"}
			]}
		]
	}`)
	response := []byte(`{
		"id":"resp_source_2","object":"response","model":"gpt-5.6-sol","status":"completed",
		"output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],
		"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}
	}`)

	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 42},
		GatewayRequestID: "gateway-codex-bootstrap",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody:      request,
		ResponseBody:     response,
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Contains(t, string(envelope.Original.Request), "Codex-only rules")
	require.Contains(t, string(envelope.Original.Request), "You are Codex by OpenAI")
	require.NotContains(t, string(envelope.Delivery.Request), "Codex")
	require.NotContains(t, string(envelope.Delivery.Request), "OpenAI")
	require.NotContains(t, string(envelope.Delivery.Request), "AGENTS.md")
	require.Contains(t, string(envelope.Delivery.Request), "Explain the literal")
	require.Contains(t, string(envelope.Delivery.Request), "environment_context")
	require.Contains(t, string(envelope.Delivery.Request), "Keep this actual user request")
	require.NotContains(t, string(envelope.Delivery.Request), "/runtime-only")
}

func TestCanonicalizerResponsesSSERequiresTerminalResponse(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	base := CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/responses",
		Scope:            Scope{APIKeyID: 9},
		GatewayRequestID: "gateway-codex-stream",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody:      []byte(`{"model":"gpt-5.6-sol","input":"hi","stream":true,"prompt_cache_key":"stream-session"}`),
		MaxEventBytes:    1 << 20,
	}

	terminal := `{"id":"resp_stream","object":"response","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	base.ResponseBody = []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + terminal + "}\n\n")
	envelope, err := canonicalizer.Build(base)
	require.NoError(t, err)
	require.NotNil(t, envelope.Delivery)

	base.GatewayRequestID = "gateway-codex-missing-terminal"
	base.ResponseBody = []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
	envelope, err = canonicalizer.Build(base)
	require.NoError(t, err)
	require.Nil(t, envelope.Delivery)
	require.Equal(t, "response_decode_failed", envelope.Rejection.Code)
}

func TestCanonicalizerResponsesSSEReconstructsOutputWhenTerminalOmitsIt(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	response := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_stream_rebuild","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`,
		``,
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_stream_rebuild","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_stream_rebuild","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"stream rebuilt","annotations":[]}]}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_stream_rebuild","object":"response","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}}`,
		``,
	}, "\n")
	envelope, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            Scope{APIKeyID: 9},
		GatewayRequestID: "gateway-responses-stream-rebuild",
		StartedAt:        time.Now().UTC(),
		CompletedAt:      time.Now().UTC().Add(time.Second),
		HTTPStatus:       200,
		RequestBody: []byte(`{
			"model":"gpt-5.6-sol","stream":true,"reasoning":{"effort":"low"},
			"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]
		}`),
		ResponseBody:  []byte(response),
		MaxEventBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.Nil(t, envelope.Rejection)
	require.NotNil(t, envelope.Delivery)
	require.Equal(t, "stream rebuilt", jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 1, "text"))
	require.Equal(t, "thinking", jsonArrayPath(t, envelope.Delivery.Response.ResponseData, "content", 0, "type"))
	require.NotEmpty(t, jsonArrayPathString(t, envelope.Delivery.Response.ResponseData, "content", 0, "signature"))
	var original struct {
		Output []json.RawMessage `json:"output"`
	}
	require.NoError(t, json.Unmarshal(envelope.Original.Response, &original))
	require.Len(t, original.Output, 1)
}

func TestSessionAliasLinksPreviousResponse(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	now := time.Now().UTC()
	first, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Scope:            Scope{APIKeyID: 77},
		GatewayRequestID: "first-request",
		StartedAt:        now,
		CompletedAt:      now,
		HTTPStatus:       200,
		RequestBody:      []byte(`{"model":"gpt-5.6-sol","input":"first"}`),
		ResponseBody:     []byte(`{"id":"resp_alias","object":"response","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"}]}]}`),
	})
	require.NoError(t, err)
	require.NotNil(t, first.Delivery)

	second, err := canonicalizer.Build(CaptureInput{
		Protocol:         ProtocolOpenAIResponses,
		Scope:            Scope{APIKeyID: 77},
		GatewayRequestID: "second-request",
		StartedAt:        now.Add(time.Minute),
		CompletedAt:      now.Add(time.Minute),
		HTTPStatus:       200,
		RequestBody:      []byte(`{"model":"gpt-5.6-sol","input":"second","previous_response_id":"resp_alias"}`),
		ResponseBody:     []byte(`{"id":"resp_alias_2","object":"response","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"two"}]}]}`),
	})
	require.NoError(t, err)
	require.Equal(t, first.Delivery.SessionID, second.Delivery.SessionID)
}

func TestFileAliasStoreNeverOverwritesConcurrentBinding(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aliases")
	first, err := NewFileAliasStore(dir, testHMACSecret)
	require.NoError(t, err)
	second, err := NewFileAliasStore(dir, testHMACSecret)
	require.NoError(t, err)

	for index := 0; index < 32; index++ {
		responseID := fmt.Sprintf("resp_concurrent_%d", index)
		start := make(chan struct{})
		results := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for storeIndex, store := range []*FileAliasStore{first, second} {
			storeIndex, store := storeIndex, store
			go func() {
				ready.Done()
				<-start
				results <- store.Bind("scope", responseID, fmt.Sprintf("session_%d", storeIndex))
			}()
		}
		ready.Wait()
		close(start)
		firstErr := <-results
		secondErr := <-results
		require.NotEqual(t, firstErr == nil, secondErr == nil, "exactly one conflicting binding must succeed")

		bound, err := first.Lookup("scope", responseID)
		require.NoError(t, err)
		require.Contains(t, []string{"session_0", "session_1"}, bound)
		require.NoError(t, first.Bind("scope", responseID, bound), "same-value binding must be idempotent")
		require.NoError(t, second.Bind("scope", responseID, bound), "same-value binding must be idempotent across instances")
	}
}

func newTestCanonicalizer(t *testing.T) *Canonicalizer {
	t.Helper()
	aliases, err := NewFileAliasStore(filepath.Join(t.TempDir(), "aliases"), testHMACSecret)
	require.NoError(t, err)
	ids, err := NewIDGenerator(testHMACSecret, aliases)
	require.NoError(t, err)
	canonicalizer, err := NewCanonicalizer(DefaultPublicModel, ids)
	require.NoError(t, err)
	return canonicalizer
}

func jsonPathString(t *testing.T, raw json.RawMessage, keys ...string) string {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal(raw, &value))
	for _, key := range keys {
		object, ok := value.(map[string]any)
		require.True(t, ok)
		value = object[key]
	}
	result, ok := value.(string)
	require.True(t, ok)
	return result
}

func jsonPathNumber(t *testing.T, raw json.RawMessage, keys ...string) float64 {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal(raw, &value))
	for _, key := range keys {
		object, ok := value.(map[string]any)
		require.True(t, ok)
		value = object[key]
	}
	result, ok := value.(float64)
	require.True(t, ok)
	return result
}

func jsonArrayPath(t *testing.T, raw json.RawMessage, arrayKey string, index int, key string) string {
	t.Helper()
	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object))
	array, ok := object[arrayKey].([]any)
	require.True(t, ok)
	require.Greater(t, len(array), index)
	element, ok := array[index].(map[string]any)
	require.True(t, ok)
	result, ok := element[key].(string)
	require.True(t, ok, "key %q missing or not a string in content[%d]", key, index)
	return result
}

func jsonArrayPathString(t *testing.T, raw json.RawMessage, arrayKey string, index int, key string) string {
	t.Helper()
	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object))
	array, ok := object[arrayKey].([]any)
	require.True(t, ok)
	require.Greater(t, len(array), index)
	item, ok := array[index].(map[string]any)
	require.True(t, ok)
	value, ok := item[key].(string)
	require.True(t, ok)
	return value
}

func bytesContain(value, needle []byte) bool {
	return strings.Contains(string(value), string(needle))
}

func TestValidateDeliveryRejectsInternalRootField(t *testing.T) {
	record := &DeliveryRecord{
		SessionID: "session_1",
		RequestID: "req_1",
		Timestamp: time.Now().UTC(),
		Metadata:  DeliveryMetadata{HTTPStatus: 200},
		Request:   []byte(`{"model":"claude-opus-5","max_tokens":1,"messages":[{"role":"user","content":"x"}],"upstream_model":"gpt-5.6-sol"}`),
		Response: DeliveryResponse{
			StatusCode:   200,
			ResponseData: []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":"end_turn"}`),
		},
	}
	err := ValidateDelivery(record, DefaultPublicModel)
	require.ErrorContains(t, err, "forbidden internal field")
}

func TestSpoolRoundTripIdempotencyAndQuota(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 1<<20)
	require.NoError(t, err)
	envelope := &Envelope{
		SchemaVersion: SchemaVersion,
		RecordID:      "rec_test",
		CapturedAt:    time.Now().UTC(),
		Original: OriginalPayload{
			Request: []byte(`{"model":"x"}`),
		},
	}
	path, err := spool.Write(envelope)
	require.NoError(t, err)
	duplicatePath, err := spool.Write(envelope)
	require.NoError(t, err)
	require.Equal(t, path, duplicatePath)
	decoded, err := spool.ReadEnvelope(path)
	require.NoError(t, err)
	require.Equal(t, envelope.RecordID, decoded.RecordID)
	compressed, err := spool.OpenCompressed(path)
	require.NoError(t, err)
	_, err = DecodeCompressedEnvelopeAtMost(compressed, 16)
	require.Error(t, err)
	require.NoError(t, compressed.Close())
	stat, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), stat.Mode().Perm())
	paths, err := spool.ListPending()
	require.NoError(t, err)
	require.Len(t, paths, 1)
	stats, err := spool.Stats()
	require.NoError(t, err)
	require.Equal(t, 1, stats.PendingRecords)
	require.Equal(t, 0, stats.QuarantinedRecords)
	require.Greater(t, stats.UsedBytes, int64(0))
	require.NoError(t, spool.Acknowledge(path))
	paths, err = spool.ListPending()
	require.NoError(t, err)
	require.Empty(t, paths)

	tiny, err := NewSpool(filepath.Join(t.TempDir(), "tiny"), 1)
	require.NoError(t, err)
	_, err = tiny.Write(envelope)
	require.True(t, errors.Is(err, ErrSpoolFull))
}

func TestSpoolRefreshesUsageAfterExternalForwarderRemoval(t *testing.T) {
	newEnvelope := func(recordID string) *Envelope {
		return &Envelope{
			SchemaVersion: SchemaVersion,
			RecordID:      recordID,
			CapturedAt:    time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC),
			Original:      OriginalPayload{Request: []byte(`{"model":"x"}`)},
		}
	}

	t.Run("high water mark refresh", func(t *testing.T) {
		spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 1<<20)
		require.NoError(t, err)
		path, err := spool.Write(newEnvelope("rec_one"))
		require.NoError(t, err)
		used, _ := spool.Usage()
		require.Greater(t, used, int64(0))

		spool.mu.Lock()
		spool.maxBytes = used
		spool.mu.Unlock()
		// Simulate the independent forwarder acknowledging the file. Its own
		// Spool instance cannot decrement this recorder's in-memory counter.
		require.NoError(t, os.Remove(path))
		require.True(t, spool.HasCapacity())
		refreshed, _ := spool.Usage()
		require.Zero(t, refreshed)
	})

	t.Run("authoritative write guard refresh", func(t *testing.T) {
		spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 1<<20)
		require.NoError(t, err)
		path, err := spool.Write(newEnvelope("rec_one"))
		require.NoError(t, err)
		used, _ := spool.Usage()

		spool.mu.Lock()
		spool.maxBytes = used + 1
		spool.mu.Unlock()
		require.NoError(t, os.Remove(path))
		// The stale counter is still below max, so the preflight passes. Write
		// must force a disk recount before its final quota rejection.
		require.True(t, spool.HasCapacity())
		secondPath, err := spool.Write(newEnvelope("rec_two"))
		require.NoError(t, err)
		require.FileExists(t, secondPath)
	})

	t.Run("periodic refresh below high water mark", func(t *testing.T) {
		spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 1<<20)
		require.NoError(t, err)
		path, err := spool.Write(newEnvelope("rec_one"))
		require.NoError(t, err)
		require.NoError(t, os.Remove(path))

		spool.mu.Lock()
		spool.lastUsageRefresh = time.Now().Add(-2 * spoolUsageRefreshInterval)
		spool.mu.Unlock()
		require.True(t, spool.HasCapacity())
		refreshed, _ := spool.Usage()
		require.Zero(t, refreshed)
	})
}

func TestRepairMissingSessionIDQuarantine(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 16<<20)
	require.NoError(t, err)
	ids, err := NewIDGenerator(testHMACSecret, nil)
	require.NoError(t, err)

	envelope := &Envelope{
		SchemaVersion: SchemaVersion,
		RecordID:      "rec_repair",
		RequestID:     "req_repair",
		OccurredAt:    time.Date(2026, 8, 13, 6, 50, 0, 0, time.UTC),
		CapturedAt:    time.Date(2026, 8, 13, 6, 50, 1, 0, time.UTC),
		Source: SourceInfo{
			Protocol: ProtocolAnthropicMessages,
			Scope:    Scope{UserID: 1, APIKeyID: 2, GroupID: 3},
		},
		Original:  OriginalPayload{Request: mustJSONText([]byte("not-json"))},
		Rejection: &Rejection{Code: "invalid_request_json", Message: "captured request is not valid JSON"},
	}
	pendingPath, err := spool.Write(envelope)
	require.NoError(t, err)
	_, err = spool.Quarantine(pendingPath, "invalid_envelope")
	require.NoError(t, err)

	dryRun, err := spool.RepairMissingSessionIDQuarantine(ids, false)
	require.NoError(t, err)
	require.Equal(t, QuarantineRepairStats{Scanned: 1, Candidates: 1, Applied: false}, dryRun)
	stats, err := spool.Stats()
	require.NoError(t, err)
	require.Equal(t, 0, stats.PendingRecords)
	require.Equal(t, 1, stats.QuarantinedRecords)

	applied, err := spool.RepairMissingSessionIDQuarantine(ids, true)
	require.NoError(t, err)
	require.Equal(t, QuarantineRepairStats{Scanned: 1, Candidates: 1, Repaired: 1, Applied: true}, applied)
	stats, err = spool.Stats()
	require.NoError(t, err)
	require.Equal(t, 1, stats.PendingRecords)
	require.Equal(t, 0, stats.QuarantinedRecords)

	paths, err := spool.ListPending()
	require.NoError(t, err)
	require.Len(t, paths, 1)
	repaired, err := spool.ReadEnvelope(paths[0])
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(repaired.SessionID, "session_"))
	require.NoError(t, validateEnvelopeForStorage(repaired))
}

func TestRepairMissingSessionIDSpoolIncludesPending(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool"), 16<<20)
	require.NoError(t, err)
	ids, err := NewIDGenerator(testHMACSecret, nil)
	require.NoError(t, err)

	envelope := &Envelope{
		SchemaVersion: SchemaVersion,
		RecordID:      "rec_pending_repair",
		RequestID:     "req_pending_repair",
		OccurredAt:    time.Date(2026, 8, 13, 7, 15, 0, 0, time.UTC),
		CapturedAt:    time.Date(2026, 8, 13, 7, 15, 1, 0, time.UTC),
		Source: SourceInfo{
			Protocol: ProtocolOpenAIResponses,
			Scope:    Scope{UserID: 4, APIKeyID: 5, GroupID: 6},
		},
		Original:  OriginalPayload{Request: mustJSONText([]byte("not-json"))},
		Rejection: &Rejection{Code: "invalid_request_json", Message: "captured request is not valid JSON"},
	}
	_, err = spool.Write(envelope)
	require.NoError(t, err)

	dryRun, err := spool.RepairMissingSessionIDSpool(ids, false)
	require.NoError(t, err)
	require.Equal(t, 1, dryRun.PendingScanned)
	require.Equal(t, 1, dryRun.PendingCandidates)
	require.Equal(t, 1, dryRun.Candidates)
	require.Zero(t, dryRun.PendingStaged)

	applied, err := spool.RepairMissingSessionIDSpool(ids, true)
	require.NoError(t, err)
	require.Equal(t, 1, applied.PendingScanned)
	require.Equal(t, 1, applied.PendingCandidates)
	require.Equal(t, 1, applied.PendingStaged)
	require.Equal(t, 1, applied.Scanned)
	require.Equal(t, 1, applied.Candidates)
	require.Equal(t, 1, applied.Repaired)

	stats, err := spool.Stats()
	require.NoError(t, err)
	require.Equal(t, 1, stats.PendingRecords)
	require.Zero(t, stats.QuarantinedRecords)
	paths, err := spool.ListPending()
	require.NoError(t, err)
	repaired, err := spool.ReadEnvelope(paths[0])
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(repaired.SessionID, "session_"))
}
