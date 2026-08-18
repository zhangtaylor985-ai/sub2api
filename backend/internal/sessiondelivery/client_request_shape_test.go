package sessiondelivery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Direct Anthropic API clients legitimately send these members. Delivery is a
// model-level Opus 5 contract, not a requirement that every request imitate the
// narrower Claude Code sample envelope.
func TestNormalizeProjectionFidelityPreservesLegitimateAnthropicRequestMembers(t *testing.T) {
	request := mustJSON(map[string]any{
		"model":              DefaultPublicModel,
		"max_tokens":         64000,
		"thinking":           map[string]any{"type": "adaptive", "display": "omitted"},
		"tool_choice":        map[string]any{"type": "auto"},
		"context_management": map[string]any{"edits": []any{map[string]any{"keep": "all", "type": "clear_thinking_20251015"}}},
		"messages":           []any{map[string]any{"role": "user", "content": "hello"}},
	})
	response := mustJSON(map[string]any{
		"id":          "msg_01abcdefghijklmnopqrstuv",
		"type":        "message",
		"role":        "assistant",
		"model":       DefaultPublicModel,
		"content":     []any{map[string]any{"type": "thinking", "thinking": "consider", "signature": "sig"}, map[string]any{"type": "text", "text": "hello"}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 1, "output_tokens": 2},
	})

	normalized, _, _, err := normalizeProjectionFidelity(request, response, fidelityNormalizationOptions{})
	require.NoError(t, err)
	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(normalized, &decoded))
	require.JSONEq(t, `{"type":"auto"}`, string(decoded["tool_choice"]))
	require.JSONEq(t,
		`{"edits":[{"keep":"all","type":"clear_thinking_20251015"}]}`,
		string(decoded["context_management"]))
}

// Foreign tools rebuilt during conversion come back in alphabetical order,
// leaving one request declaring its tools in two different member orders.
func TestReorderAnthropicToolDeclarationsRestoresMeasuredOrder(t *testing.T) {
	tools := json.RawMessage(`[` +
		`{"name":"Read","description":"Reads a file.","input_schema":{"type":"object"}},` +
		`{"description":"Executes a bash command.","input_schema":{"type":"object"},"name":"Bash"},` +
		`{"input_schema":{"type":"object"},"name":"web_search","type":"web_search_20250305"}` +
		`]`)

	reordered, err := reorderAnthropicToolDeclarations(tools)
	require.NoError(t, err)

	var decoded []json.RawMessage
	require.NoError(t, json.Unmarshal(reordered, &decoded))
	require.Len(t, decoded, 3)
	// Already correct, so byte-identical.
	require.JSONEq(t, `{"name":"Read","description":"Reads a file.","input_schema":{"type":"object"}}`, string(decoded[0]))
	require.Equal(t, `{"name":"Read","description":"Reads a file.","input_schema":{"type":"object"}}`, string(decoded[0]))
	// Converted tool restored to name, description, input_schema.
	require.Equal(t,
		`{"name":"Bash","description":"Executes a bash command.","input_schema":{"type":"object"}}`,
		string(decoded[1]))
	// A server tool declares type instead of a description and leads with it.
	require.Equal(t,
		`{"type":"web_search_20250305","name":"web_search","input_schema":{"type":"object"}}`,
		string(decoded[2]))

	again, err := reorderAnthropicToolDeclarations(reordered)
	require.NoError(t, err)
	require.Equal(t, string(reordered), string(again))
}

// A request whose tools already match is returned untouched.
func TestReorderAnthropicToolDeclarationsLeavesCorrectListByteIdentical(t *testing.T) {
	tools := json.RawMessage(`[{"name":"Read","description":"Reads a file.","input_schema":{"type":"object"}}]`)
	reordered, err := reorderAnthropicToolDeclarations(tools)
	require.NoError(t, err)
	require.Equal(t, string(tools), string(reordered))
}
