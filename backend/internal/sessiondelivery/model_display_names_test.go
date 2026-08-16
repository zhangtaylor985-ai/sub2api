package sessiondelivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Bash tool description carries the commit trailer Claude Code tells the
// model to append, naming the active model.
const bashDescriptionTemplate = "Execute a bash command.\n" +
	"- Commit only when the user asks. If on the default branch, branch first.\n" +
	"- End git commit messages with:\n" +
	"Co-Authored-By: Claude %s <noreply@anthropic.com>\n" +
	"- End PR bodies with:\n" +
	"🤖 Generated with [Claude Code](https://claude.com/claude-code)"

func toolRequest(description string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"tools": mustJSON([]any{
			map[string]any{
				"name":         "Bash",
				"description":  description,
				"input_schema": map[string]any{"type": "object"},
			},
		}),
	}
}

func TestNormalizeModelDisplayNamesRewritesCommitTrailer(t *testing.T) {
	for _, name := range []string{"Fable 5", "Sonnet 5", "Opus 4.8"} {
		request := toolRequest(strings.Replace(bashDescriptionTemplate, "%s", name, 1))
		require.ErrorContains(t, validateModelDisplayNames(request), "credits a foreign model")

		rewrites, err := normalizeModelDisplayNames(request)
		require.NoError(t, err)
		require.Equal(t, int64(1), rewrites)
		require.NoError(t, validateModelDisplayNames(request))

		description := toolDescription(t, request)
		require.Contains(t, description, "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>")
		require.NotContains(t, description, name)
		// Surrounding instructions survive.
		require.Contains(t, description, "Generated with [Claude Code]")
		require.Contains(t, description, "If on the default branch, branch first.")
		// The measured member order survives re-encoding.
		encoded := string(request["tools"])
		require.Less(t, strings.Index(encoded, `"name"`), strings.Index(encoded, `"description"`))
		require.Less(t, strings.Index(encoded, `"description"`), strings.Index(encoded, `"input_schema"`))
	}
}

// toolDescription decodes the first tool's description. Raw request bytes keep
// Go's HTML escaping until the delivery writer undoes it, so assertions about
// prose containing "<" have to read the decoded string.
func toolDescription(t *testing.T, request map[string]json.RawMessage) string {
	t.Helper()
	var tools []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(request["tools"], &tools))
	require.NotEmpty(t, tools)
	var description string
	require.NoError(t, json.Unmarshal(tools[0]["description"], &description))
	return description
}

// Both display names that denote the delivered model are authentic, as is a
// bare trailer naming no model.
func TestNormalizeModelDisplayNamesKeepsDeliveredAndBareTrailers(t *testing.T) {
	for _, trailer := range []string{
		"Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>",
		"Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>",
		"Co-Authored-By: Claude <noreply@anthropic.com>",
	} {
		request := toolRequest("Execute a bash command.\n" + trailer)
		before := string(request["tools"])
		rewrites, err := normalizeModelDisplayNames(request)
		require.NoError(t, err)
		require.Zero(t, rewrites, trailer)
		require.Equal(t, before, string(request["tools"]), trailer)
		require.Contains(t, toolDescription(t, request), trailer)
		require.NoError(t, validateModelDisplayNames(request))
	}
}

// Untouched declarations must stay byte-identical so the pass cannot perturb
// tools it has no reason to change.
func TestNormalizeModelDisplayNamesLeavesUnrelatedToolsByteIdentical(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{
			map[string]any{"description": "Read a file.", "input_schema": map[string]any{}, "name": "Read"},
			map[string]any{
				"name":         "Bash",
				"description":  strings.Replace(bashDescriptionTemplate, "%s", "Fable 5", 1),
				"input_schema": map[string]any{"type": "object"},
			},
		}),
	}
	var before []json.RawMessage
	require.NoError(t, json.Unmarshal(request["tools"], &before))

	rewrites, err := normalizeModelDisplayNames(request)
	require.NoError(t, err)
	require.Equal(t, int64(1), rewrites)

	var after []json.RawMessage
	require.NoError(t, json.Unmarshal(request["tools"], &after))
	require.Equal(t, string(before[0]), string(after[0]), "Read must be untouched")
	require.NotEqual(t, string(before[1]), string(after[1]))
}

// Claude Code sends its environment block as a conversation turn, carrying the
// identity line and knowledge cutoff outside the system prompt.
func TestNormalizeModelDisplayNamesRewritesEnvironmentTurn(t *testing.T) {
	block := "<env>\n - Platform: darwin\n - Shell: zsh\n" +
		" - You are powered by the model named Fable 5. The exact model ID is claude-fable-5.\n" +
		" - Assistant knowledge cutoff is January 2026.\n</env>"

	for _, content := range []any{
		block,
		[]any{map[string]any{"type": "text", "text": block}},
	} {
		request := map[string]json.RawMessage{
			"messages": mustJSON([]any{map[string]any{"role": "user", "content": content}}),
		}
		require.ErrorContains(t, validateModelDisplayNames(request), "names a foreign model")

		rewrites, err := normalizeModelDisplayNames(request)
		require.NoError(t, err)
		require.Equal(t, int64(1), rewrites)
		require.NoError(t, validateModelDisplayNames(request))

		encoded := string(request["messages"])
		require.Contains(t, encoded, "named Opus 5. The exact model ID is claude-opus-5.")
		require.Contains(t, encoded, "cutoff is May 2026.")
		require.NotContains(t, encoded, "Fable")
		require.NotContains(t, encoded, "January 2026")
		// Environment facts around the identity line are untouched.
		require.Contains(t, encoded, "Platform: darwin")
		require.Less(t, strings.Index(encoded, `"role"`), strings.Index(encoded, `"content"`))
	}
}

// Tool inputs and results are not client template text and must pass through.
func TestNormalizeModelDisplayNamesLeavesNonTextBlocks(t *testing.T) {
	request := map[string]json.RawMessage{
		"messages": mustJSON([]any{
			map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type":  "tool_use",
				"id":    "toolu_01abcdefghijklmnopqrstuv",
				"name":  "Bash",
				"input": map[string]any{"command": "git log --author='Claude Fable 5'"},
			}}},
			map[string]any{"role": "user", "content": []any{map[string]any{
				"type":        "tool_result",
				"tool_use_id": "toolu_01abcdefghijklmnopqrstuv",
				"content":     "Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
			}}},
		}),
	}
	before := string(request["messages"])
	rewrites, err := normalizeModelDisplayNames(request)
	require.NoError(t, err)
	require.Zero(t, rewrites)
	require.Equal(t, before, string(request["messages"]))
}

func TestNormalizeModelDisplayNamesIsIdempotent(t *testing.T) {
	request := toolRequest(strings.Replace(bashDescriptionTemplate, "%s", "Fable 5", 1))
	request["messages"] = mustJSON([]any{map[string]any{
		"role":    "user",
		"content": "You are powered by the model named Fable 5. The exact model ID is claude-fable-5.",
	}})

	rewrites, err := normalizeModelDisplayNames(request)
	require.NoError(t, err)
	require.Equal(t, int64(2), rewrites)
	first := string(request["tools"]) + "|" + string(request["messages"])

	rewrites, err = normalizeModelDisplayNames(request)
	require.NoError(t, err)
	require.Zero(t, rewrites)
	require.Equal(t, first, string(request["tools"])+"|"+string(request["messages"]))
}
