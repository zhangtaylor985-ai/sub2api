package sessiondelivery

import (
	"encoding/json"
	"fmt"
)

// anthropicToolKeyOrder is the member order of a tool declaration.
// MEASURED 8951/9026 captured tool objects.
var anthropicToolKeyOrder = []string{"name", "description", "input_schema"}

// normalizeModelDisplayNames aligns model display-name references that sit
// outside request.system with the delivered model.
//
// Claude Code writes the active model's display name into more than its system
// prompt: the Bash tool description carries the git commit trailer it instructs
// the model to append, and the environment block arrives as a conversation turn.
// Leaving those behind reproduces the contradiction the system prompt pass
// exists to remove — a record delivered as Opus 5 telling the model to credit
// commits to a different model.
func normalizeModelDisplayNames(request map[string]json.RawMessage) (int64, error) {
	tools, err := normalizeToolDescriptionModelNames(request)
	if err != nil {
		return 0, err
	}
	messages, err := normalizeMessageModelNames(request)
	if err != nil {
		return 0, err
	}
	return tools + messages, nil
}

func normalizeToolDescriptionModelNames(request map[string]json.RawMessage) (int64, error) {
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
		rewritten, changed := rewriteModelDisplayNames(description)
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

func normalizeMessageModelNames(request map[string]json.RawMessage) (int64, error) {
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
		content, changed, err := rewriteContentModelNames(message["content"])
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

// rewriteContentModelNames handles both message content shapes, rewriting only
// text so tool inputs and results pass through untouched.
func rewriteContentModelNames(raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return raw, false, nil
	}
	var asText string
	if err := json.Unmarshal(raw, &asText); err == nil {
		rewritten, changed := rewriteModelDisplayNames(asText)
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
		rewritten, blockChanged := rewriteModelDisplayNames(text)
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

// validateModelDisplayNames fails closed when any request text still credits or
// names a model other than the one the record is delivered under.
func validateModelDisplayNames(request map[string]json.RawMessage) error {
	for _, text := range requestModelNameTexts(request) {
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

// requestModelNameTexts returns the request text that carries client-authored
// model references: tool descriptions and conversation turns. The system prompt
// has its own validator.
func requestModelNameTexts(request map[string]json.RawMessage) []string {
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
				content := message["content"]
				var asText string
				if json.Unmarshal(content, &asText) == nil {
					texts = append(texts, asText)
					continue
				}
				texts = append(texts, contentBlockProse(content)...)
			}
		}
	}
	return texts
}
