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

	// modelTierParagraphPattern DETECTS the paragraph, and is never used to
	// decide how much text to remove: its extent is unbounded, so a wording
	// change could make it swallow far more than the paragraph. Removal is
	// driven by knownForeignTierParagraphPattern instead, and anything this
	// finds afterwards holds the whole record back.
	modelTierParagraphPattern = regexp.MustCompile(`(?s)(\A|\n)This iteration of Claude is .*?(?:\n\n|\z)`)
	// knownForeignTierParagraphPattern REMOVES only paragraphs matching a
	// measured literal exactly, bounded by paragraph breaks on both sides.
	knownForeignTierParagraphPattern = compileKnownTierParagraphs()

	// Claude Code also names the active model in the git commit trailer it tells
	// the model to append. MEASURED: 84/88 real Claude Code records carry
	// "Co-Authored-By: Claude Opus 5 (1M context)", so the display name is the
	// active model rather than a bare "Claude".
	coAuthorTrailerPattern = regexp.MustCompile(`Co-Authored-By: Claude([^\n<]*)<`)
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
	return inner == "" || inner == publicModelDisplayName || inner == publicModelDisplayName+" (1M context)"
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
	text := strings.TrimLeft(match, "\n")
	prefix := "This iteration of Claude is Claude " + publicModelDisplayName
	if !strings.HasPrefix(text, prefix) {
		return false
	}
	remainder := text[len(prefix):]
	if remainder == "" {
		return true
	}
	// A delimiter must end the display name. In particular, reject lookalikes
	// such as "Opus 5.6", which regexp word boundaries incorrectly accept.
	return strings.HasPrefix(remainder, ",") ||
		strings.HasPrefix(remainder, "\n") ||
		remainder == "." ||
		strings.HasPrefix(remainder, ". ") ||
		strings.HasPrefix(remainder, ".\n")
}

// foreignModelTierParagraphs holds each model-tier paragraph exactly as
// measured. MEASURED: one 694-character variant, byte-identical across every
// occurrence in every captured archive.
//
// Removal matches these literally rather than by pattern. A pattern would
// delete whatever a wording change happened to make it match, silently and
// without bound; a literal can only ever remove text that was verified first,
// and an unrecognized variant survives to hold its record back instead.
var foreignModelTierParagraphs = []string{
	"This iteration of Claude is Claude Fable 5, the first model in Anthropic's new Claude 5 " +
		"family and part of a new Mythos-class model tier that sits above Claude Opus in capability. " +
		"Claude Fable 5 and Claude Mythos 5 share the same underlying model. " +
		"Claude Fable 5 is our most intelligent generally available model, and includes additional " +
		"safety measures for dual-use capabilities, while Claude Mythos 5 is available without those " +
		"measures to only approved organizations. " +
		"Fable 5 is the most advanced generally available Claude model. " +
		"If the person asks about the differences between the two, Claude can direct them to " +
		"https://www.anthropic.com/news/claude-fable-5-mythos-5 for more information.",
}

// compileKnownTierParagraphs builds an exact-text matcher requiring a blank
// line or a text boundary on each side. Anything standing on its own line but
// not in its own paragraph is a shape that was never measured, so it is left
// for the hold-back path rather than removed on a guess.
func compileKnownTierParagraphs() *regexp.Regexp {
	quoted := make([]string, 0, len(foreignModelTierParagraphs))
	for _, paragraph := range foreignModelTierParagraphs {
		quoted = append(quoted, regexp.QuoteMeta(paragraph))
	}
	return regexp.MustCompile(`(?:\A|\n\n)(?:` + strings.Join(quoted, "|") + `)(?:\n\n|\z)`)
}

// stripForeignModelTierParagraph removes measured model-tier paragraphs that
// introduce a model other than the delivered one, reporting how many went.
//
// Only the paragraph's opening sentence is an identity claim; the remainder
// ranks the named model against its siblings and links to a model-specific
// announcement URL. Rewriting just the opening sentence would leave the rest
// asserting a different model, and substituting the model name throughout would
// produce claims Anthropic never published (a model ranked above itself, an
// announcement URL that does not exist). Dropping the paragraph keeps the
// surrounding prompt coherent without inventing vendor copy.
func stripForeignModelTierParagraph(text string) (string, int64) {
	var stripped int64
	rewritten := knownForeignTierParagraphPattern.ReplaceAllStringFunc(text, func(match string) string {
		if tierParagraphNamesPublicModel(match) {
			return match
		}
		stripped++
		// The leading blank line separated the paragraph from the one above it
		// and still has to separate that one from whatever now follows.
		if strings.HasPrefix(match, "\n\n") {
			return "\n\n"
		}
		return ""
	})
	return rewritten, stripped
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
// delivered model, reporting whether it changed and how many tier paragraphs
// were removed.
func rewriteModelIdentityText(text string) (string, bool, int64) {
	rewritten, _ := rewriteModelDisplayNames(text)
	// The tier paragraph is dropped rather than reworded, so it is handled only
	// here: elsewhere the surrounding turn depends on it and the record is held
	// back instead. A prompt can carry it without the identity line, and vice
	// versa.
	rewritten, stripped := stripForeignModelTierParagraph(rewritten)
	return rewritten, rewritten != text, stripped
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

// systemIdentityRewriteStats separates the two edits so the count of removed
// paragraphs can be reported on its own.
type systemIdentityRewriteStats struct {
	BlocksRewritten   int64
	ParagraphsStopped int64
}

// normalizeSystemModelIdentity rewrites model self-references in request.system
// so the prompt agrees with the model the record is delivered under.
func normalizeSystemModelIdentity(system json.RawMessage) (json.RawMessage, systemIdentityRewriteStats, error) {
	var stats systemIdentityRewriteStats
	if len(system) == 0 {
		return system, stats, nil
	}

	var asText string
	if err := json.Unmarshal(system, &asText); err == nil {
		rewritten, changed, stripped := rewriteModelIdentityText(asText)
		if !changed {
			return system, stats, nil
		}
		encoded, err := json.Marshal(rewritten)
		if err != nil {
			return nil, stats, fmt.Errorf("encode system prompt: %w", err)
		}
		stats.BlocksRewritten = 1
		stats.ParagraphsStopped = stripped
		return encoded, stats, nil
	}

	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(system, &blocks); err != nil {
		return system, stats, nil
	}

	for _, block := range blocks {
		raw, ok := block["text"]
		if !ok {
			continue
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			continue
		}
		rewritten, changed, stripped := rewriteModelIdentityText(text)
		if !changed {
			continue
		}
		encoded, err := json.Marshal(rewritten)
		if err != nil {
			return nil, stats, fmt.Errorf("encode system prompt block: %w", err)
		}
		block["text"] = encoded
		stats.BlocksRewritten++
		stats.ParagraphsStopped += stripped
	}
	if stats.BlocksRewritten == 0 {
		return system, stats, nil
	}

	// Re-encoding a map would sort members, so rewritten blocks go back out in
	// the measured Anthropic order.
	ordered := make([]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		encoded, err := marshalOrderedObject(block, anthropicBlockKeyOrder["text"])
		if err != nil {
			return nil, stats, fmt.Errorf("encode system prompt block: %w", err)
		}
		ordered = append(ordered, encoded)
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return nil, stats, fmt.Errorf("encode system prompt blocks: %w", err)
	}
	return encoded, stats, nil
}
