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
