package sessiondelivery

import (
	"encoding/json"
	"fmt"
)

// claudeCodeTokenBudget is the max_tokens every observed real Claude Code
// request declares.
const claudeCodeTokenBudget = 64000

// alignRequestTokenBudget raises a request budget that its own response
// outran.
//
// The upstream that produced a projected response never saw the client's
// max_tokens, so a client probe sending max_tokens=1 can end up paired with a
// response carrying dozens of output tokens and stop_reason=end_turn. The real
// API cannot produce that pairing: reaching the budget truncates the response
// and reports stop_reason=max_tokens. Because max_tokens is a ceiling rather
// than an observed quantity, raising it to the Claude Code value removes the
// contradiction without altering what the exchange says.
//
// A response that outran even the Claude Code ceiling is left untouched for
// validateRequestTokenBudget to reject, rather than being papered over with a
// budget no real client sends.
func alignRequestTokenBudget(request, response map[string]json.RawMessage) (int64, error) {
	budget, err := requestTokenBudget(request)
	if err != nil {
		return 0, err
	}
	output := int64(responseOutputTokens(response))
	if budget <= 0 || output <= budget || output > claudeCodeTokenBudget {
		return 0, nil
	}
	request["max_tokens"] = mustJSON(claudeCodeTokenBudget)
	return 1, nil
}

// validateRequestTokenBudget fails closed on a response that reports more
// output tokens than its request allowed.
func validateRequestTokenBudget(request, response map[string]json.RawMessage) error {
	budget, err := requestTokenBudget(request)
	if err != nil {
		return err
	}
	if budget <= 0 {
		return nil
	}
	if output := int64(responseOutputTokens(response)); output > budget {
		return fmt.Errorf(
			"response usage output_tokens %d exceeds request max_tokens %d",
			output, budget,
		)
	}
	return nil
}

func requestTokenBudget(request map[string]json.RawMessage) (int64, error) {
	raw := request["max_tokens"]
	if len(raw) == 0 {
		return 0, nil
	}
	var budget int64
	if err := json.Unmarshal(raw, &budget); err != nil {
		return 0, fmt.Errorf("decode request max_tokens: %w", err)
	}
	return budget, nil
}
