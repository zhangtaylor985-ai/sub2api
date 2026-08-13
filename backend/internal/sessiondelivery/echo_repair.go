package sessiondelivery

import (
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
	prior     map[string][]json.RawMessage // assistant text -> thinking blocks of that response
}

func (r *echoRepair) process(record *DeliveryRecord) error {
	if record == nil {
		return nil
	}
	if record.SessionID != r.sessionID {
		r.sessionID = record.SessionID
		r.prior = make(map[string][]json.RawMessage)
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
	for i, rawMsg := range messages {
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
		if len(content) == 0 || contentHasBlock(content, "thinking") {
			continue
		}
		blocks, ok := r.prior[assistantTextKey(content)]
		if !ok || len(blocks) == 0 {
			continue
		}
		msg["content"] = mustJSON(append(append([]json.RawMessage{}, blocks...), content...))
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
	r.prior[assistantTextKey(content)] = thinking
	return nil
}

func contentHasBlock(content []json.RawMessage, blockType string) bool {
	for _, block := range content {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(block, &head); err != nil {
			continue
		}
		if head.Type == blockType {
			return true
		}
	}
	return false
}

// assistantTextKey concatenates visible text blocks in order; it is the echo
// match key a real client would reproduce verbatim.
func assistantTextKey(content []json.RawMessage) string {
	key := ""
	for _, block := range content {
		var parsed struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(block, &parsed); err != nil {
			continue
		}
		if parsed.Type == "text" {
			key += parsed.Text
		}
	}
	return key
}
