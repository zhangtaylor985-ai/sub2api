package sessiondelivery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func budgetFixture(maxTokens, outputTokens int) (map[string]json.RawMessage, map[string]json.RawMessage) {
	request := map[string]json.RawMessage{
		"model":      mustJSON(DefaultPublicModel),
		"max_tokens": mustJSON(maxTokens),
	}
	response := map[string]json.RawMessage{
		"stop_reason": mustJSON("end_turn"),
		"usage":       mustJSON(map[string]any{"output_tokens": outputTokens}),
	}
	return request, response
}

// A client probe sending max_tokens=1 was paired with a full projected
// response, a combination the real API cannot produce.
func TestAlignRequestTokenBudgetRaisesOutrunBudget(t *testing.T) {
	request, response := budgetFixture(1, 49)
	require.ErrorContains(t, validateRequestTokenBudget(request, response), "exceeds request max_tokens")

	raised, err := alignRequestTokenBudget(request, response)
	require.NoError(t, err)
	require.Equal(t, int64(1), raised)
	require.JSONEq(t, `64000`, string(request["max_tokens"]))
	require.NoError(t, validateRequestTokenBudget(request, response))

	// Re-running must not move the budget again.
	raised, err = alignRequestTokenBudget(request, response)
	require.NoError(t, err)
	require.Zero(t, raised)
	require.JSONEq(t, `64000`, string(request["max_tokens"]))
}

// A budget the response stayed within is authentic client input.
func TestAlignRequestTokenBudgetLeavesHonouredBudgets(t *testing.T) {
	for _, testCase := range []struct{ budget, output int }{
		{64000, 49},
		{8192, 8192},
		{100000, 70000},
		{1, 1},
	} {
		request, response := budgetFixture(testCase.budget, testCase.output)
		raised, err := alignRequestTokenBudget(request, response)
		require.NoError(t, err)
		require.Zero(t, raised)
		require.JSONEq(t, string(mustJSON(testCase.budget)), string(request["max_tokens"]))
		require.NoError(t, validateRequestTokenBudget(request, response))
	}
}

// Raising to the Claude Code ceiling cannot fix a response that outran it, so
// the record is left for the validator to hold back rather than being given a
// budget no real client sends.
func TestAlignRequestTokenBudgetLeavesResponsesBeyondClaudeCodeCeiling(t *testing.T) {
	request, response := budgetFixture(1, claudeCodeTokenBudget+1)

	raised, err := alignRequestTokenBudget(request, response)
	require.NoError(t, err)
	require.Zero(t, raised)
	require.JSONEq(t, `1`, string(request["max_tokens"]))
	require.ErrorContains(t, validateRequestTokenBudget(request, response), "exceeds request max_tokens")
}

// Unknown future budgets are not rewritten on a guess. The validator keeps the
// fail-closed boundary until that exact shape has been measured and approved.
func TestAlignRequestTokenBudgetLeavesUnmeasuredContradictions(t *testing.T) {
	request, response := budgetFixture(32, 49)

	raised, err := alignRequestTokenBudget(request, response)
	require.NoError(t, err)
	require.Zero(t, raised)
	require.JSONEq(t, `32`, string(request["max_tokens"]))
	require.ErrorContains(t, validateRequestTokenBudget(request, response), "exceeds request max_tokens")
}

// A request without a budget declares no ceiling to contradict.
func TestRequestTokenBudgetToleratesAbsentAndMalformedBudgets(t *testing.T) {
	request := map[string]json.RawMessage{"model": mustJSON(DefaultPublicModel)}
	response := map[string]json.RawMessage{"usage": mustJSON(map[string]any{"output_tokens": 49})}
	raised, err := alignRequestTokenBudget(request, response)
	require.NoError(t, err)
	require.Zero(t, raised)
	require.NoError(t, validateRequestTokenBudget(request, response))

	request["max_tokens"] = json.RawMessage(`"64000"`)
	_, err = alignRequestTokenBudget(request, response)
	require.ErrorContains(t, err, "decode request max_tokens")
	require.ErrorContains(t, validateRequestTokenBudget(request, response), "decode request max_tokens")
}

// The 2026-08-16 failed hour carried max_tokens=8 with usage.output_tokens=11.
// Exercise the shared exporter/rebuild pass and its second replay so the
// repair is exact and archive rebuilds remain byte-idempotent.
func TestNormalizeProjectionFidelityRepairsHistoricalEightTokenContradictionIdempotently(t *testing.T) {
	request := mustJSON(map[string]any{
		"model":      DefaultPublicModel,
		"max_tokens": 8,
		"thinking":   map[string]any{"type": "adaptive", "display": "omitted"},
		"messages": []any{
			map[string]any{"role": "user", "content": "count"},
		},
	})
	response := mustJSON(map[string]any{
		"id":          "msg_01" + "abcdefghijklmnopqrstuv",
		"type":        "message",
		"role":        "assistant",
		"model":       DefaultPublicModel,
		"content":     []any{map[string]any{"type": "text", "text": "What would you like me to count?"}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 2, "output_tokens": 11},
	})

	normalizedRequest, normalizedResponse, stats, err := normalizeProjectionFidelity(
		request, response, fidelityNormalizationOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.RequestTokenBudgetRaised)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(normalizedRequest, &decoded))
	require.JSONEq(t, `64000`, string(decoded["max_tokens"]))
	normalizedResponseObject, err := decodeJSONObject(normalizedResponse, "response.response_data")
	require.NoError(t, err)
	require.NoError(t, validateRequestTokenBudget(decoded, normalizedResponseObject))

	replayedRequest, replayedResponse, replayedStats, err := normalizeProjectionFidelity(
		normalizedRequest, normalizedResponse, fidelityNormalizationOptions{},
	)
	require.NoError(t, err)
	require.Zero(t, replayedStats.RequestTokenBudgetRaised)
	require.Equal(t, normalizedRequest, replayedRequest)
	require.Equal(t, normalizedResponse, replayedResponse)
}
