package sessiondelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Export-time conversation echo repair.
//
// Real Claude Code clients echo each assistant turn's thinking blocks
// (signature included) verbatim in the next request's message history. Our
// gateway clients never see thinking blocks — the live response path is
// intentionally untouched — so captured requests lack those echoes, and a
// delivered session would diverge from real Claude Code x Opus 5 traffic.
// The exporter therefore re-inserts the thinking blocks of earlier responses
// into later requests of the same session.
//
// Records stream in ordered by (session_id, occurred_at, request_id), so a
// small per-session state suffices. Matching is content-based (exact
// concatenated assistant text) to stay correct under client-side history
// compaction. Requests without thinking enabled are left untouched, as are
// assistant messages that already carry thinking blocks or match no earlier
// response.
type echoRepair struct {
	sessionID string
	prior     []echoAssistantTurn
}

type echoAssistantTurn struct {
	key      string
	thinking []json.RawMessage
}

func (r *echoRepair) restore(sessionID string, checkpoint []projectionEchoTurn) {
	r.sessionID = sessionID
	r.prior = make([]echoAssistantTurn, 0, len(checkpoint))
	for _, turn := range checkpoint {
		r.prior = append(r.prior, echoAssistantTurn{
			key:      turn.Key,
			thinking: append([]json.RawMessage(nil), turn.Thinking...),
		})
	}
}

func (r *echoRepair) checkpoint() []projectionEchoTurn {
	checkpoint := make([]projectionEchoTurn, 0, len(r.prior))
	for _, turn := range r.prior {
		checkpoint = append(checkpoint, projectionEchoTurn{
			Key:      turn.key,
			Thinking: append([]json.RawMessage(nil), turn.thinking...),
		})
	}
	return checkpoint
}

func (r *echoRepair) process(record *DeliveryRecord) error {
	if record == nil {
		return nil
	}
	if record.SessionID != r.sessionID {
		r.sessionID = record.SessionID
		r.prior = nil
	}

	request, err := decodeJSONObject(record.Request, "request")
	if err != nil {
		return err
	}
	if requestThinkingEnabled(request) {
		changed, err := r.repairMessages(request)
		if err != nil {
			return err
		}
		if changed {
			encoded, err := json.Marshal(request)
			if err != nil {
				return fmt.Errorf("re-encode repaired request: %w", err)
			}
			record.Request = encoded
		}
	}
	return r.collectResponse(record.Response.ResponseData)
}

// repairMessages prepends earlier responses' thinking blocks onto matching
// assistant messages in the request history. Returns whether anything changed.
func (r *echoRepair) repairMessages(request map[string]json.RawMessage) (bool, error) {
	var messages []json.RawMessage
	if raw := request["messages"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &messages); err != nil {
			return false, fmt.Errorf("decode request messages: %w", err)
		}
	}
	changed := false
	priorIndex := len(r.prior) - 1
	// Match backwards so duplicate assistant texts align with their most
	// recent corresponding responses. This also naturally handles compacted
	// histories that retain only a suffix of the original conversation.
	for i := len(messages) - 1; i >= 0 && priorIndex >= 0; i-- {
		rawMsg := messages[i]
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			continue
		}
		if rawString(msg["role"]) != "assistant" {
			continue
		}
		var content []json.RawMessage
		if raw := msg["content"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &content); err != nil {
				continue // string content or other shapes: leave untouched
			}
		}
		if len(content) == 0 {
			continue
		}
		key := assistantContentKey(content)
		if key == "" {
			continue
		}
		matched := -1
		for j := priorIndex; j >= 0; j-- {
			if r.prior[j].key == key {
				matched = j
				break
			}
		}
		if matched < 0 {
			continue
		}
		turn := r.prior[matched]
		priorIndex = matched - 1
		if len(turn.thinking) == 0 {
			continue
		}
		hasThinking, hasSignedThinking := thinkingBlockSignatureState(content)
		if hasSignedThinking {
			continue // preserve a real or already completed echo byte-exact
		}
		if hasThinking {
			// Older projections could echo an unsigned thinking block. Replace
			// only that incomplete prefix with the signed block retained from
			// the matching prior response.
			content = withoutContentBlock(content, "thinking")
		}
		msg["content"] = mustJSON(append(append([]json.RawMessage{}, turn.thinking...), content...))
		reencoded, err := json.Marshal(msg)
		if err != nil {
			return false, fmt.Errorf("re-encode assistant message: %w", err)
		}
		messages[i] = reencoded
		changed = true
	}
	if changed {
		request["messages"] = mustJSON(messages)
	}
	return changed, nil
}

// collectResponse remembers this response's thinking blocks keyed by its
// visible text, so later requests in the session can echo them.
func (r *echoRepair) collectResponse(responseData json.RawMessage) error {
	if len(responseData) == 0 {
		return nil
	}
	response, err := decodeJSONObject(responseData, "response.response_data")
	if err != nil {
		return err
	}
	var content []json.RawMessage
	if raw := response["content"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &content); err != nil {
			return fmt.Errorf("decode response content: %w", err)
		}
	}
	var thinking []json.RawMessage
	for _, block := range content {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(block, &head); err != nil {
			continue
		}
		if head.Type == "thinking" {
			thinking = append(thinking, block)
		}
	}
	if len(thinking) == 0 {
		return nil
	}
	key := assistantContentKey(content)
	if key != "" {
		r.prior = append(r.prior, echoAssistantTurn{key: key, thinking: thinking})
	}
	return nil
}

func thinkingBlockSignatureState(content []json.RawMessage) (hasThinking, hasSignedThinking bool) {
	for _, block := range content {
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(block, &parsed); err != nil || rawString(parsed["type"]) != "thinking" {
			continue
		}
		hasThinking = true
		signature := rawString(parsed["signature"])
		if signature != "" && validateOpus5SignatureShape(signature, DefaultPublicModel) == nil {
			hasSignedThinking = true
		}
	}
	return hasThinking, hasSignedThinking
}

func withoutContentBlock(content []json.RawMessage, blockType string) []json.RawMessage {
	filtered := make([]json.RawMessage, 0, len(content))
	for _, block := range content {
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(block, &head) == nil && head.Type == blockType {
			continue
		}
		filtered = append(filtered, block)
	}
	return filtered
}

// assistantContentKey prefers visible text because request and response
// canonicalization may rewrite opaque tool IDs. Length-prefixing prevents
// collisions between differently split text blocks. Tool-only turns fall back
// to a canonical JSON digest of their non-thinking blocks.
func assistantContentKey(content []json.RawMessage) string {
	textKey := ""
	var fallback []json.RawMessage
	for _, block := range content {
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(block, &parsed); err != nil {
			continue
		}
		blockType := rawString(parsed["type"])
		if blockType == "thinking" || blockType == "redacted_thinking" {
			continue
		}
		if blockType == "text" {
			text := rawString(parsed["text"])
			textKey += fmt.Sprintf("%d:%s", len(text), text)
		}
		canonical, err := json.Marshal(parsed)
		if err == nil {
			fallback = append(fallback, canonical)
		}
	}
	if textKey != "" {
		digest := sha256.Sum256([]byte(textKey))
		return "text:" + hex.EncodeToString(digest[:])
	}
	if len(fallback) == 0 {
		return ""
	}
	canonical, err := json.Marshal(fallback)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(canonical)
	return "content:" + hex.EncodeToString(digest[:])
}
