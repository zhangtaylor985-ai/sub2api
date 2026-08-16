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

	// Alongside the identity line, Anthropic injects a paragraph introducing the
	// active model and ranking it against its siblings. It occupies its own
	// paragraph and is terminated by a blank line.
	modelTierParagraphPattern = regexp.MustCompile(`(?s)(\A|\n)This iteration of Claude is .*?(?:\n\n|\z)`)
	// publicModelTierParagraph recognizes the paragraph as already describing
	// the delivered model, in which case it is authentic client text.
	publicModelTierParagraph = regexp.MustCompile(`\AThis iteration of Claude is Claude ` + publicModelDisplayName + `\b`)

	// Claude Code also names the active model in the git commit trailer it tells
	// the model to append. MEASURED: 84/88 real Claude Code records carry
	// "Co-Authored-By: Claude Opus 5 (1M context)", so the display name is the
	// active model rather than a bare "Claude".
	coAuthorTrailerPattern = regexp.MustCompile(`Co-Authored-By: Claude([^\n<]*)<`)
	// publicModelTrailerName accepts both the plain and 1M-context display
	// names, which denote the same delivered model.
	publicModelTrailerName = regexp.MustCompile(`\A` + publicModelDisplayName + `\b`)
)

const coAuthorTrailerPrefix = "Co-Authored-By: Claude"

func canonicalCoAuthorTrailer() string {
	return coAuthorTrailerPrefix + " " + publicModelDisplayName + " <"
}

// trailerNamesPublicModel reports whether a commit trailer already credits the
// delivered model. A bare "Co-Authored-By: Claude" names no model at all and is
// left alone.
func trailerNamesPublicModel(match string) bool {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, coAuthorTrailerPrefix), "<"))
	return inner == "" || publicModelTrailerName.MatchString(inner)
}

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

// tierParagraphNamesPublicModel reports whether a matched tier paragraph
// already introduces the delivered model. The match may carry the newline that
// separates it from the preceding paragraph.
func tierParagraphNamesPublicModel(match string) bool {
	return publicModelTierParagraph.MatchString(strings.TrimLeft(match, "\n"))
}

// stripForeignModelTierParagraph removes the model-tier paragraph when it
// introduces a model other than the delivered one.
//
// Only the paragraph's opening sentence is an identity claim; the remainder
// ranks the named model against its siblings and links to a model-specific
// announcement URL. Rewriting just the opening sentence would leave the rest
// asserting a different model, and substituting the model name throughout
// would produce claims Anthropic never published (a model ranked above itself,
// an announcement URL that does not exist). Dropping the paragraph keeps the
// surrounding prompt coherent without inventing vendor copy.
func stripForeignModelTierParagraph(text string) (string, bool) {
	rewritten := modelTierParagraphPattern.ReplaceAllStringFunc(text, func(match string) string {
		if tierParagraphNamesPublicModel(match) {
			return match
		}
		if strings.HasPrefix(match, "\n") {
			return "\n"
		}
		return ""
	})
	return rewritten, rewritten != text
}

// rewriteModelDisplayNames aligns every model display-name reference in a
// single text with the delivered model.
//
// Each pattern here substitutes one name inside a fixed client template, which
// stays coherent afterwards. This is safe to apply outside the system prompt:
// Claude Code writes the same names into tool descriptions and into the
// environment block it sends as a conversation turn.
func rewriteModelDisplayNames(text string) (string, bool) {
	rewritten := text
	if modelIdentityLinePattern.MatchString(rewritten) {
		rewritten = modelIdentityLinePattern.ReplaceAllStringFunc(rewritten, func(line string) string {
			if namesPublicModel(line) {
				return line
			}
			return canonicalModelIdentityLine()
		})
		// The cutoff belongs to the advertised model. Normalize it even when the
		// identity line already says Opus 5; otherwise a stale January cutoff creates
		// a second, independently observable contradiction.
		rewritten = knowledgeCutoffPattern.ReplaceAllString(rewritten, "${1}"+publicModelKnowledgeCutoff)
	}
	rewritten = coAuthorTrailerPattern.ReplaceAllStringFunc(rewritten, func(match string) string {
		if trailerNamesPublicModel(match) {
			return match
		}
		return canonicalCoAuthorTrailer()
	})
	return rewritten, rewritten != text
}

// rewriteModelIdentityText aligns a single system prompt block with the
// delivered model, reporting whether it changed.
func rewriteModelIdentityText(text string) (string, bool) {
	rewritten, _ := rewriteModelDisplayNames(text)
	// The tier paragraph is dropped rather than reworded, so it is handled only
	// here: elsewhere the surrounding turn depends on it and the record is held
	// back instead. A prompt can carry it without the identity line, and vice
	// versa.
	rewritten, _ = stripForeignModelTierParagraph(rewritten)
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
		for _, paragraph := range modelTierParagraphPattern.FindAllString(text, -1) {
			if !tierParagraphNamesPublicModel(paragraph) {
				trimmed := strings.TrimSpace(paragraph)
				if len(trimmed) > 120 {
					trimmed = trimmed[:120]
				}
				return fmt.Errorf("system prompt introduces a foreign model: %q", trimmed)
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
