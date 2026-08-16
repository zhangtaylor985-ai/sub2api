package sessiondelivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func turnRequest(text string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"messages": mustJSON([]any{map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": text}},
		}}),
	}
}

func firstTurnText(t *testing.T, request map[string]json.RawMessage) string {
	t.Helper()
	var messages []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(request["messages"], &messages))
	require.NotEmpty(t, messages)
	require.NotEmpty(t, messages[0].Content)
	return messages[0].Content[0].Text
}

// Each phrasing below was measured in the captured corpus.
func TestScrubClientIdentityRemovesHarnessScaffolding(t *testing.T) {
	cases := []struct{ before, after string }{
		{"The following is the Codex agent history added since your last approval.",
			"The following is the agent history added since your last approval."},
		{"The Codex agent has requested the following next action:",
			"The agent has requested the following next action:"},
		{"Reviewed Codex session id: 019ecb42-17a4-7993-a074-be",
			"Reviewed session id: 019ecb42-17a4-7993-a074-be"},
		{"## My request for Codex:\n今天的操作",
			"## My request:\n今天的操作"},
		{"# Files m…77 tokens truncated…r Codex:\n",
			"# Files m…77 tokens truncated…r:\n"},
		{"## codex-clipboard-97467026-2044.jpg: /var/folders/T/codex-clipboard-97467026-2044.jpg",
			"## clipboard-97467026-2044.jpg: /var/folders/T/clipboard-97467026-2044.jpg"},
	}
	for _, testCase := range cases {
		request := turnRequest(testCase.before)
		require.ErrorContains(t, validateClientIdentity(request), "client fingerprint")

		scrubs, err := scrubClientIdentity(request)
		require.NoError(t, err)
		require.Equal(t, int64(1), scrubs)
		require.Equal(t, testCase.after, firstTurnText(t, request))
		require.NoError(t, validateClientIdentity(request))
	}
}

// A filename referenced twice in one record has to come out the same both
// times, or the turn stops referring to the file it names.
func TestScrubClientIdentityRenamesEveryOccurrenceConsistently(t *testing.T) {
	text := "## codex-clipboard-abc.jpg: /tmp/codex-clipboard-abc.jpg\nRead /tmp/codex-clipboard-abc.jpg"
	request := turnRequest(text)

	_, err := scrubClientIdentity(request)
	require.NoError(t, err)

	got := firstTurnText(t, request)
	require.Equal(t, 3, strings.Count(got, "clipboard-abc.jpg"))
	require.NotContains(t, got, "codex-clipboard")
}

func TestScrubClientIdentityCleansToolDescriptions(t *testing.T) {
	request := map[string]json.RawMessage{
		"tools": mustJSON([]any{map[string]any{
			"name":         "mcp__idea__apply_patch",
			"description":  "Apply a patch using the Codex apply_patch format or unified git diff format.",
			"input_schema": map[string]any{"type": "object"},
		}}),
	}
	require.ErrorContains(t, validateClientIdentity(request), "client fingerprint")

	scrubs, err := scrubClientIdentity(request)
	require.NoError(t, err)
	require.Equal(t, int64(1), scrubs)
	require.NoError(t, validateClientIdentity(request))
	require.Contains(t, toolDescription(t, request), "Apply a patch using the apply_patch format")
}

// A user directory called Codex appears in genuine Claude Code records, and a
// Claude Code skill catalogue lists a skill for delegating to Codex. Neither
// says which client sent the request.
func TestScrubClientIdentityLeavesNeutralMentionsAlone(t *testing.T) {
	neutral := []string{
		"/Users/x/Documents/ObsidianVaults/Codex/.claude/settings.json",
		"- codex:codex-rescue: Proactively use when Claude Code is stuck, or should hand a " +
			"substantial coding task to Codex through the shared runtime (Tools: Bash)",
		"- codebase-design\n- codex\n- design-an-interface",
		"- codex:codex-cli-runtime\n- codex:codex-result-handling\n- codex:gpt-5-4-prompting",
		"codex-build: Hand a frozen spec (PLAN.md) to OpenAI Codex to IMPLEMENT with full write access",
		"iterates fixes via the SAME Codex session up to MAX_FIX_ROUNDS before taking over",
		"the exact role-flip of /codex-review. Codex builds from the spec in a --yolo sandbox",
		"Claude Code (builder) and OpenAI Codex (read-only critic) tag-team an implementation",
		"right after a plan survives /grill-me-codex, /grill-with-docs-codex, or /codex-review",
		// A skill that checks for the client is not the client speaking.
		"- codex:setup: Check whether the local Codex CLI is ready and optionally toggle the review",
		"- codex:rescue: hand the task back to the Codex rescue subagent",
	}
	for _, text := range neutral {
		request := turnRequest(text)
		scrubs, err := scrubClientIdentity(request)
		require.NoError(t, err)
		require.Zero(t, scrubs, text)
		require.Equal(t, text, firstTurnText(t, request))
		require.NoError(t, validateClientIdentity(request), text)
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
	_, err := scrubClientIdentity(request)
	require.NoError(t, err)
	require.Equal(t, before, string(request["messages"]))

	clean := turnRequest("帮我看一下这个文档")
	require.Zero(t, countHumanClientMentions(clean, nil))
}

// A wording inside the measured family that the replacement table does not
// cover survives to hold the record back, rather than being guessed at.
func TestValidateClientIdentityCatchesUnknownFingerprints(t *testing.T) {
	for _, text := range []string{
		"Opened the Codex In-app Browser to continue",
		"Loaded /Users/x/.codex/plugins/cache/bundled/browser",
		"The Codex agent history format changed in this build",
		"Restored Codex session id 019ecb42 from disk",
	} {
		request := turnRequest(text)
		scrubs, err := scrubClientIdentity(request)
		require.NoError(t, err)
		require.Zero(t, scrubs, "an unmeasured wording must not be rewritten: %s", text)
		require.ErrorContains(t, validateClientIdentity(request), "client fingerprint", text)
	}
}

func TestScrubClientIdentityIsIdempotent(t *testing.T) {
	request := turnRequest("The Codex agent has requested the following next action:\n" +
		"## My request for Codex:\n## codex-clipboard-abc.jpg")

	scrubs, err := scrubClientIdentity(request)
	require.NoError(t, err)
	require.Equal(t, int64(1), scrubs)
	first := string(request["messages"])

	scrubs, err = scrubClientIdentity(request)
	require.NoError(t, err)
	require.Zero(t, scrubs)
	require.Equal(t, first, string(request["messages"]))
}
