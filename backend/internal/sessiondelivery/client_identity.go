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
// Detection still covers the final encoded documents, but rewriting is
// structure-aware. Exact replacements are allowed only in client-owned system
// text, tool descriptions, user-side harness/tool-result content and tool-call
// inputs that carry client temporary paths. Assistant text and thinking are
// never rewritten; a remaining fingerprint holds the record back instead.

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

// scrubClientIdentity is the exact-literal primitive used only after the caller
// has established that the byte slice belongs to an allowed structural field.
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

// normalizeClientIdentity cleans measured client-owned fields without touching
// assistant prose. It returns the number of exact literal occurrences removed.
func normalizeClientIdentity(request, response map[string]json.RawMessage) (int64, error) {
	var scrubbed int64

	if raw := request["system"]; len(raw) > 0 {
		rewritten, count := scrubClientIdentity(raw)
		if count > 0 {
			request["system"] = rewritten
			scrubbed += count
		}
	}

	tools, count, err := scrubToolDescriptionClientIdentity(request["tools"])
	if err != nil {
		return 0, err
	}
	if count > 0 {
		request["tools"] = tools
		scrubbed += count
	}

	messages, count, err := scrubRequestMessageClientIdentity(request["messages"])
	if err != nil {
		return 0, err
	}
	if count > 0 {
		request["messages"] = messages
		scrubbed += count
	}

	content, count, err := scrubToolInputClientIdentity(response["content"])
	if err != nil {
		return 0, err
	}
	if count > 0 {
		response["content"] = content
		scrubbed += count
	}
	return scrubbed, nil
}

func scrubToolDescriptionClientIdentity(raw json.RawMessage) (json.RawMessage, int64, error) {
	if !isJSONArray(raw) {
		return raw, 0, nil
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, 0, fmt.Errorf("decode request tools for client identity: %w", err)
	}
	var scrubbed int64
	for index, rawTool := range tools {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(rawTool, &tool); err != nil {
			continue
		}
		description, count := scrubClientIdentity(tool["description"])
		if count == 0 {
			continue
		}
		tool["description"] = description
		encoded, err := marshalOrderedObject(tool, anthropicToolKeyOrder)
		if err != nil {
			return nil, 0, fmt.Errorf("encode tool after client identity scrub: %w", err)
		}
		tools[index] = encoded
		scrubbed += count
	}
	if scrubbed == 0 {
		return raw, 0, nil
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return nil, 0, fmt.Errorf("encode request tools after client identity scrub: %w", err)
	}
	return encoded, scrubbed, nil
}

func scrubRequestMessageClientIdentity(raw json.RawMessage) (json.RawMessage, int64, error) {
	if !isJSONArray(raw) {
		return raw, 0, nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, 0, fmt.Errorf("decode request messages for client identity: %w", err)
	}
	var scrubbed int64
	for index, rawMessage := range messages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			continue
		}
		var (
			content json.RawMessage
			count   int64
			err     error
		)
		switch rawString(message["role"]) {
		case "user":
			// User-side content carries both measured harness wrappers and tool
			// results. Only exact literals are replaced; ordinary prose is not.
			content, count = scrubClientIdentity(message["content"])
		case "assistant":
			// Assistant text/thinking is model output and must remain authentic.
			// Tool inputs may carry client-generated paths and filenames.
			content, count, err = scrubToolInputClientIdentity(message["content"])
		}
		if err != nil {
			return nil, 0, err
		}
		if count == 0 {
			continue
		}
		message["content"] = content
		encoded, err := marshalOrderedObject(message, anthropicRequestMessageKeyOrder)
		if err != nil {
			return nil, 0, fmt.Errorf("encode request message after client identity scrub: %w", err)
		}
		messages[index] = encoded
		scrubbed += count
	}
	if scrubbed == 0 {
		return raw, 0, nil
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return nil, 0, fmt.Errorf("encode request messages after client identity scrub: %w", err)
	}
	return encoded, scrubbed, nil
}

func scrubToolInputClientIdentity(raw json.RawMessage) (json.RawMessage, int64, error) {
	if !isJSONArray(raw) {
		return raw, 0, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, 0, fmt.Errorf("decode content for client identity: %w", err)
	}
	var scrubbed int64
	for index, rawBlock := range blocks {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			continue
		}
		blockType := rawString(block["type"])
		if blockType != "tool_use" && blockType != "server_tool_use" {
			continue
		}
		input, count := scrubClientIdentity(block["input"])
		if count == 0 {
			continue
		}
		block["input"] = input
		encoded, err := marshalOrderedObject(block, anthropicBlockKeyOrder[blockType])
		if err != nil {
			return nil, 0, fmt.Errorf("encode tool input after client identity scrub: %w", err)
		}
		blocks[index] = encoded
		scrubbed += count
	}
	if scrubbed == 0 {
		return raw, 0, nil
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return nil, 0, fmt.Errorf("encode content after client identity scrub: %w", err)
	}
	return encoded, scrubbed, nil
}

// countClientIdentityFingerprints reports any measured or unknown client
// fingerprint left after scoped normalization. Such a record is held back
// before final fidelity validation rather than repaired by rewriting prose.
func countClientIdentityFingerprints(values ...map[string]json.RawMessage) int64 {
	var fingerprints int64
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		fingerprints += int64(len(clientIdentityPattern.FindAll(encoded, -1)))
	}
	return fingerprints
}

// countHumanClientMentions reports conversation prose that explicitly names
// the client. The text is never edited; the record is held back.
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
