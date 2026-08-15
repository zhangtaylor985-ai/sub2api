package sessiondelivery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A role:"system" entry inside messages is not a shape the Anthropic Messages
// API accepts — that endpoint takes only user and assistant turns, with system
// instructions supplied as the top-level system parameter. Real Claude Code
// delivers its mid-conversation injections (date changes, plan-mode notices,
// skill and MCP inventories, hook output) as an extra text block appended to
// the user turn, wrapped in <system-reminder> tags.
//
// MEASURED in this corpus: 606 such entries across 156 records, every one of
// them immediately after a user turn and never at index 0. 56 already carried
// the <system-reminder> wrapper, which is the client's own formatting and the
// ground truth for the shape below: a dedicated text block whose text opens
// with "<system-reminder>\n" and closes with "\n</system-reminder>". The same
// injection text ("The date has changed...") appears in this corpus in both
// forms, so folding restores the shape the client itself uses elsewhere rather
// than inventing one.
const (
	systemReminderOpen  = "<system-reminder>\n"
	systemReminderClose = "\n</system-reminder>"
)

// foldSystemRoleMessages moves every non-user, non-assistant turn into an
// adjacent user turn and drops the original entry. It prefers the preceding
// user turn, which is where the client places these injections; a following
// user turn is the fallback. An entry with no adjacent user turn is left in
// place for the validator to reject, because silently dropping conversation
// content would be worse than a visible failure.
//
// The pass is idempotent: a folded request has no such entry left, and an
// already-wrapped text is carried over verbatim instead of being wrapped twice.
func foldSystemRoleMessages(request map[string]json.RawMessage) (int64, error) {
	raw := request["messages"]
	if !isJSONArray(raw) {
		return 0, nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return 0, fmt.Errorf("decode request messages for system role folding: %w", err)
	}

	decoded := make([]map[string]json.RawMessage, len(messages))
	for index, rawMessage := range messages {
		message, err := decodeJSONObject(rawMessage, "request message")
		if err != nil {
			return 0, err
		}
		decoded[index] = message
	}

	var folded int64
	removed := make([]bool, len(decoded))
	for index, message := range decoded {
		role := rawString(message["role"])
		if role == "user" || role == "assistant" {
			continue
		}
		target := nearestUserTurn(decoded, removed, index)
		if target < 0 {
			continue
		}
		blocks, err := systemReminderBlocks(message["content"])
		if err != nil {
			return 0, err
		}
		if len(blocks) == 0 {
			removed[index] = true
			folded++
			continue
		}
		if err := appendUserContentBlocks(decoded[target], blocks, target > index); err != nil {
			return 0, err
		}
		removed[index] = true
		folded++
	}
	if folded == 0 {
		return 0, nil
	}

	kept := make([]json.RawMessage, 0, len(decoded))
	for index, message := range decoded {
		if removed[index] {
			continue
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return 0, fmt.Errorf("re-encode request message after system role folding: %w", err)
		}
		kept = append(kept, encoded)
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return 0, fmt.Errorf("re-encode request messages after system role folding: %w", err)
	}
	request["messages"] = encoded
	return folded, nil
}

// nearestUserTurn finds the user turn to fold into, scanning backwards first so
// the injection keeps its conversational position, then forwards.
func nearestUserTurn(messages []map[string]json.RawMessage, removed []bool, from int) int {
	for index := from - 1; index >= 0; index-- {
		if !removed[index] && rawString(messages[index]["role"]) == "user" {
			return index
		}
	}
	for index := from + 1; index < len(messages); index++ {
		if !removed[index] && rawString(messages[index]["role"]) == "user" {
			return index
		}
	}
	return -1
}

// systemReminderBlocks converts the entry's content into text blocks carrying
// the client's wrapper. Block content is carried over whole so any member the
// client attached — cache_control in this corpus — survives; only the text is
// wrapped, and only when the client had not wrapped it already.
func systemReminderBlocks(content json.RawMessage) ([]json.RawMessage, error) {
	if len(content) == 0 {
		return nil, nil
	}
	if isJSONArray(content) {
		var blocks []json.RawMessage
		if err := json.Unmarshal(content, &blocks); err != nil {
			return nil, fmt.Errorf("decode system message content for folding: %w", err)
		}
		wrapped := make([]json.RawMessage, 0, len(blocks))
		for _, rawBlock := range blocks {
			block, err := decodeJSONObject(rawBlock, "system message content block")
			if err != nil {
				return nil, err
			}
			if rawString(block["type"]) != "text" {
				wrapped = append(wrapped, rawBlock)
				continue
			}
			text := rawString(block["text"])
			if strings.TrimSpace(text) == "" {
				continue
			}
			block["text"] = mustJSON(wrapSystemReminder(text))
			encoded, err := json.Marshal(block)
			if err != nil {
				return nil, fmt.Errorf("re-encode system message content block for folding: %w", err)
			}
			wrapped = append(wrapped, encoded)
		}
		return wrapped, nil
	}

	var text string
	if err := json.Unmarshal(content, &text); err != nil {
		return nil, fmt.Errorf("decode system message string content for folding: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	block, err := json.Marshal(map[string]string{"type": "text", "text": wrapSystemReminder(text)})
	if err != nil {
		return nil, fmt.Errorf("encode folded system reminder block: %w", err)
	}
	return []json.RawMessage{block}, nil
}

func wrapSystemReminder(text string) string {
	if strings.Contains(text, "<system-reminder>") {
		return text
	}
	return systemReminderOpen + text + systemReminderClose
}

// appendUserContentBlocks attaches the reminder blocks to a user turn,
// promoting string content to a block array because that is what the client
// sends in the overwhelming majority of turns (MEASURED 3,804 array against
// 266 string). Blocks folded backwards land at the end of the turn, matching
// where the client appends them; blocks folded forwards lead the turn so the
// injection still precedes the text it qualifies.
func appendUserContentBlocks(message map[string]json.RawMessage, blocks []json.RawMessage, prepend bool) error {
	existing, err := userContentBlocks(message["content"])
	if err != nil {
		return err
	}
	combined := make([]json.RawMessage, 0, len(existing)+len(blocks))
	if prepend {
		combined = append(combined, blocks...)
		combined = append(combined, existing...)
	} else {
		combined = append(combined, existing...)
		combined = append(combined, blocks...)
	}
	encoded, err := json.Marshal(combined)
	if err != nil {
		return fmt.Errorf("re-encode user content after system role folding: %w", err)
	}
	message["content"] = encoded
	return nil
}

func userContentBlocks(content json.RawMessage) ([]json.RawMessage, error) {
	if len(content) == 0 {
		return nil, nil
	}
	if isJSONArray(content) {
		var blocks []json.RawMessage
		if err := json.Unmarshal(content, &blocks); err != nil {
			return nil, fmt.Errorf("decode user content for system role folding: %w", err)
		}
		return blocks, nil
	}
	var text string
	if err := json.Unmarshal(content, &text); err != nil {
		return nil, fmt.Errorf("decode user string content for system role folding: %w", err)
	}
	block, err := json.Marshal(map[string]string{"type": "text", "text": text})
	if err != nil {
		return nil, fmt.Errorf("encode promoted user text block: %w", err)
	}
	return []json.RawMessage{block}, nil
}
