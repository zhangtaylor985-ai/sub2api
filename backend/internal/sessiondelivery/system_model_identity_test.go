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
		{"opus56", "You are powered by the model named Opus 5.6. The exact model ID is claude-opus-5-6."},
		{"gpt", "You are powered by the model gpt-5.5."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			prompt := testCase.line + "\n - Assistant knowledge cutoff is January 2026.\n - Claude Code is available as a CLI."
			system, rewrites, err := normalizeSystemModelIdentity(mustJSON(prompt))
			require.NoError(t, err)
			require.Equal(t, int64(1), rewrites.BlocksRewritten)

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
		require.Zero(t, rewrites.BlocksRewritten)
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
	require.Equal(t, int64(1), rewrites.BlocksRewritten)
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
	require.Equal(t, int64(1), rewrites.BlocksRewritten)

	second, again, err := normalizeSystemModelIdentity(first)
	require.NoError(t, err)
	require.Zero(t, again.BlocksRewritten)
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
	require.Equal(t, int64(1), rewrites.BlocksRewritten)
	require.Contains(t, string(normalized), "May 2026")
	require.NoError(t, validateSystemModelIdentity(normalized))
}

// A prompt that never names a model is left alone rather than having one added.
func TestNormalizeSystemModelIdentityIgnoresPromptsWithoutModelLine(t *testing.T) {
	system := mustJSON("You are Claude Code, Anthropic's official CLI for Claude.")
	normalized, rewrites, err := normalizeSystemModelIdentity(system)
	require.NoError(t, err)
	require.Zero(t, rewrites.BlocksRewritten)
	require.Equal(t, string(system), string(normalized))
	require.NoError(t, validateSystemModelIdentity(system))
}

// Exercising the shipped literal keeps the fixture from drifting away from
// what removal actually matches.
var foreignTierParagraph = foreignModelTierParagraphs[0]

// The literal has to stay byte-identical to what was measured, since removal
// matches it exactly and a stray edit would silently stop removing anything.
func TestForeignModelTierParagraphLiteralIsTheMeasuredText(t *testing.T) {
	require.Len(t, foreignModelTierParagraphs, 1)
	require.Len(t, foreignModelTierParagraphs[0], 694)
	require.True(t, strings.HasPrefix(foreignModelTierParagraphs[0], "This iteration of Claude is Claude Fable 5,"))
	require.True(t, strings.HasSuffix(foreignModelTierParagraphs[0], "claude-fable-5-mythos-5 for more information."))
}

// tierPrompt reproduces the captured layout: prose, then the tier paragraph,
// then a following section, all separated by blank lines.
func tierPrompt(paragraph string) string {
	return "Report outcomes faithfully.\n\n" +
		"You are powered by the model named Fable 5. The exact model ID is claude-fable-5.\n" +
		" - Assistant knowledge cutoff is January 2026.\n\n" +
		paragraph + "\n\n" +
		"# Session-specific guidance\n - When the user types `/<skill-name>`, invoke it via Skill."
}

// Rewriting only the identity line left the paragraph below it still naming
// Fable 5, so the prompt contradicted itself. The paragraph goes as a unit.
func TestNormalizeSystemModelIdentityStripsForeignModelTierParagraph(t *testing.T) {
	system := mustJSON(tierPrompt(foreignTierParagraph))

	normalized, rewrites, err := normalizeSystemModelIdentity(system)
	require.NoError(t, err)
	require.Equal(t, int64(1), rewrites.BlocksRewritten)

	var got string
	require.NoError(t, json.Unmarshal(normalized, &got))
	require.Contains(t, got, "You are powered by the model named Opus 5. The exact model ID is claude-opus-5.")
	require.Contains(t, got, "Assistant knowledge cutoff is May 2026.")
	// Nothing that names the backing model may survive.
	require.NotContains(t, got, "Fable")
	require.NotContains(t, got, "Mythos")
	require.NotContains(t, got, "This iteration of Claude is")
	require.NotContains(t, got, "claude-fable-5-mythos-5")
	// The surrounding prompt keeps its shape: neighbouring sections survive and
	// stay separated by exactly one blank line where the paragraph used to be.
	require.Contains(t, got, "Report outcomes faithfully.")
	require.Contains(t, got, "# Session-specific guidance")
	require.Contains(t, got, "cutoff is May 2026.\n\n# Session-specific guidance")
	require.NotContains(t, got, "\n\n\n")
	require.NoError(t, validateSystemModelIdentity(normalized))
}

// A paragraph that introduces the delivered model is authentic client text.
func TestNormalizeSystemModelIdentityKeepsAuthenticOpus5TierParagraph(t *testing.T) {
	paragraph := "This iteration of Claude is Claude Opus 5, the most capable model in " +
		"Anthropic's Claude 5 family."
	prompt := "Report outcomes faithfully.\n\n" +
		canonicalModelIdentityLine() + "\n - Assistant knowledge cutoff is May 2026.\n\n" +
		paragraph + "\n\n# Session-specific guidance"

	normalized, rewrites, err := normalizeSystemModelIdentity(mustJSON(prompt))
	require.NoError(t, err)
	require.Zero(t, rewrites.BlocksRewritten)
	require.JSONEq(t, string(mustJSON(prompt)), string(normalized))
	require.NoError(t, validateSystemModelIdentity(normalized))
}

func TestValidateSystemModelIdentityRejectsForeignTierParagraph(t *testing.T) {
	// The identity line already agrees with the delivered model, so only the
	// paragraph check can catch this record.
	prompt := canonicalModelIdentityLine() + "\n - Assistant knowledge cutoff is May 2026.\n\n" +
		foreignTierParagraph + "\n\n# Session-specific guidance"
	require.ErrorContains(t, validateSystemModelIdentity(mustJSON(prompt)), "introduces a foreign model")

	// The paragraph is also caught without any identity line present.
	alone := mustJSON(foreignTierParagraph)
	require.ErrorContains(t, validateSystemModelIdentity(alone), "introduces a foreign model")

	stripped, rewrites, err := normalizeSystemModelIdentity(alone)
	require.NoError(t, err)
	require.Equal(t, int64(1), rewrites.BlocksRewritten)
	require.NoError(t, validateSystemModelIdentity(stripped))

	for _, text := range []string{
		"This iteration of Claude is Claude Opus 5.6, a different model.",
		"This iteration of Claude is Claude Opus 5 Pro, a different model.",
	} {
		require.ErrorContains(t, validateSystemModelIdentity(mustJSON(text)), "introduces a foreign model", text)
	}
}

func TestNormalizeSystemModelIdentityTierParagraphIsIdempotent(t *testing.T) {
	system := mustJSON(tierPrompt(foreignTierParagraph))

	first, rewrites, err := normalizeSystemModelIdentity(system)
	require.NoError(t, err)
	require.Equal(t, int64(1), rewrites.BlocksRewritten)

	second, again, err := normalizeSystemModelIdentity(first)
	require.NoError(t, err)
	require.Zero(t, again.BlocksRewritten)
	require.Equal(t, string(first), string(second))
}

// Removal matches measured text exactly. A variant is left in place so the
// record is held back, rather than having an unverified span deleted from it.
func TestNormalizeSystemModelIdentityHoldsBackUnknownTierParagraphVariants(t *testing.T) {
	variants := map[string]string{
		"unknown model":   "This iteration of Claude is Claude Aurora 7, a model that did not exist when this was measured.",
		"reworded":        "This iteration of Claude is Claude Fable 5, the newest model in the Claude 5 family.",
		"truncated":       foreignTierParagraph[:400],
		"extra sentence":  foreignTierParagraph + " An additional sentence Anthropic added later.",
		"no blank before": "Report outcomes faithfully.\n" + foreignTierParagraph,
	}
	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			prompt := "Report outcomes faithfully.\n\n" + variant + "\n\n# Session-specific guidance"
			normalized, rewrites, err := normalizeSystemModelIdentity(mustJSON(prompt))
			require.NoError(t, err)
			require.Zero(t, rewrites.ParagraphsStopped, "an unmeasured variant must not be removed")

			var got string
			if rewrites.BlocksRewritten > 0 {
				require.NoError(t, json.Unmarshal(normalized, &got))
			} else {
				got = prompt
			}
			// Nothing was cut out of the surrounding prompt.
			require.Contains(t, got, "Report outcomes faithfully.")
			require.Contains(t, got, "# Session-specific guidance")
			require.Contains(t, got, variant)

			// The record cannot ship, so the leftover has to be visible to both
			// the hold-back count and the validator.
			request := map[string]json.RawMessage{"system": mustJSON(got)}
			require.Positive(t, countForeignModelTierProse(request, nil))
			require.ErrorContains(t, validateSystemModelIdentity(mustJSON(got)), "foreign model")
		})
	}
}

// The removal pattern is anchored to the measured text, so it cannot run past
// the paragraph even when no blank line follows for a long stretch.
func TestNormalizeSystemModelIdentityRemovalStaysWithinTheParagraph(t *testing.T) {
	tail := strings.Repeat("Ordinary prompt guidance that must survive. ", 200)
	prompt := "Report outcomes faithfully.\n\n" + foreignTierParagraph + "\n" + tail

	normalized, rewrites, err := normalizeSystemModelIdentity(mustJSON(prompt))
	require.NoError(t, err)
	// One newline is not a paragraph break, so this occurrence is not the
	// measured shape and is held back rather than guessed at.
	require.Zero(t, rewrites.ParagraphsStopped)

	got := prompt
	if rewrites.BlocksRewritten > 0 {
		require.NoError(t, json.Unmarshal(normalized, &got))
	}
	require.Contains(t, got, tail)
}

// The paragraph is inert in request.system, but as a conversation turn the
// assistant answers it, so the record can only be held back.
func TestCountForeignModelTierProseFlagsUnrewritableTurns(t *testing.T) {
	request := map[string]json.RawMessage{
		"messages": mustJSON([]any{
			// The captured record sends the paragraph as a bare string.
			map[string]any{"role": "user", "content": foreignTierParagraph},
		}),
	}
	response := map[string]json.RawMessage{
		"content": mustJSON([]any{map[string]any{
			"type": "text",
			"text": "Understood — I'll treat Claude Fable 5 as the most advanced model.",
		}}),
	}
	require.Equal(t, int64(1), countForeignModelTierProse(request, response))

	// Block-array content is covered too.
	request["messages"] = mustJSON([]any{map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "text", "text": foreignTierParagraph}},
	}})
	require.Equal(t, int64(1), countForeignModelTierProse(request, response))

	// A turn introducing the delivered model is authentic and must not be flagged.
	request["messages"] = mustJSON([]any{map[string]any{
		"role":    "user",
		"content": "This iteration of Claude is Claude Opus 5, Anthropic's most capable model.",
	}})
	require.Zero(t, countForeignModelTierProse(request, response))

	// Ordinary turns are untouched.
	request["messages"] = mustJSON([]any{map[string]any{"role": "user", "content": "count"}})
	require.Zero(t, countForeignModelTierProse(request, response))
}

// The paragraph is dropped even when it closes the prompt with no trailing
// blank line to terminate it.
func TestNormalizeSystemModelIdentityStripsTierParagraphAtPromptEnd(t *testing.T) {
	system := mustJSON("Report outcomes faithfully.\n\n" + foreignTierParagraph)

	normalized, rewrites, err := normalizeSystemModelIdentity(system)
	require.NoError(t, err)
	require.Equal(t, int64(1), rewrites.BlocksRewritten)

	var got string
	require.NoError(t, json.Unmarshal(normalized, &got))
	require.Contains(t, got, "Report outcomes faithfully.")
	require.NotContains(t, got, "Fable")
	require.NoError(t, validateSystemModelIdentity(normalized))
}
