package sessiondelivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Each phrasing below was measured in the captured corpus.
func TestScrubClientIdentityRemovesHarnessScaffolding(t *testing.T) {
	cases := []struct{ before, after string }{
		{"The following is the Codex agent history added since your last approval.",
			"The following is the agent history added since your last approval."},
		{"The Codex agent has requested the following next action:",
			"The agent has requested the following next action:"},
		{"Reviewed Codex session id: 019ecb42-17a4-7993-a074-be",
			"Reviewed session id: 019ecb42-17a4-7993-a074-be"},
		{"## My request for Codex:\n今天的操作", "## My request:\n今天的操作"},
		{"# Files m…77 tokens truncated…r Codex:\n", "# Files m…77 tokens truncated…r:\n"},
		{"## codex-clipboard-97467026.jpg: /var/T/codex-clipboard-97467026.jpg",
			"## clipboard-97467026.jpg: /var/T/clipboard-97467026.jpg"},
		{"# Selected Browser - Name: Codex In-app Browser - Type: iab",
			"# Selected Browser - Name: In-app Browser - Type: iab"},
		{"Apply a patch using the Codex apply_patch format or unified git diff",
			"Apply a patch using the apply_patch format or unified git diff"},
	}
	for _, testCase := range cases {
		require.Error(t, validateClientIdentity([]byte(testCase.before)), testCase.before)

		scrubbed, count := scrubClientIdentity([]byte(testCase.before))
		require.Positive(t, count)
		require.Equal(t, testCase.after, string(scrubbed))
		require.NoError(t, validateClientIdentity(scrubbed))
	}
}

// The fingerprints reach tool-call inputs and tool results, which the earlier
// text-block traversal never visited. Scrubbing the encoded bytes reaches every
// one, and reaches exactly what the validator inspects.
func TestScrubClientIdentityReachesToolInputsAndResults(t *testing.T) {
	document := []byte(`{"messages":[{"role":"assistant","content":[` +
		`{"type":"tool_use","name":"Bash","input":{"command":"cat C:\\Users\\a\\plugins\\cache\\openai-bundled\\browser\\x.js"}}]},` +
		`{"role":"user","content":[{"type":"tool_result","content":"# Selected Browser - Name: Codex In-app Browser"}]}]}`)
	require.Error(t, validateClientIdentity(document))

	scrubbed, count := scrubClientIdentity(document)
	require.Equal(t, int64(2), count)
	require.NoError(t, validateClientIdentity(scrubbed))
	require.Contains(t, string(scrubbed), `cache\\bundled\\browser`)
	require.Contains(t, string(scrubbed), "Name: In-app Browser")
}

// A filename referenced several times has to come out the same every time, or
// the turn stops referring to the file it names.
func TestScrubClientIdentityRenamesEveryOccurrenceConsistently(t *testing.T) {
	document := []byte("## codex-clipboard-abc.jpg: /tmp/codex-clipboard-abc.jpg\nRead /tmp/codex-clipboard-abc.jpg")
	scrubbed, count := scrubClientIdentity(document)
	require.Equal(t, int64(3), count)
	require.Equal(t, 3, strings.Count(string(scrubbed), "clipboard-abc.jpg"))
	require.NotContains(t, string(scrubbed), "codex-clipboard")
}

// MEASURED: .codex/ occurs in 185 records carrying a claude-cli user agent, so
// it reports which tools are installed rather than which one sent the request.
// A user directory called Codex and a Claude Code skill catalogue listing a
// Codex-delegation skill are neutral for the same reason.
func TestScrubClientIdentityLeavesUserEnvironmentAndClaudeCodeContextAlone(t *testing.T) {
	neutral := []string{
		"sed -n '1,320p' /Users/xiazhi/.codex/skills/brainstorming/SKILL.md",
		"rollout_path=/Users/xiazhi/.codex/sessions/2026/08/13/rollout-2026-08-13T19-54-39",
		`C:\Users\Administrator\.codex\generated_images\abc`,
		"/Users/x/Documents/ObsidianVaults/Codex/.claude/settings.json",
		"- codex:codex-rescue: Proactively use when Claude Code is stuck, or should hand a " +
			"substantial coding task to Codex through the shared runtime (Tools: Bash)",
		"- codebase-design\n- codex\n- design-an-interface",
		"codex-build: Hand a frozen spec (PLAN.md) to OpenAI Codex to IMPLEMENT",
		"iterates fixes via the SAME Codex session up to MAX_FIX_ROUNDS before taking over",
		"Claude Code (builder) and OpenAI Codex (read-only critic) tag-team an implementation",
		"- codex:setup: Check whether the local Codex CLI is ready and optionally toggle the review",
	}
	for _, text := range neutral {
		scrubbed, count := scrubClientIdentity([]byte(text))
		require.Zero(t, count, text)
		require.Equal(t, text, string(scrubbed))
		require.NoError(t, validateClientIdentity([]byte(text)), text)
	}
}

// Editing what a person typed is out of scope, so the record is held back.
func TestCountHumanClientMentionsHoldsBackUserProse(t *testing.T) {
	request := map[string]json.RawMessage{
		"messages": mustJSON([]any{map[string]any{
			"role":    "user",
			"content": "如果我直接在codex对话框跟你对话，说了我的操作，需要你自动帮我补齐这个文档",
		}}),
	}
	require.Equal(t, int64(1), countHumanClientMentions(request, nil))

	// The prose is left exactly as the user wrote it.
	before := string(request["messages"])
	scrubbed, count := scrubClientIdentity(request["messages"])
	require.Zero(t, count)
	require.Equal(t, before, string(scrubbed))

	clean := map[string]json.RawMessage{
		"messages": mustJSON([]any{map[string]any{"role": "user", "content": "帮我看一下这个文档"}}),
	}
	require.Zero(t, countHumanClientMentions(clean, nil))
}

// A wording inside the measured family that the table does not cover survives
// to hold the record back, rather than being guessed at.
func TestValidateClientIdentityCatchesUnknownFingerprints(t *testing.T) {
	for _, text := range []string{
		"The Codex agent history format changed in this build",
		"Restored Codex session id 019ecb42 from disk",
		"Opened the Codex In-app Viewer instead",
		"Uses Codex apply_patch semantics",
		"awaiting a request for Codex to continue",
	} {
		scrubbed, count := scrubClientIdentity([]byte(text))
		require.Zero(t, count, "an unmeasured wording must not be rewritten: %s", text)
		require.ErrorContains(t, validateClientIdentity(scrubbed), "client fingerprint", text)
	}
}

// A literal that appears inside a longer token is still the client's name, so
// replacing the substring is the intended outcome.
func TestScrubClientIdentityHandlesLiteralsInsideLongerTokens(t *testing.T) {
	scrubbed, count := scrubClientIdentity([]byte("loaded from openai-bundled-v2 cache"))
	require.Equal(t, int64(1), count)
	require.Equal(t, "loaded from bundled-v2 cache", string(scrubbed))
	require.NoError(t, validateClientIdentity(scrubbed))
}

func TestScrubClientIdentityIsIdempotent(t *testing.T) {
	document := []byte("The Codex agent has requested the following next action:\n" +
		"## My request for Codex:\n## codex-clipboard-abc.jpg\nName: Codex In-app Browser")

	first, count := scrubClientIdentity(document)
	require.Equal(t, int64(4), count)

	second, again := scrubClientIdentity(first)
	require.Zero(t, again)
	require.Equal(t, string(first), string(second))
	require.NoError(t, validateClientIdentity(second))
}

// End to end through the normalizer: measured user-side scaffolding is cleaned,
// but assistant prose is preserved and the record is marked for hold-back.
func TestNormalizeProjectionFidelityScopesClientIdentityCleanup(t *testing.T) {
	request := mustJSON(map[string]any{
		"model":      DefaultPublicModel,
		"max_tokens": 64000,
		"thinking":   map[string]any{"type": "adaptive", "display": "omitted"},
		"messages": []any{map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": "The Codex agent has requested a review"}},
		}},
	})
	response := mustJSON(map[string]any{
		"id":          "msg_01" + "abcdefghijklmnopqrstuv",
		"type":        "message",
		"role":        "assistant",
		"model":       DefaultPublicModel,
		"content":     []any{map[string]any{"type": "text", "text": "Opened the Codex In-app Browser"}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 20},
	})

	normalizedRequest, normalizedResponse, stats, err := normalizeProjectionFidelity(
		request, response, fidelityNormalizationOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.ClientIdentityScrubbed)
	require.Positive(t, stats.ForeignModelSelfClaims)
	require.Contains(t, string(normalizedRequest), "The agent has requested a review")
	require.Contains(t, string(normalizedResponse), "Opened the Codex In-app Browser")
	require.Error(t, validateClientIdentity(normalizedRequest, normalizedResponse))
}

func TestNormalizeClientIdentityCleansClientArtifactsWithoutRewritingAssistantText(t *testing.T) {
	request := map[string]json.RawMessage{
		"system": mustJSON("The following is the Codex agent history"),
		"tools": mustJSON([]any{map[string]any{
			"name":         "Browser",
			"description":  "Uses the Codex In-app Browser",
			"input_schema": map[string]any{"type": "object"},
		}}),
		"messages": mustJSON([]any{
			map[string]any{"role": "user", "content": "## My request for Codex:\nread codex-clipboard-a.png"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "Opened the Codex In-app Browser"},
				map[string]any{"type": "tool_use", "id": "toolu_x", "name": "Bash", "input": map[string]any{"command": "cat /tmp/codex-clipboard-a.png"}},
			}},
		}),
	}
	response := map[string]json.RawMessage{
		"content": mustJSON([]any{
			map[string]any{"type": "text", "text": "The Codex agent has requested approval"},
			map[string]any{"type": "tool_use", "id": "toolu_y", "name": "Bash", "input": map[string]any{"command": "ls openai-bundled"}},
		}),
	}

	scrubbed, err := normalizeClientIdentity(request, response)
	require.NoError(t, err)
	require.Equal(t, int64(6), scrubbed)
	require.NotContains(t, string(request["system"]), "Codex agent history")
	require.NotContains(t, string(request["tools"]), "Codex In-app")
	require.Contains(t, string(request["messages"]), "Opened the Codex In-app Browser")
	require.Contains(t, string(request["messages"]), "/tmp/clipboard-a.png")
	require.Contains(t, string(response["content"]), "The Codex agent has requested approval")
	require.Contains(t, string(response["content"]), "ls bundled")
	require.Equal(t, int64(2), countClientIdentityFingerprints(request, response))
}
