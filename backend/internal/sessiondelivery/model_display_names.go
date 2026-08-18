package sessiondelivery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// normalizeModelDisplayNames aligns model display-name references that sit
// outside request.system in measured client templates.
//
// Claude Code writes the active model's display name into more than its system
// prompt: the Bash tool description carries the git commit trailer it instructs
// the model to append, and one measured client environment block arrives as a
// user turn. Free-form user and assistant prose is deliberately excluded from
// this pass: changing it would fabricate the conversation rather than normalize
// client scaffolding.
func normalizeModelDisplayNames(request map[string]json.RawMessage) (int64, error) {
	tools, err := normalizeToolDescriptionModelNames(request, rewriteModelDisplayNames)
	if err != nil {
		return 0, err
	}
	environments, err := normalizeClientEnvironmentModelNames(request, rewriteModelDisplayNames)
	if err != nil {
		return 0, err
	}
	return tools + environments, nil
}

// textRewriter reports the rewritten text and whether it changed.
type textRewriter func(string) (string, bool)

func normalizeToolDescriptionModelNames(request map[string]json.RawMessage, rewrite textRewriter) (int64, error) {
	raw := request["tools"]
	if !isJSONArray(raw) {
		return 0, nil
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return 0, fmt.Errorf("decode request tools for model display names: %w", err)
	}
	var rewrites int64
	for index, rawTool := range tools {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(rawTool, &tool); err != nil {
			continue
		}
		var description string
		if err := json.Unmarshal(tool["description"], &description); err != nil {
			continue
		}
		rewritten, changed := rewrite(description)
		if !changed {
			continue
		}
		tool["description"] = mustJSON(rewritten)
		// Only rewritten tools are re-encoded, so untouched declarations stay
		// byte-identical.
		encoded, err := marshalOrderedObject(tool, anthropicToolKeyOrder)
		if err != nil {
			return 0, fmt.Errorf("encode tool declaration: %w", err)
		}
		tools[index] = encoded
		rewrites++
	}
	if rewrites == 0 {
		return 0, nil
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return 0, fmt.Errorf("encode request tools: %w", err)
	}
	request["tools"] = encoded
	return rewrites, nil
}

func normalizeClientEnvironmentModelNames(request map[string]json.RawMessage, rewrite textRewriter) (int64, error) {
	raw := request["messages"]
	if !isJSONArray(raw) {
		return 0, nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return 0, fmt.Errorf("decode request messages for model display names: %w", err)
	}
	var rewrites int64
	for index, rawMessage := range messages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			continue
		}
		if rawString(message["role"]) != "user" {
			continue
		}
		content, changed, err := rewriteClientEnvironmentContent(message["content"], rewrite)
		if err != nil {
			return 0, err
		}
		if !changed {
			continue
		}
		message["content"] = content
		encoded, err := marshalOrderedObject(message, anthropicRequestMessageKeyOrder)
		if err != nil {
			return 0, fmt.Errorf("encode request message: %w", err)
		}
		messages[index] = encoded
		rewrites++
	}
	if rewrites == 0 {
		return 0, nil
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return 0, fmt.Errorf("encode request messages: %w", err)
	}
	request["messages"] = encoded
	return rewrites, nil
}

// rewriteClientEnvironmentContent handles both message content shapes, but
// rewrites only a measured <env> client block. Ordinary user text, tool results
// and every assistant turn remain byte-for-byte untouched.
func rewriteClientEnvironmentContent(raw json.RawMessage, rewrite textRewriter) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return raw, false, nil
	}
	var asText string
	if err := json.Unmarshal(raw, &asText); err == nil {
		if !isMeasuredClientEnvironment(asText) {
			return raw, false, nil
		}
		rewritten, changed := rewrite(asText)
		if !changed {
			return raw, false, nil
		}
		return mustJSON(rewritten), true, nil
	}
	if !isJSONArray(raw) {
		return raw, false, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return raw, false, nil
	}
	changed := false
	for index, rawBlock := range blocks {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			continue
		}
		if rawString(block["type"]) != "text" {
			continue
		}
		var text string
		if err := json.Unmarshal(block["text"], &text); err != nil {
			continue
		}
		if !isMeasuredClientEnvironment(text) {
			continue
		}
		rewritten, blockChanged := rewrite(text)
		if !blockChanged {
			continue
		}
		block["text"] = mustJSON(rewritten)
		encoded, err := marshalOrderedObject(block, anthropicBlockKeyOrder["text"])
		if err != nil {
			return nil, false, fmt.Errorf("encode text block: %w", err)
		}
		blocks[index] = encoded
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return nil, false, fmt.Errorf("encode message content: %w", err)
	}
	return encoded, true, nil
}

func isMeasuredClientEnvironment(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "<env>\n") &&
		strings.HasSuffix(trimmed, "\n</env>") &&
		modelIdentityLinePattern.MatchString(trimmed)
}

// validateModelDisplayNames fails closed when a measured client template still
// credits or names a model other than the one the record is delivered under.
// It intentionally does not scan arbitrary conversation prose.
func validateModelDisplayNames(request map[string]json.RawMessage) error {
	for _, text := range requestTemplateModelNameTexts(request) {
		for _, line := range modelIdentityLinePattern.FindAllString(text, -1) {
			if !namesPublicModel(line) {
				return fmt.Errorf("request text names a foreign model: %q", line)
			}
		}
		for _, trailer := range coAuthorTrailerPattern.FindAllString(text, -1) {
			if !trailerNamesPublicModel(trailer) {
				return fmt.Errorf("request text credits a foreign model: %q", trailer)
			}
		}
		for _, paragraph := range modelTierParagraphPattern.FindAllString(text, -1) {
			if !tierParagraphNamesPublicModel(paragraph) {
				return fmt.Errorf("request text introduces a foreign model")
			}
		}
	}
	return nil
}

// requestTemplateModelNameTexts returns only measured client-authored model
// templates. The system prompt has its own validator.
func requestTemplateModelNameTexts(request map[string]json.RawMessage) []string {
	var texts []string
	if raw := request["tools"]; isJSONArray(raw) {
		var tools []map[string]json.RawMessage
		if json.Unmarshal(raw, &tools) == nil {
			for _, tool := range tools {
				if description := rawString(tool["description"]); description != "" {
					texts = append(texts, description)
				}
			}
		}
	}
	if raw := request["messages"]; isJSONArray(raw) {
		var messages []map[string]json.RawMessage
		if json.Unmarshal(raw, &messages) == nil {
			for _, message := range messages {
				if rawString(message["role"]) != "user" {
					continue
				}
				content := message["content"]
				var asText string
				if json.Unmarshal(content, &asText) == nil {
					if isMeasuredClientEnvironment(asText) {
						texts = append(texts, asText)
					}
					continue
				}
				for _, text := range contentBlockProse(content) {
					if isMeasuredClientEnvironment(text) {
						texts = append(texts, text)
					}
				}
			}
		}
	}
	return texts
}
