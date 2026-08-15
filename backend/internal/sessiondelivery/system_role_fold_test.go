package sessiondelivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func decodeMessages(t *testing.T, request json.RawMessage) []map[string]any {
	t.Helper()
	var decoded struct {
		Messages []map[string]any `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(request, &decoded))
	return decoded.Messages
}

func blockTexts(t *testing.T, content any) []string {
	t.Helper()
	blocks, ok := content.([]any)
	require.True(t, ok, "content must be a block array, got %T", content)
	texts := make([]string, 0, len(blocks))
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		require.True(t, ok)
		if block["type"] == "text" {
			texts = append(texts, block["text"].(string))
		}
	}
	return texts
}

func TestFoldSystemRoleMessagesAppendsReminderToPrecedingUserTurn(t *testing.T) {
	request := map[string]json.RawMessage{
		"messages": mustJSON([]any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "run the tests"},
			}},
			map[string]any{"role": "system", "content": "The date has changed. Today's date is now 2026-08-12."},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "ok"},
			}},
		}),
	}

	folded, err := foldSystemRoleMessages(request)
	require.NoError(t, err)
	require.Equal(t, int64(1), folded)

	messages := decodeMessages(t, mustJSON(map[string]json.RawMessage{"messages": request["messages"]}))
	require.Len(t, messages, 2)
	require.Equal(t, "user", messages[0]["role"])
	require.Equal(t, "assistant", messages[1]["role"])

	texts := blockTexts(t, messages[0]["content"])
	require.Equal(t, []string{
		"run the tests",
		"<system-reminder>\nThe date has changed. Today's date is now 2026-08-12.\n</system-reminder>",
	}, texts)
}

// The client wraps some injections itself; those must survive verbatim so a
// second pass cannot nest one wrapper inside another.
func TestFoldSystemRoleMessagesDoesNotDoubleWrapOrRepeat(t *testing.T) {
	alreadyWrapped := "<system-reminder>\nPlan mode is active.\n</system-reminder>"
	request := map[string]json.RawMessage{
		"messages": mustJSON([]any{
			map[string]any{"role": "user", "content": "go"},
			map[string]any{"role": "system", "content": []any{
				map[string]any{"type": "text", "text": alreadyWrapped, "cache_control": map[string]any{"type": "ephemeral"}},
			}},
		}),
	}

	folded, err := foldSystemRoleMessages(request)
	require.NoError(t, err)
	require.Equal(t, int64(1), folded)
	first := request["messages"]

	// A string user content is promoted to a block array, which is the shape
	// the client sends in the overwhelming majority of turns.
	messages := decodeMessages(t, mustJSON(map[string]json.RawMessage{"messages": first}))
	require.Len(t, messages, 1)
	require.Equal(t, []string{"go", alreadyWrapped}, blockTexts(t, messages[0]["content"]))
	// Counted on the decoded text, because the raw bytes still carry Go's HTML
	// escaping until the record is written out.
	require.Equal(t, 1, strings.Count(blockTexts(t, messages[0]["content"])[1], "<system-reminder>"))

	// Members the client attached to the block are carried over.
	require.Contains(t, string(first), `"cache_control"`)

	folded, err = foldSystemRoleMessages(request)
	require.NoError(t, err)
	require.Zero(t, folded)
	require.JSONEq(t, string(first), string(request["messages"]))
}

func TestFoldSystemRoleMessagesFallsBackToFollowingUserTurn(t *testing.T) {
	request := map[string]json.RawMessage{
		"messages": mustJSON([]any{
			map[string]any{"role": "system", "content": "Skills are available."},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "hello"},
			}},
		}),
	}

	folded, err := foldSystemRoleMessages(request)
	require.NoError(t, err)
	require.Equal(t, int64(1), folded)

	messages := decodeMessages(t, mustJSON(map[string]json.RawMessage{"messages": request["messages"]}))
	require.Len(t, messages, 1)
	require.Equal(t, []string{
		"<system-reminder>\nSkills are available.\n</system-reminder>",
		"hello",
	}, blockTexts(t, messages[0]["content"]))
}

// Dropping conversation content silently would be worse than a visible
// failure, so an entry with no adjacent user turn is left for the validator.
func TestFoldSystemRoleMessagesLeavesUnattachableEntry(t *testing.T) {
	request := map[string]json.RawMessage{
		"messages": mustJSON([]any{
			map[string]any{"role": "system", "content": "orphan"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "hi"},
			}},
		}),
	}

	folded, err := foldSystemRoleMessages(request)
	require.NoError(t, err)
	require.Zero(t, folded)
	require.Contains(t, string(request["messages"]), `"system"`)
}

func TestValidateDeliveryFidelityRejectsForeignRequestShape(t *testing.T) {
	record := func(request any) *DeliveryRecord {
		record := usageTestRecord("session_role_gate", fixedTestTime(), 100, 10, "hi")
		record.Request = mustJSON(request)
		record.Response.ResponseData = mustJSON(map[string]any{
			"id": anthropicPublicID("msg_", "role_gate"), "type": "message",
			"role": "assistant", "model": DefaultPublicModel,
			"content":     []any{map[string]any{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn", "stop_sequence": nil, "stop_details": nil,
			"usage": map[string]any{
				"input_tokens": 2, "output_tokens": 1,
				"cache_creation_input_tokens": 10, "cache_read_input_tokens": 0,
				"cache_creation":  map[string]any{"ephemeral_5m_input_tokens": 10, "ephemeral_1h_input_tokens": 0},
				"server_tool_use": map[string]any{"web_search_requests": 0, "web_fetch_requests": 0},
				"service_tier":    "standard", "inference_geo": "global", "iterations": []any{}, "speed": "standard",
			},
		})
		return record
	}

	require.ErrorContains(t, ValidateDeliveryFidelity(record(map[string]any{
		"model": DefaultPublicModel, "max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "system", "content": "reminder"},
		},
	}), DefaultPublicModel), "not a Messages API role")

	require.ErrorContains(t, ValidateDeliveryFidelity(record(map[string]any{
		"model": DefaultPublicModel, "max_tokens": 1024,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"name": ""}},
	}), DefaultPublicModel), "has no name")

	// Conversion runs ahead of the validator, so a foreign tool reaching it
	// means the conversion missed one; the gate fails closed rather than
	// shipping the originating client's tool surface.
	require.ErrorContains(t, ValidateDeliveryFidelity(record(map[string]any{
		"model": DefaultPublicModel, "max_tokens": 1024,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"name": "apply_patch"}},
	}), DefaultPublicModel), "not a Claude Code tool")
}

func TestClaudeCodeToolSetViolation(t *testing.T) {
	accepted := [][]any{
		{map[string]any{"name": "Bash"}, map[string]any{"name": "Read"}},
		{map[string]any{"name": "mcp__plugin_figma_figma__whoami"}},
		{map[string]any{"name": "web_search"}},
	}
	for _, tools := range accepted {
		require.NoError(t, claudeCodeToolSetViolation(mustJSON(tools)), "%v", tools)
	}

	// The three foreign cohorts measured in the corpus, plus the empty name the
	// Messages API rejects outright.
	rejected := map[string][]any{
		"codex cli":  {map[string]any{"name": "Bash"}, map[string]any{"name": "apply_patch"}},
		"codex app":  {map[string]any{"name": "codex_app"}},
		"codex exec": {map[string]any{"name": "exec_command"}},
		"platform":   {map[string]any{"name": "music_generate"}},
		"empty name": {map[string]any{"name": ""}},
	}
	for name, tools := range rejected {
		t.Run(name, func(t *testing.T) {
			require.Error(t, claudeCodeToolSetViolation(mustJSON(tools)))
		})
	}

	// A request without tools is ordinary and must pass.
	require.NoError(t, claudeCodeToolSetViolation(nil))
}

// Folding is part of the shared normalizer, so ingest, hourly export and
// offline rebuild all converge on the same delivered shape.
func TestNormalizeProjectionFidelityFoldsSystemRoleMessages(t *testing.T) {
	request := mustJSON(map[string]any{
		"model": DefaultPublicModel, "max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "hi"},
			}},
			map[string]any{"role": "system", "content": "Plan mode is active."},
		},
	})
	response := mustJSON(map[string]any{
		"id": "msg_x", "type": "message", "role": "assistant", "model": DefaultPublicModel,
		"content":     []any{map[string]any{"type": "text", "text": "ok"}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
	})

	normalizedRequest, _, stats, err := normalizeProjectionFidelity(request, response, fidelityNormalizationOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.SystemRoleMessagesFolded)
	require.NotContains(t, string(normalizedRequest), `"role":"system"`)
	require.Contains(t, string(unescapeJSONHTML(normalizedRequest)), "<system-reminder>")
}
