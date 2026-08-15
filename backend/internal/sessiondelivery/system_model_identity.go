package sessiondelivery

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Claude Code writes the active model into its own system prompt. Delivery
// records are all published as DefaultPublicModel, so a prompt naming a
// different model contradicts the record that carries it.
const (
	publicModelDisplayName     = "Opus 5"
	publicModelKnowledgeCutoff = "May 2026"
	publicModel1MIdentityLine  = "You are powered by the model named Opus 5 (1M context). The exact model ID is claude-opus-5[1m]."
)

var (
	// The identity sentence always occupies its own line, and the display name
	// may itself contain periods ("Opus 4.8"), so the whole line is rebuilt
	// rather than parsed field by field.
	modelIdentityLinePattern = regexp.MustCompile(`You are powered by the model[^\n]*`)
	knowledgeCutoffPattern   = regexp.MustCompile(`(Assistant knowledge cutoff is )([^\n.]*)`)
)

func canonicalModelIdentityLine() string {
	return fmt.Sprintf(
		"You are powered by the model named %s. The exact model ID is %s.",
		publicModelDisplayName, DefaultPublicModel,
	)
}

// namesPublicModel reports whether an identity line already refers to the
// delivered model. The 1M-context variant names the same model and is left
// untouched so authentic client text survives.
func namesPublicModel(line string) bool {
	line = strings.TrimSpace(line)
	return line == canonicalModelIdentityLine() || line == publicModel1MIdentityLine
}

// rewriteModelIdentityText aligns a single system prompt block with the
// delivered model, reporting whether it changed.
func rewriteModelIdentityText(text string) (string, bool) {
	lines := modelIdentityLinePattern.FindAllString(text, -1)
	if len(lines) == 0 {
		return text, false
	}
	rewritten := modelIdentityLinePattern.ReplaceAllStringFunc(text, func(line string) string {
		if namesPublicModel(line) {
			return line
		}
		return canonicalModelIdentityLine()
	})
	// The cutoff belongs to the advertised model. Normalize it even when the
	// identity line already says Opus 5; otherwise a stale January cutoff creates
	// a second, independently observable contradiction.
	rewritten = knowledgeCutoffPattern.ReplaceAllString(rewritten, "${1}"+publicModelKnowledgeCutoff)
	return rewritten, rewritten != text
}

// validateSystemModelIdentity fails closed when a system prompt still claims a
// model other than the one the record is delivered under.
func validateSystemModelIdentity(system json.RawMessage) error {
	for _, text := range systemPromptTexts(system) {
		lines := modelIdentityLinePattern.FindAllString(text, -1)
		for _, line := range lines {
			if !namesPublicModel(line) {
				return fmt.Errorf("system prompt names a foreign model: %q", line)
			}
		}
		if len(lines) == 0 {
			continue
		}
		for _, cutoff := range knowledgeCutoffPattern.FindAllStringSubmatch(text, -1) {
			if strings.TrimSpace(cutoff[2]) != publicModelKnowledgeCutoff {
				return fmt.Errorf("system prompt carries a foreign model knowledge cutoff: %q", cutoff[2])
			}
		}
	}
	return nil
}

// systemPromptTexts returns the prompt text carried by either system shape.
func systemPromptTexts(system json.RawMessage) []string {
	if len(system) == 0 {
		return nil
	}
	var asText string
	if err := json.Unmarshal(system, &asText); err == nil {
		return []string{asText}
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(system, &blocks); err != nil {
		return nil
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return texts
}

// normalizeSystemModelIdentity rewrites model self-references in request.system
// so the prompt agrees with the model the record is delivered under.
func normalizeSystemModelIdentity(system json.RawMessage) (json.RawMessage, int64, error) {
	if len(system) == 0 {
		return system, 0, nil
	}

	var asText string
	if err := json.Unmarshal(system, &asText); err == nil {
		rewritten, changed := rewriteModelIdentityText(asText)
		if !changed {
			return system, 0, nil
		}
		encoded, err := json.Marshal(rewritten)
		if err != nil {
			return nil, 0, fmt.Errorf("encode system prompt: %w", err)
		}
		return encoded, 1, nil
	}

	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(system, &blocks); err != nil {
		return system, 0, nil
	}

	var rewrites int64
	for _, block := range blocks {
		raw, ok := block["text"]
		if !ok {
			continue
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			continue
		}
		rewritten, changed := rewriteModelIdentityText(text)
		if !changed {
			continue
		}
		encoded, err := json.Marshal(rewritten)
		if err != nil {
			return nil, 0, fmt.Errorf("encode system prompt block: %w", err)
		}
		block["text"] = encoded
		rewrites++
	}
	if rewrites == 0 {
		return system, 0, nil
	}

	// Re-encoding a map would sort members, so rewritten blocks go back out in
	// the measured Anthropic order.
	ordered := make([]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		encoded, err := marshalOrderedObject(block, anthropicBlockKeyOrder["text"])
		if err != nil {
			return nil, 0, fmt.Errorf("encode system prompt block: %w", err)
		}
		ordered = append(ordered, encoded)
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return nil, 0, fmt.Errorf("encode system prompt blocks: %w", err)
	}
	return encoded, rewrites, nil
}
