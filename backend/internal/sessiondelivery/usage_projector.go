package sessiondelivery

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// Export-time usage projection.
//
// The GPT upstream has no Anthropic prompt-cache semantics, so captured usage
// reports cache_creation_input_tokens=0 and a flat cache_read — a systematic
// tell against real Claude Code x Opus 5 traffic, where every turn shows the
// deterministic pattern (measured on a real local Opus 5 session):
//
//	input_tokens            = 2 (tiny uncached tail)
//	cache_read(k)           = prefix(k-1)        (read what the last turn wrote)
//	cache_creation(k)       = prefix(k) - read(k) (only the new content)
//	prefix(k)               = total_prompt(k) - input_tokens
//	chain restart (read=0)  on the first turn, after client-side compaction
//	                         (first user message changes), or when the gap
//	                         between turns exceeds the 5m ephemeral TTL
//
// Totals come from the real upstream token counts stored at capture time;
// only the cache split is simulated. Runs at export, sharing the per-session
// ordered stream with the echo repair, so stored envelopes stay as captured.
type usageProjector struct {
	sessionID    string
	prevPrefix   int
	firstMsgKey  [32]byte
	prevOccurred time.Time
	haveState    bool
}

const (
	// anthropicUncachedTail is the constant tiny uncached suffix observed in
	// real Claude Code traffic (trailing system-reminder tokens).
	anthropicUncachedTail = 2
	// ephemeralCacheTTL is the default 5-minute cache TTL Claude Code uses;
	// longer gaps force a real cache miss (read=0, full re-creation).
	ephemeralCacheTTL = 5 * time.Minute
)

func (p *usageProjector) process(record *DeliveryRecord) error {
	if record == nil {
		return nil
	}
	if record.SessionID != p.sessionID {
		p.sessionID = record.SessionID
		p.haveState = false
	}

	response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
	if err != nil {
		return err
	}
	if len(response["usage"]) == 0 {
		return nil // nothing to project
	}
	var usage map[string]json.RawMessage
	if err := json.Unmarshal(response["usage"], &usage); err != nil {
		return fmt.Errorf("decode response usage: %w", err)
	}

	total := rawInt(usage["input_tokens"]) + rawInt(usage["cache_read_input_tokens"]) + rawInt(usage["cache_creation_input_tokens"])
	if total <= 0 {
		total = len(record.Request) / 4 // fallback: rough bytes-per-token estimate
	}
	prefix := total - anthropicUncachedTail
	if prefix < 0 {
		prefix = 0
	}

	msgKey := firstUserMessageKey(record.Request)
	newChain := !p.haveState ||
		msgKey != p.firstMsgKey ||
		record.Timestamp.Sub(p.prevOccurred) > ephemeralCacheTTL

	var read, creation int
	if newChain {
		read, creation = 0, prefix
	} else {
		read = p.prevPrefix
		if read > prefix {
			read = prefix // defensive: prefix never shrinks within a chain
		}
		creation = prefix - read
	}

	usage["input_tokens"] = mustJSON(anthropicUncachedTail)
	usage["cache_read_input_tokens"] = mustJSON(read)
	usage["cache_creation_input_tokens"] = mustJSON(creation)
	usage["cache_creation"] = mustJSON(map[string]int{
		"ephemeral_1h_input_tokens": 0,
		"ephemeral_5m_input_tokens": creation,
	})
	usage["server_tool_use"] = mustJSON(map[string]int{
		"web_search_requests": countServerToolCalls(response["content"], "web_search"),
		"web_fetch_requests":  countServerToolCalls(response["content"], "web_fetch"),
	})
	if _, ok := usage["service_tier"]; !ok {
		usage["service_tier"] = mustJSON("standard")
	}
	usage["inference_geo"] = mustJSON("global")
	usage["iterations"] = mustJSON([]any{})
	usage["speed"] = mustJSON("standard")

	encoded, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("re-encode usage: %w", err)
	}
	response["usage"] = encoded
	encodedResp, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("re-encode response: %w", err)
	}
	record.Response.ResponseData = encodedResp

	p.prevPrefix = prefix
	p.firstMsgKey = msgKey
	p.prevOccurred = record.Timestamp
	p.haveState = true
	return nil
}

// firstUserMessageKey fingerprints the conversation head; client-side
// compaction replaces it, which is exactly when the real cache chain breaks.
func firstUserMessageKey(request json.RawMessage) [32]byte {
	var parsed struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(request, &parsed); err != nil || len(parsed.Messages) == 0 {
		return [32]byte{}
	}
	return sha256.Sum256(parsed.Messages[0].Content)
}

func countServerToolCalls(content json.RawMessage, name string) int {
	var blocks []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if len(content) == 0 || json.Unmarshal(content, &blocks) != nil {
		return 0
	}
	n := 0
	for _, b := range blocks {
		if b.Type == "server_tool_use" && b.Name == name {
			n++
		}
	}
	return n
}

func rawInt(raw json.RawMessage) int {
	var v int
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}
