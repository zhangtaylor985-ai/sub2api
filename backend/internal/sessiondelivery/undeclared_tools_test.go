package sessiondelivery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func toolCallResponse(names ...string) map[string]json.RawMessage {
	blocks := make([]any, 0, len(names)+1)
	blocks = append(blocks, map[string]any{"type": "text", "text": "working on it"})
	for index, name := range names {
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    "toolu_01abcdefghijklmnopqrstu" + string(rune('a'+index)),
			"name":  name,
			"input": map[string]any{},
		})
	}
	return map[string]json.RawMessage{
		"stop_reason": mustJSON("tool_use"),
		"content":     mustJSON(blocks),
	}
}

// MEASURED: 10 of 10365 captured records call a tool with no tools key at all.
func TestCountUndeclaredResponseToolsFlagsCallsWithNoDeclaredSurface(t *testing.T) {
	for _, name := range []string{"exec", "wait", "list_agents"} {
		request := map[string]json.RawMessage{"model": mustJSON(DefaultPublicModel)}
		require.Equal(t, int64(1), countUndeclaredResponseTools(request, toolCallResponse(name)), name)
	}

	// Every call in the turn is counted.
	request := map[string]json.RawMessage{"model": mustJSON(DefaultPublicModel)}
	require.Equal(t, int64(2), countUndeclaredResponseTools(request, toolCallResponse("exec", "wait")))
}

// An empty array is the same as no surface at all.
func TestCountUndeclaredResponseToolsTreatsEmptyArrayAsNoSurface(t *testing.T) {
	request := map[string]json.RawMessage{"tools": json.RawMessage(`[]`)}
	require.Equal(t, int64(1), countUndeclaredResponseTools(request, toolCallResponse("exec")))
}

// A response naming a tool absent from a non-empty array does occur in genuine
// Claude Code traffic, so the narrow condition must not reach it.
func TestCountUndeclaredResponseToolsIgnoresCallsAgainstADeclaredSurface(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{
			map[string]any{"name": "Read", "description": "Reads a file.", "input_schema": map[string]any{}},
		}),
	}
	// Declared.
	require.Zero(t, countUndeclaredResponseTools(request, toolCallResponse("Read")))
	// Undeclared, but a surface exists, so this is left alone.
	require.Zero(t, countUndeclaredResponseTools(request, toolCallResponse("Bash")))
}

// A turn with no tool call is unaffected however the tools key looks.
func TestCountUndeclaredResponseToolsIgnoresTurnsWithoutToolCalls(t *testing.T) {
	response := map[string]json.RawMessage{
		"stop_reason": mustJSON("end_turn"),
		"content":     mustJSON([]any{map[string]any{"type": "text", "text": "done"}}),
	}
	for _, request := range []map[string]json.RawMessage{
		{"model": mustJSON(DefaultPublicModel)},
		{"tools": json.RawMessage(`[]`)},
		{"tools": mustJSON([]any{map[string]any{"name": "Read"}})},
	} {
		require.Zero(t, countUndeclaredResponseTools(request, response))
	}
}

// End to end through the normalizer, the pass the exporter and rebuild share.
func TestNormalizeProjectionFidelityReportsUndeclaredResponseTools(t *testing.T) {
	request := mustJSON(map[string]any{
		"model":      DefaultPublicModel,
		"max_tokens": 64000,
		"thinking":   map[string]any{"type": "adaptive", "display": "omitted"},
		"messages":   []any{map[string]any{"role": "user", "content": "run it"}},
	})
	response := mustJSON(map[string]any{
		"id":    "msg_01" + "abcdefghijklmnopqrstuv",
		"type":  "message",
		"role":  "assistant",
		"model": DefaultPublicModel,
		"content": []any{map[string]any{
			"type":  "tool_use",
			"id":    "toolu_01abcdefghijklmnopqrstuv",
			"name":  "exec",
			"input": map[string]any{"cmd": "ls"},
		}},
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 20},
	})

	_, _, stats, err := normalizeProjectionFidelity(request, response, fidelityNormalizationOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.UndeclaredResponseTools)
}
