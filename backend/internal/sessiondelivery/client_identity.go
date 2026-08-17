package sessiondelivery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
)

// A delivered record is published as a Claude Opus 5 conversation. Which client
// produced it is not part of that claim, but a client that stamps its own name
// into the turn text contradicts it in the same way a foreign model name would.
//
// Only client-generated text is rewritten: the scaffolding an agent harness
// injects into a turn, and the names the client gives its own temporary files
// and bundled plugins. Text a person typed is never edited — a record whose
// prose names the client is held back instead, the same way a record is held
// back when the assistant's own prose names a foreign model.
//
// Whether a mention is a fingerprint is decided by measurement, not by how it
// reads. Across the captured corpus, 5617 records carry a claude-cli user agent
// and provably came from Claude Code. A string absent from all of them and
// present only elsewhere is a client artifact; a string both groups share is
// the user's environment. On that test "Codex In-app", "codex-clipboard" and
// "openai-bundled" are artifacts (0 occurrences among the 5617), while
// ".codex/" is not: 185 genuine Claude Code records reference it, because a
// home directory says which tools are installed, not which one sent the
// request. Treating it as a fingerprint produced 1016 false violations.
//
// Scrubbing and detection both run over the encoded bytes. The shapes below
// contain no character JSON escapes, so a byte replacement reaches tool
// results, tool-call inputs and assistant text alike — and, just as
// importantly, the two run over identical text, so nothing can be flagged in a
// place the scrubber never visits.

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
	{"codex-clipboard", "clipboard"},
	{"Codex In-app Browser", "In-app Browser"},
	{"Codex apply_patch format", "apply_patch format"},
	// The client's bundled plugin cache, which also carries its upstream vendor.
	{"openai-bundled", "bundled"},
	{"openai-primary-runtime", "primary-runtime"},
}

var (
	// clientIdentityPattern DETECTS the family the replacements belong to, so a
	// wording the table does not cover holds its record back instead of
	// shipping. Every alternative is measured as absent from the claude-cli
	// group; guessing is what makes this kind of check misfire, as "Codex CLI"
	// did by matching a Claude Code skill description.
	clientIdentityPattern = regexp.MustCompile(
		`(?i)Codex agent history|Codex agent has requested|Codex session id` +
			`|request for Codex|\x{2026}r Codex:|codex-clipboard` +
			`|Codex In-app|Codex apply_patch|openai-bundled|openai-primary-runtime`)

	// humanClientMention matches a person naming their client in their own
	// words. Editing that would mean rewriting what the user said.
	humanClientMention = regexp.MustCompile(`(?i)codex\s*对话框|codex\s*(?:dialog|chat) box`)
)

// scrubClientIdentity removes the client's name from its own scaffolding across
// an encoded document.
func scrubClientIdentity(encoded []byte) ([]byte, int64) {
	var scrubbed int64
	for _, replacement := range clientIdentityReplacements {
		from := []byte(replacement.from)
		count := bytes.Count(encoded, from)
		if count == 0 {
			continue
		}
		encoded = bytes.ReplaceAll(encoded, from, []byte(replacement.to))
		scrubbed += int64(count)
	}
	return encoded, scrubbed
}

// countHumanClientMentions reports turns where a person named the client.
func countHumanClientMentions(request, response map[string]json.RawMessage) int64 {
	var mentions int64
	for _, text := range conversationProse(request, response) {
		mentions += int64(len(humanClientMention.FindAllString(text, -1)))
	}
	return mentions
}

// validateClientIdentity fails closed when a document still carries a client
// fingerprint the replacement table did not recognize.
func validateClientIdentity(documents ...[]byte) error {
	for _, document := range documents {
		if match := clientIdentityPattern.Find(document); match != nil {
			return fmt.Errorf("delivery record carries a client fingerprint: %q", match)
		}
	}
	return nil
}
