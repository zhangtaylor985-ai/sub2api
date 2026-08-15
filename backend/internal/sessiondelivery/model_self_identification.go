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
