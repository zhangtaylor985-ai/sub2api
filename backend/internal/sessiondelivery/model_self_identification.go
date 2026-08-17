package sessiondelivery

import (
	"encoding/json"
	"regexp"
	"strings"
)

// A record is delivered as Claude Opus 5 output, so assistant prose that names
// a different model contradicts it. Unlike the system prompt, which follows a
// fixed client template, this is free-form generated text: there is no faithful
// way to restate it, so affected records are held back instead of rewritten.
//
// Patterns stay anchored to a first-person claim. Assistants routinely discuss
// other models as subject matter ("Mem0 defaults to gpt-5-mini"), and that is
// ordinary content rather than a disclosure.
// Patterns with a capture group are identity claims only when the captured gap
// between the pronoun and the model name stays nominal. "我是来帮你排查 OpenAI
// 兼容层" and "I'm here to help with your OpenAI integration" state a purpose,
// not an identity.
var foreignModelSelfClaimPatterns = []*regexp.Regexp{
	regexp.MustCompile(`我(?:是|为)([^。！？\n]{0,40}?)(?i:GPT|ChatGPT|Codex|OpenAI|Gemini)`),
	regexp.MustCompile(`作为([^。！？\n]{0,30}?)(?i:GPT|ChatGPT|Codex|OpenAI|Gemini)`),
	regexp.MustCompile(`(?i)\bI(?:'m| am)\b([^.\n]{0,40}?)\b(?:GPT|ChatGPT|Codex|OpenAI|Gemini)`),
	regexp.MustCompile(`(?i)(?:trained|developed|built|created) by OpenAI()`),
	regexp.MustCompile(`由\s*OpenAI\s*(?:训练|开发|研发)()`),
	regexp.MustCompile(`(?i)powered by([^.\n]{0,25}?)\b(?:GPT|Codex)`),
}

// selfClaimDisqualifiers mark the gap as a verb construction rather than a
// predicate naming the model.
var selfClaimDisqualifiers = []string{
	"来", "帮", "为了", "想", "要", "排查", "解决", "处理", "对比", "负责", "使用", "调用",
	"here", "help", "working", "looking", "trying", "going", "about", "using", "debugging",
}

func isIdentityClaim(gap string) bool {
	for _, disqualifier := range selfClaimDisqualifiers {
		if strings.Contains(strings.ToLower(gap), disqualifier) {
			return false
		}
	}
	return true
}

// countForeignModelSelfClaims reports assistant turns that identify the model
// as something other than the delivered one.
func countForeignModelSelfClaims(request, response map[string]json.RawMessage) int64 {
	var claims int64
	for _, text := range assistantProse(request, response) {
		if hasForeignModelSelfClaim(text) {
			claims++
		}
	}
	return claims
}

func hasForeignModelSelfClaim(text string) bool {
	for _, pattern := range foreignModelSelfClaimPatterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if isIdentityClaim(match[1]) {
				return true
			}
		}
	}
	return false
}

// countForeignModelTrailerProse reports model-authored text that credits a
// commit to a model other than the delivered one.
//
// The trailer is rewritten wherever the client wrote it — tool descriptions and
// the environment block — but the model also repeats it when a project rule
// tells it which trailer to use. That repetition is the model's own prose, so
// the record is held back rather than edited. MEASURED: 1 of 10365 captured
// records, quoting a CLAUDE.md that still names Opus 4.8.
//
// Tool results are not model output and are not examined here: 503 of the 507
// captured occurrences are a user's own document read back by a tool, which a
// real session would carry regardless of which model served it.
func countForeignModelTrailerProse(request, response map[string]json.RawMessage) int64 {
	var claims int64
	for _, text := range assistantProse(request, response) {
		for _, trailer := range coAuthorTrailerPattern.FindAllString(text, -1) {
			if !trailerNamesPublicModel(trailer) {
				claims++
			}
		}
	}
	return claims
}

// countForeignModelTierProse reports model-tier paragraphs introducing a model
// other than the delivered one that survive normalization.
//
// Two cases reach here. A client can send the paragraph as a conversation turn,
// which the assistant then answers on its own terms, restating the named model
// as the active one; dropping the turn would leave that reply answering nothing
// and rewriting the reply would fabricate model output. And a paragraph left in
// request.system is one that did not match any measured literal, so removing it
// would mean deleting unverified text by pattern.
//
// Both are held back rather than edited: an unrecognized variant costs one
// record, while guessing at its extent would silently damage the rest.
//
// It runs after normalizeSystemModelIdentity, so recognized paragraphs are gone
// by this point and only genuine leftovers are counted.
func countForeignModelTierProse(request, response map[string]json.RawMessage) int64 {
	texts := conversationProse(request, response)
	texts = append(texts, systemPromptTexts(request["system"])...)
	var hits int64
	for _, text := range texts {
		for _, paragraph := range modelTierParagraphPattern.FindAllString(text, -1) {
			if !tierParagraphNamesPublicModel(paragraph) {
				hits++
			}
		}
	}
	return hits
}

// assistantProse returns model-authored text from the response and from the
// assistant turns echoed back in request history.
func assistantProse(request, response map[string]json.RawMessage) []string {
	texts := contentBlockProse(response["content"])

	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return texts
	}
	for _, message := range messages {
		if rawString(message["role"]) != "assistant" {
			continue
		}
		texts = append(texts, contentBlockProse(message["content"])...)
	}
	return texts
}

// conversationProse returns the text of every conversation turn regardless of
// role, accepting either content shape, plus the response body. Client-injected
// prompt text can arrive as a user turn, so unlike assistantProse this is not
// restricted to model-authored text.
func conversationProse(request, response map[string]json.RawMessage) []string {
	texts := contentBlockProse(response["content"])

	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return texts
	}
	for _, message := range messages {
		raw := message["content"]
		var asText string
		if err := json.Unmarshal(raw, &asText); err == nil {
			texts = append(texts, asText)
			continue
		}
		texts = append(texts, contentBlockProse(raw)...)
	}
	return texts
}

func contentBlockProse(raw json.RawMessage) []string {
	if !isJSONArray(raw) {
		return nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch rawString(block["type"]) {
		case "text":
			texts = append(texts, rawString(block["text"]))
		case "thinking":
			texts = append(texts, rawString(block["thinking"]))
		}
	}
	return texts
}
