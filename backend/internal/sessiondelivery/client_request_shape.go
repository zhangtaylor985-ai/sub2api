package sessiondelivery

import (
	"encoding/json"
	"fmt"
)

// Real Claude Code requests declare neither of these members. MEASURED across
// 89 captured requests carrying a claude-cli user agent: 0 sent tool_choice and
// 0 sent context_management, while 29 and 68 foreign-client requests did.
//
// Both are legitimate Anthropic request members, so their presence is a client
// fingerprint rather than a protocol error, and both can steer the response.
// They are therefore dropped only when the value provably did not: tool_choice
// "auto" is what the API does with no tool_choice at all, and a context edit
// that keeps everything removes nothing. Any other value stays, because the
// exchange would no longer explain itself without it.
func normalizeClientRequestShape(request map[string]json.RawMessage) (int64, error) {
	var dropped int64

	inert, err := toolChoiceIsDefault(request["tool_choice"])
	if err != nil {
		return 0, err
	}
	if inert {
		delete(request, "tool_choice")
		dropped++
	}

	inert, err = contextManagementIsInert(request["context_management"])
	if err != nil {
		return 0, err
	}
	if inert {
		delete(request, "context_management")
		dropped++
	}
	return dropped, nil
}

// toolChoiceIsDefault reports a tool_choice that leaves the decision to the
// model, which is the behaviour of omitting the member.
func toolChoiceIsDefault(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	var choice map[string]json.RawMessage
	if err := json.Unmarshal(raw, &choice); err != nil {
		return false, nil
	}
	if rawString(choice["type"]) != "auto" {
		return false, nil
	}
	// "auto" alongside a modifier such as disable_parallel_tool_use is no longer
	// the default behaviour.
	return len(choice) == 1, nil
}

// contextManagementIsInert reports a context_management directive whose every
// edit keeps its whole target, so nothing was removed from the context.
func contextManagementIsInert(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	var management map[string]json.RawMessage
	if err := json.Unmarshal(raw, &management); err != nil {
		return false, nil
	}
	if len(management) != 1 || !isJSONArray(management["edits"]) {
		return false, nil
	}
	var edits []map[string]json.RawMessage
	if err := json.Unmarshal(management["edits"], &edits); err != nil {
		return false, fmt.Errorf("decode context_management edits: %w", err)
	}
	if len(edits) == 0 {
		return false, nil
	}
	for _, edit := range edits {
		// An edit carrying anything beyond its type and an all-keeping scope may
		// trim the context in a way the record could not otherwise explain.
		if len(edit) != 2 || rawString(edit["keep"]) != "all" || rawString(edit["type"]) == "" {
			return false, nil
		}
	}
	return true, nil
}

// validateClientRequestShape fails closed on a request that still declares a
// member no real Claude Code request carries in a form that did nothing.
func validateClientRequestShape(request map[string]json.RawMessage) error {
	inert, err := toolChoiceIsDefault(request["tool_choice"])
	if err != nil {
		return err
	}
	if inert {
		return fmt.Errorf("request declares a default tool_choice no Claude Code request sends")
	}
	inert, err = contextManagementIsInert(request["context_management"])
	if err != nil {
		return err
	}
	if inert {
		return fmt.Errorf("request declares an inert context_management no Claude Code request sends")
	}
	return nil
}
