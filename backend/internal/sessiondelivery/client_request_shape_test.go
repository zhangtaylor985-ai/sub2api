package sessiondelivery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// The captured values: tool_choice "auto" and a context edit keeping everything.
func TestNormalizeClientRequestShapeDropsInertMembers(t *testing.T) {
	request := map[string]json.RawMessage{
		"model":              mustJSON(DefaultPublicModel),
		"tool_choice":        json.RawMessage(`{"type":"auto"}`),
		"context_management": json.RawMessage(`{"edits":[{"keep":"all","type":"clear_thinking_20251015"}]}`),
	}
	require.ErrorContains(t, validateClientRequestShape(request), "tool_choice")

	dropped, err := normalizeClientRequestShape(request)
	require.NoError(t, err)
	require.Equal(t, int64(2), dropped)
	require.NotContains(t, request, "tool_choice")
	require.NotContains(t, request, "context_management")
	require.Contains(t, request, "model")
	require.NoError(t, validateClientRequestShape(request))

	dropped, err = normalizeClientRequestShape(request)
	require.NoError(t, err)
	require.Zero(t, dropped)
}

// A value that steered the response has to stay, or the record stops explaining
// itself.
func TestNormalizeClientRequestShapeKeepsMembersThatShapedTheResponse(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"forced tool", "tool_choice", `{"name":"Bash","type":"tool"}`},
		{"any tool", "tool_choice", `{"type":"any"}`},
		{"no tools", "tool_choice", `{"type":"none"}`},
		{"auto with modifier", "tool_choice", `{"disable_parallel_tool_use":true,"type":"auto"}`},
		{"clearing edit", "context_management", `{"edits":[{"keep":"3","type":"clear_thinking_20251015"}]}`},
		{"trimming edit", "context_management", `{"edits":[{"clear_at_least":{"type":"input_tokens","value":100},"keep":"all","type":"clear_tool_uses_20250919"}]}`},
		{"unknown member", "context_management", `{"edits":[{"keep":"all","type":"x"}],"trigger":{"type":"input_tokens","value":1}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := map[string]json.RawMessage{testCase.key: json.RawMessage(testCase.value)}
			dropped, err := normalizeClientRequestShape(request)
			require.NoError(t, err)
			require.Zero(t, dropped)
			require.JSONEq(t, testCase.value, string(request[testCase.key]))
			require.NoError(t, validateClientRequestShape(request))
		})
	}
}

func TestNormalizeClientRequestShapeIgnoresAbsentMembers(t *testing.T) {
	request := map[string]json.RawMessage{"model": mustJSON(DefaultPublicModel)}
	dropped, err := normalizeClientRequestShape(request)
	require.NoError(t, err)
	require.Zero(t, dropped)
	require.NoError(t, validateClientRequestShape(request))
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
