package sessiondelivery

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecanonicalizeRejectedEnvelopePreservesStableAuditFields(t *testing.T) {
	canonicalizer := newTestCanonicalizer(t)
	started := time.Date(2026, 8, 14, 4, 5, 6, 789000000, time.UTC)
	request := []byte(`{
		"model":"gpt-5.6-sol","reasoning":{"effort":"high"},
		"input":[
			{"type":"additional_tools","role":"developer","tools":[
				{"type":"namespace","name":"collaboration","tools":[
					{"type":"function","name":"send_message","parameters":{"type":"object","properties":{}}}
				]}
			]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"start"}]},
			{"type":"agent_message","author":"/root/worker","recipient":"/root","content":[
				{"type":"encrypted_content","data":"opaque"},
				{"type":"input_text","text":"agent result"}
			]},
			{"type":"function_call","call_id":"call_send","namespace":"collaboration","name":"send_message","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_send","output":[{"type":"input_text","text":"delivered"}]}
		]
	}`)
	response := []byte(`{
		"id":"resp_repair","object":"response","model":"gpt-5.6-sol","status":"completed",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],
		"usage":{"input_tokens":30,"output_tokens":4,"total_tokens":34}
	}`)
	envelope := &Envelope{
		SchemaVersion:    SchemaVersion,
		RecordID:         "rec_stable",
		SessionID:        "session_stable",
		RequestID:        "req_stable",
		OccurredAt:       started,
		CapturedAt:       started.Add(2345 * time.Millisecond),
		GatewayRequestID: "gateway-stable",
		Source: SourceInfo{
			Protocol: ProtocolOpenAIResponses,
			Endpoint: "/v1/responses",
			Scope:    Scope{UserID: 1, APIKeyID: 2, GroupID: 3},
			Stream:   true,
		},
		HTTPStatus: 200,
		DurationMS: 2345,
		Original: OriginalPayload{
			Request:  append([]byte(nil), request...),
			Response: append([]byte(nil), response...),
		},
		Rejection: &Rejection{Code: "request_conversion_failed", Message: "legacy output type"},
	}

	repaired, err := recanonicalizeRejectedEnvelope(canonicalizer, envelope)
	require.NoError(t, err)
	require.Nil(t, repaired.Rejection)
	require.NotNil(t, repaired.Delivery)
	require.Equal(t, envelope.RecordID, repaired.RecordID)
	require.Equal(t, envelope.SessionID, repaired.SessionID)
	require.Equal(t, envelope.RequestID, repaired.RequestID)
	require.Equal(t, envelope.OccurredAt, repaired.OccurredAt)
	require.Equal(t, envelope.CapturedAt, repaired.CapturedAt)
	require.Equal(t, envelope.GatewayRequestID, repaired.GatewayRequestID)
	require.Equal(t, envelope.Source, repaired.Source)
	require.Equal(t, envelope.DurationMS, repaired.DurationMS)
	require.True(t, bytes.Equal(request, repaired.Original.Request))
	require.True(t, bytes.Equal(response, repaired.Original.Response))
	require.Equal(t, envelope.SessionID, repaired.Delivery.SessionID)
	require.Equal(t, envelope.RequestID, repaired.Delivery.RequestID)
	require.Equal(t, envelope.OccurredAt, repaired.Delivery.Timestamp)
	require.NotContains(t, string(repaired.Delivery.Request), "additional_tools")
	require.NotContains(t, string(repaired.Delivery.Request), "agent_message")
	require.NotContains(t, string(repaired.Delivery.Request), "function_call_output")
	require.Contains(t, string(repaired.Delivery.Request), "collaboration__send_message")

	projected := *repaired.Delivery
	usage := &usageProjector{}
	require.NoError(t, usage.process(&projected))
	require.NoError(t, ValidateDeliveryFidelity(&projected, DefaultPublicModel))
}
