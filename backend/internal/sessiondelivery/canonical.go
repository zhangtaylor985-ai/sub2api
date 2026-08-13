package sessiondelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

type Canonicalizer struct {
	publicModel string
	ids         *IDGenerator
}

func NewCanonicalizer(publicModel string, ids *IDGenerator) (*Canonicalizer, error) {
	publicModel = strings.TrimSpace(publicModel)
	if publicModel == "" {
		publicModel = DefaultPublicModel
	}
	if ids == nil {
		return nil, errors.New("session delivery ID generator is required")
	}
	return &Canonicalizer{publicModel: publicModel, ids: ids}, nil
}

func (c *Canonicalizer) Build(input CaptureInput) (*Envelope, error) {
	startedAt := input.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	completedAt := input.CompletedAt.UTC()
	if completedAt.IsZero() || completedAt.Before(startedAt) {
		completedAt = startedAt
	}
	durationMS := completedAt.Sub(startedAt).Milliseconds()

	gatewayRequestID := strings.TrimSpace(input.GatewayRequestID)
	if gatewayRequestID == "" {
		digest := sha256.Sum256(append(append([]byte(nil), input.RequestBody...), []byte(startedAt.Format(time.RFC3339Nano))...))
		gatewayRequestID = "generated-" + hex.EncodeToString(digest[:16])
	}
	publicRequestID := c.ids.RequestID(input.Scope, gatewayRequestID)

	envelope := &Envelope{
		SchemaVersion:    SchemaVersion,
		RecordID:         c.ids.RecordID(input.Scope, gatewayRequestID),
		RequestID:        publicRequestID,
		OccurredAt:       startedAt,
		CapturedAt:       completedAt,
		GatewayRequestID: gatewayRequestID,
		Source: SourceInfo{
			Protocol: input.Protocol,
			Endpoint: strings.TrimSpace(input.Endpoint),
			Scope:    input.Scope,
		},
		HTTPStatus: input.HTTPStatus,
		DurationMS: durationMS,
	}

	requestBody := json.RawMessage(bytesClone(input.RequestBody))
	if !json.Valid(requestBody) {
		sessionID, err := c.ids.ResolveSession(
			input.Protocol,
			input.Scope,
			input.SessionHeader,
			requestBody,
			nil,
			publicRequestID,
		)
		if err != nil {
			return nil, fmt.Errorf("resolve rejected request session ID: %w", err)
		}
		envelope.SessionID = sessionID
		envelope.Original.Request = mustJSONText(input.RequestBody)
		envelope.Rejection = &Rejection{Code: "invalid_request_json", Message: "captured request is not valid JSON"}
		return envelope, nil
	}
	envelope.Original.Request = requestBody
	envelope.Source.Stream = requestStreamEnabled(requestBody)

	decoded, decodeErr := decodeCapturedResponse(input.Protocol, input.ResponseBody, input.MaxEventBytes)
	if decodeErr == nil {
		envelope.Original.Response = decoded.Body
	}

	sessionID, err := c.ids.ResolveSession(
		input.Protocol,
		input.Scope,
		input.SessionHeader,
		requestBody,
		decoded.Body,
		publicRequestID,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve session ID: %w", err)
	}
	envelope.SessionID = sessionID

	if decodeErr != nil {
		envelope.Rejection = &Rejection{Code: "response_decode_failed", Message: sanitizeRejectionMessage(decodeErr)}
		return envelope, nil
	}
	if input.HTTPStatus < 200 || input.HTTPStatus >= 300 {
		envelope.Rejection = &Rejection{Code: "http_error", Message: fmt.Sprintf("HTTP status %d is not deliverable", input.HTTPStatus)}
		return envelope, nil
	}
	if !decoded.Complete || decoded.Failed {
		envelope.Rejection = &Rejection{Code: "model_response_failed", Message: "model response did not complete successfully"}
		return envelope, nil
	}

	canonicalRequest, err := c.canonicalRequest(input.Protocol, requestBody)
	if err != nil {
		envelope.Rejection = &Rejection{Code: "request_conversion_failed", Message: sanitizeRejectionMessage(err)}
		return envelope, nil
	}
	canonicalResponse, err := c.canonicalResponse(input.Protocol, decoded.Body, input.Scope, publicRequestID)
	if err != nil {
		envelope.Rejection = &Rejection{Code: "response_conversion_failed", Message: sanitizeRejectionMessage(err)}
		return envelope, nil
	}

	// Complete thinking.signature on the stored delivery record only; the
	// client-facing response was already sent unchanged by the middleware.
	canonicalResponse, err = c.completeThinkingSignatures(input.Protocol, canonicalRequest, canonicalResponse, decoded.Body)
	if err != nil {
		envelope.Rejection = &Rejection{Code: "response_conversion_failed", Message: sanitizeRejectionMessage(err)}
		return envelope, nil
	}

	delivery := &DeliveryRecord{
		SessionID: sessionID,
		RequestID: publicRequestID,
		Timestamp: startedAt,
		Metadata: DeliveryMetadata{
			HTTPStatus: input.HTTPStatus,
			LatencyMS:  durationMS,
		},
		Request: canonicalRequest,
		Response: DeliveryResponse{
			StatusCode:   input.HTTPStatus,
			ResponseData: canonicalResponse,
		},
	}
	if err := ValidateDelivery(delivery, c.publicModel); err != nil {
		envelope.Rejection = &Rejection{Code: "delivery_validation_failed", Message: sanitizeRejectionMessage(err)}
		return envelope, nil
	}
	envelope.Delivery = delivery
	return envelope, nil
}

func (c *Canonicalizer) canonicalRequest(protocol Protocol, body json.RawMessage) (json.RawMessage, error) {
	switch protocol {
	case ProtocolAnthropicMessages:
		var request map[string]json.RawMessage
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, fmt.Errorf("decode Anthropic request: %w", err)
		}
		request["model"] = mustJSON(c.publicModel)
		return json.Marshal(request)
	case ProtocolOpenAIResponses:
		var request apicompat.ResponsesRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, fmt.Errorf("decode Responses request: %w", err)
		}
		converted, err := apicompat.ResponsesToAnthropicRequest(&request)
		if err != nil {
			return nil, fmt.Errorf("convert Responses request: %w", err)
		}
		// Codex sends a large client-owned instructions/developer preamble which
		// names Codex/OpenAI and describes internal transport behavior. Keep the
		// original request in the isolated envelope for audit, but never expose
		// that client fingerprint in the Anthropic delivery projection.
		converted.System = nil
		converted.Model = c.publicModel
		return json.Marshal(converted)
	default:
		return nil, fmt.Errorf("unsupported capture protocol %q", protocol)
	}
}

func (c *Canonicalizer) canonicalResponse(protocol Protocol, body json.RawMessage, scope Scope, publicRequestID string) (json.RawMessage, error) {
	switch protocol {
	case ProtocolAnthropicMessages:
		var response map[string]json.RawMessage
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("decode Anthropic response: %w", err)
		}
		response["id"] = mustJSON(c.ids.ResponseID(scope, rawString(response["id"]), publicRequestID))
		response["model"] = mustJSON(c.publicModel)
		return json.Marshal(response)
	case ProtocolOpenAIResponses:
		var response apicompat.ResponsesResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("decode Responses response: %w", err)
		}
		if response.Status == "failed" {
			return nil, errors.New("Responses response has failed status")
		}
		for _, output := range response.Output {
			switch output.Type {
			case "message", "reasoning", "function_call", "web_search_call":
			default:
				return nil, fmt.Errorf("unsupported Responses output type %q", output.Type)
			}
		}
		converted := apicompat.ResponsesToAnthropic(&response, c.publicModel)
		converted.ID = c.ids.ResponseID(scope, response.ID, publicRequestID)
		converted.Model = c.publicModel
		return json.Marshal(converted)
	default:
		return nil, fmt.Errorf("unsupported capture protocol %q", protocol)
	}
}

// completeThinkingSignatures attaches synthetic Opus 5 shaped signatures to
// unsigned thinking blocks in the stored delivery response (and inserts the
// display=omitted thinking block when a thinking-enabled request produced
// none). Real upstream signatures are never modified.
func (c *Canonicalizer) completeThinkingSignatures(protocol Protocol, canonicalRequest, canonicalResponse, rawResponse json.RawMessage) (json.RawMessage, error) {
	var requestMap map[string]json.RawMessage
	if err := json.Unmarshal(canonicalRequest, &requestMap); err != nil {
		return nil, fmt.Errorf("decode canonical request: %w", err)
	}
	thinkingEnabled := requestThinkingEnabled(requestMap)

	hadReasoning := false
	if protocol == ProtocolOpenAIResponses {
		hadReasoning = responseHadReasoningOutput(rawResponse)
	}

	var responseMap map[string]json.RawMessage
	if err := json.Unmarshal(canonicalResponse, &responseMap); err != nil {
		return nil, fmt.Errorf("decode canonical response: %w", err)
	}
	if err := ensureThinkingSignatures(responseMap, thinkingEnabled, hadReasoning, responseOutputTokens(responseMap)); err != nil {
		return nil, err
	}
	return json.Marshal(responseMap)
}

func requestStreamEnabled(body json.RawMessage) bool {
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil {
		return false
	}
	var stream bool
	_ = json.Unmarshal(request["stream"], &stream)
	return stream
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func mustJSONText(value []byte) json.RawMessage {
	return mustJSON(string(value))
}

func bytesClone(value []byte) []byte {
	return append([]byte(nil), value...)
}

func sanitizeRejectionMessage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
