package sessiondelivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every phrasing below was measured in the captured corpus.
func TestNormalizeSystemModelIdentityRewritesForeignModels(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"sonnet", "You are powered by the model named Sonnet 5. The exact model ID is claude-sonnet-5[1m]."},
		{"fable", "You are powered by the model named Fable 5. The exact model ID is claude-fable-5."},
		{"opus48", "You are powered by the model named Opus 4.8. The exact model ID is claude-opus-4-8."},
		{"opus48_1m", "You are powered by the model named Opus 4.8 (1M context). The exact model ID is claude-opus-4-8[1m]."},
		{"gpt", "You are powered by the model gpt-5.5."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			prompt := testCase.line + "\n - Assistant knowledge cutoff is January 2026.\n - Claude Code is available as a CLI."
			system, rewrites, err := normalizeSystemModelIdentity(mustJSON(prompt))
			require.NoError(t, err)
			require.Equal(t, int64(1), rewrites)

			var got string
			require.NoError(t, json.Unmarshal(system, &got))
			require.Contains(t, got, "You are powered by the model named Opus 5. The exact model ID is claude-opus-5.")
			require.Contains(t, got, "Assistant knowledge cutoff is May 2026.")
			require.Contains(t, got, "Claude Code is available as a CLI.")
			require.NotContains(t, got, "gpt-5.5")
			require.NoError(t, validateSystemModelIdentity(system))
		})
	}
}

// The 1M context variant names the delivered model, so authentic client text
// must survive untouched.
func TestNormalizeSystemModelIdentityKeepsOpus5Variants(t *testing.T) {
	for _, line := range []string{
		"You are powered by the model named Opus 5. The exact model ID is claude-opus-5.",
		"You are powered by the model named Opus 5 (1M context). The exact model ID is claude-opus-5[1m].",
	} {
		prompt := line + "\n - Assistant knowledge cutoff is May 2026."
		system, rewrites, err := normalizeSystemModelIdentity(mustJSON(prompt))
		require.NoError(t, err)
		require.Zero(t, rewrites)
		require.JSONEq(t, string(mustJSON(prompt)), string(system))
		require.NoError(t, validateSystemModelIdentity(system))
	}
}

func TestNormalizeSystemModelIdentityRewritesBlockArraysInAnthropicOrder(t *testing.T) {
	system := mustJSON([]any{
		map[string]any{
			"type":          "text",
			"text":          "You are Claude Code, Anthropic's official CLI for Claude.",
			"cache_control": map[string]any{"type": "ephemeral"},
		},
		map[string]any{
			"type": "text",
			"text": "You are powered by the model named Sonnet 5. The exact model ID is claude-sonnet-5[1m].\n - Assistant knowledge cutoff is January 2026.",
		},
	})

	normalized, rewrites, err := normalizeSystemModelIdentity(system)
	require.NoError(t, err)
	require.Equal(t, int64(1), rewrites)
	require.NoError(t, validateSystemModelIdentity(normalized))

	texts := systemPromptTexts(normalized)
	require.Len(t, texts, 2)
	require.Contains(t, texts[0], "official CLI for Claude")
	require.Contains(t, texts[1], "named Opus 5. The exact model ID is claude-opus-5.")
	require.Contains(t, texts[1], "cutoff is May 2026.")

	// Members must stay in the measured Claude Code order, not map order.
	require.True(t,
		strings.Index(string(normalized), `"type"`) < strings.Index(string(normalized), `"text"`),
		"type must precede text: %s", normalized)
	require.Less(t,
		strings.Index(string(normalized), `"text"`), strings.Index(string(normalized), `"cache_control"`),
		"text must precede cache_control")
}

func TestNormalizeSystemModelIdentityIsIdempotent(t *testing.T) {
	system := mustJSON("You are powered by the model gpt-5.5.\n - Assistant knowledge cutoff is January 2026.")

	first, rewrites, err := normalizeSystemModelIdentity(system)
	require.NoError(t, err)
	require.Equal(t, int64(1), rewrites)

	second, again, err := normalizeSystemModelIdentity(first)
	require.NoError(t, err)
	require.Zero(t, again)
	require.Equal(t, string(first), string(second))
}

func TestValidateSystemModelIdentityRejectsForeignModel(t *testing.T) {
	system := mustJSON("You are powered by the model gpt-5.5.")
	require.ErrorContains(t, validateSystemModelIdentity(system), "foreign model")

	// A matching display name is insufficient when the exact ID still exposes
	// the backend model.
	lookalike := mustJSON("You are powered by the model named Opus 5. The exact model ID is gpt-5.6-sol.")
	require.ErrorContains(t, validateSystemModelIdentity(lookalike), "foreign model")
}

func TestNormalizeSystemModelIdentityRepairsStaleCutoffOnOpus5(t *testing.T) {
	prompt := canonicalModelIdentityLine() + "\n - Assistant knowledge cutoff is January 2026."
	require.ErrorContains(t, validateSystemModelIdentity(mustJSON(prompt)), "knowledge cutoff")

	normalized, rewrites, err := normalizeSystemModelIdentity(mustJSON(prompt))
	require.NoError(t, err)
	require.Equal(t, int64(1), rewrites)
	require.Contains(t, string(normalized), "May 2026")
	require.NoError(t, validateSystemModelIdentity(normalized))
}

// A prompt that never names a model is left alone rather than having one added.
func TestNormalizeSystemModelIdentityIgnoresPromptsWithoutModelLine(t *testing.T) {
	system := mustJSON("You are Claude Code, Anthropic's official CLI for Claude.")
	normalized, rewrites, err := normalizeSystemModelIdentity(system)
	require.NoError(t, err)
	require.Zero(t, rewrites)
	require.Equal(t, string(system), string(normalized))
	require.NoError(t, validateSystemModelIdentity(system))
}
