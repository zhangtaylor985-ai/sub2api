package sessiondelivery

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// A delivered record is published as a Claude Opus 5 conversation. Which client
// produced it is not part of that claim, but a client that stamps its own name
// into the turn text contradicts it in the same way a foreign model name would.
//
// Only client-generated text is rewritten: the scaffolding an agent harness
// injects into a turn, and the temporary filenames the client creates. Text a
// person typed is never edited — a record whose prose names the client is held
// back instead, the same way a record is held back when the assistant's own
// prose names a foreign model.
//
// Not every mention is a client fingerprint. A user directory that happens to
// be called Codex appears in genuine Claude Code records too, and a Claude Code
// skill catalogue naturally lists a skill for delegating work to Codex. Neither
// says anything about which client sent the request, so both are left alone;
// clientIdentityPattern is written tightly enough not to match them.

// clientIdentityReplacements are measured literals. Replacing exact text keeps
// removal from reaching further than what was verified, and keeps the sentence
// readable rather than leaving a gap where the product name was.
var clientIdentityReplacements = []struct{ from, to string }{
	{"The following is the Codex agent history", "The following is the agent history"},
	{"The Codex agent has requested", "The agent has requested"},
	{"Reviewed Codex session id", "Reviewed session id"},
	{"My request for Codex:", "My request:"},
	// The harness truncates long turns mid-word, leaving the tail of
	// "...for Codex:" behind.
	{"truncated\u2026r Codex:", "truncated\u2026r:"},
	{"codex-clipboard-", "clipboard-"},
	{"Codex apply_patch format", "apply_patch format"},
}

var (
	// clientIdentityPattern DETECTS the family the replacements belong to, so a
	// wording the table does not cover holds its record back instead of
	// shipping.
	//
	// Every alternative below was measured as client scaffolding. Guessing at
	// further phrasings is what makes this kind of check misfire: "Codex CLI"
	// looks like a fingerprint but occurs in a Claude Code skill description
	// ("check whether the local Codex CLI is ready"), which says nothing about
	// who sent the request.
	clientIdentityPattern = regexp.MustCompile(
		`(?i)Codex agent history|Codex agent has requested|Codex session id` +
			`|request for Codex|\x{2026}r Codex:|codex-clipboard|\.codex/` +
			`|Codex In-app|Codex apply_patch`)

	// humanClientMention matches a person naming their client in their own
	// words. Editing that would mean rewriting what the user said.
	humanClientMention = regexp.MustCompile(`(?i)codex\s*对话框|codex\s*(?:dialog|chat) box`)
)

// scrubClientIdentityText removes the client's name from its own scaffolding.
func scrubClientIdentityText(text string) (string, bool) {
	rewritten := text
	for _, replacement := range clientIdentityReplacements {
		rewritten = strings.ReplaceAll(rewritten, replacement.from, replacement.to)
	}
	return rewritten, rewritten != text
}

// scrubClientIdentity rewrites client scaffolding across the request.
func scrubClientIdentity(request map[string]json.RawMessage) (int64, error) {
	return rewriteRequestText(request, scrubClientIdentityText)
}

// countHumanClientMentions reports turns where a person named the client.
func countHumanClientMentions(request, response map[string]json.RawMessage) int64 {
	var mentions int64
	for _, text := range conversationProse(request, response) {
		mentions += int64(len(humanClientMention.FindAllString(text, -1)))
	}
	return mentions
}

// validateClientIdentity fails closed when request text still carries a client
// fingerprint the replacement table did not recognize.
func validateClientIdentity(request map[string]json.RawMessage) error {
	for _, text := range requestModelNameTexts(request) {
		if match := clientIdentityPattern.FindString(text); match != "" {
			return fmt.Errorf("request text carries a client fingerprint: %q", match)
		}
	}
	return nil
}
