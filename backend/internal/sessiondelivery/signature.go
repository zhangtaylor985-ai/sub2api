package sessiondelivery

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/thinkingsig"
)

// The GPT-backed dispatch can never produce a real Anthropic thinking
// signature, but the vendor delivery spec requires thinking.signature to be
// present and preserved. The capture boundary therefore completes the field
// at canonicalization time with a byte-shape-faithful synthetic envelope
// (see internal/pkg/thinkingsig), applied ONLY to the stored delivery
// record — client-facing responses are never modified.
//
// Rules:
//   - thinking block with a real upstream signature -> kept verbatim.
//   - thinking block without a signature (GPT projection) -> thinking text is
//     blanked to the Opus 5 display=omitted shape and a synthetic signature
//     is attached.
//   - thinking enabled in the request but no thinking block in the response
//     -> an empty thinking block is prepended (Opus 5 always emits one).
//   - redacted_thinking blocks pass through untouched; the GPT path never
//     produces them.

// ensureThinkingSignatures rewrites the response content in place.
// thinkingEnabled reflects the canonical request's thinking config;
// hadReasoningOutput reports a reasoning item in a Responses-protocol
// response. outputTokens sizes the synthetic ciphertext payload.
func ensureThinkingSignatures(response map[string]json.RawMessage, thinkingEnabled, hadReasoningOutput bool, outputTokens int) error {
	var content []json.RawMessage
	if raw := response["content"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &content); err != nil {
			return fmt.Errorf("decode response content: %w", err)
		}
	}
	model := rawString(response["model"])
	if model == "" {
		model = DefaultPublicModel
	}
	payloadLen := thinkingsig.PayloadLen(outputTokens)

	hasThinking := false
	for i, rawBlock := range content {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			continue
		}
		switch rawString(block["type"]) {
		case "thinking":
			hasThinking = true
			if rawString(block["signature"]) != "" {
				continue // real upstream signature: preserve byte-exact
			}
			block["thinking"] = mustJSON("")
			block["signature"] = mustJSON(thinkingsig.Generate(model, payloadLen))
			reencoded, err := json.Marshal(block)
			if err != nil {
				return fmt.Errorf("re-encode thinking block: %w", err)
			}
			content[i] = reencoded
		}
	}

	if !hasThinking && (thinkingEnabled || hadReasoningOutput) {
		block := mustJSON(map[string]any{
			"type":      "thinking",
			"thinking":  "",
			"signature": thinkingsig.Generate(model, payloadLen),
		})
		content = append([]json.RawMessage{block}, content...)
	}

	encoded, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("re-encode response content: %w", err)
	}
	response["content"] = encoded
	return nil
}

// requestThinkingEnabled reports whether the canonical Anthropic request
// enabled thinking (type enabled/adaptive).
func requestThinkingEnabled(request map[string]json.RawMessage) bool {
	var thinking struct {
		Type string `json:"type"`
	}
	if raw := request["thinking"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &thinking); err != nil {
			return false
		}
	}
	return thinking.Type == "enabled" || thinking.Type == "adaptive"
}

// responseOutputTokens extracts usage.output_tokens for payload sizing.
func responseOutputTokens(response map[string]json.RawMessage) int {
	var usage struct {
		OutputTokens int `json:"output_tokens"`
	}
	if raw := response["usage"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &usage)
	}
	return usage.OutputTokens
}

// responseHadReasoningOutput reports a reasoning item in a raw Responses
// response body.
func responseHadReasoningOutput(body json.RawMessage) bool {
	var response struct {
		Output []struct {
			Type string `json:"type"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return false
	}
	for _, item := range response.Output {
		if strings.EqualFold(item.Type, "reasoning") {
			return true
		}
	}
	return false
}
