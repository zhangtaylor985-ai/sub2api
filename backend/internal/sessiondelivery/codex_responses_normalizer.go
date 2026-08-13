package sessiondelivery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// normalizeCodexSessionResponsesRequest adapts newer Codex-only tool item
// variants to the stable Responses function-call shape understood by the
// delivery converter. It is used only for the stored Session projection; the
// original request and the live gateway request remain untouched.
func normalizeCodexSessionResponsesRequest(body json.RawMessage) (json.RawMessage, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode Responses request for Session projection: %w", err)
	}
	input, additionalTools, extracted, err := extractCodexSessionAdditionalTools(request["input"])
	if err != nil {
		return nil, err
	}
	normalized, changed, err := normalizeCodexSessionToolItems(input, true)
	if err != nil {
		return nil, err
	}
	toolsChanged := false
	if len(additionalTools) > 0 {
		merged, mergeChanged, mergeErr := mergeCodexSessionTools(request["tools"], additionalTools)
		if mergeErr != nil {
			return nil, mergeErr
		}
		request["tools"] = merged
		toolsChanged = mergeChanged
	}
	if !changed && !extracted && !toolsChanged {
		return body, nil
	}
	request["input"] = normalized
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode normalized Responses request: %w", err)
	}
	return encoded, nil
}

// normalizeCodexSessionResponsesResponse performs the response-side half of
// normalizeCodexSessionResponsesRequest for custom_tool_call output items.
func normalizeCodexSessionResponsesResponse(body json.RawMessage) (json.RawMessage, error) {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode Responses response for Session projection: %w", err)
	}
	normalized, changed, err := normalizeCodexSessionToolItems(response["output"], false)
	if err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	response["output"] = normalized
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode normalized Responses response: %w", err)
	}
	return encoded, nil
}

func normalizeCodexSessionToolItems(raw json.RawMessage, includeOutputs bool) (json.RawMessage, bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, false, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		// Plain string input is valid for a Responses request.
		var text string
		if includeOutputs && json.Unmarshal(raw, &text) == nil {
			return raw, false, nil
		}
		return nil, false, fmt.Errorf("decode Codex Responses tool items: %w", err)
	}

	changed := false
	for index, rawItem := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(rawItem, &item); err != nil {
			continue
		}
		itemType := rawString(item["type"])
		itemChanged := false
		switch itemType {
		case "custom_tool_call", "mcp_tool_call":
			item["type"] = mustJSON("function_call")
			if requestFieldMissing(item["arguments"]) && !requestFieldMissing(item["input"]) {
				arguments, err := codexToolArguments(item["input"])
				if err != nil {
					return nil, false, fmt.Errorf("normalize %s input: %w", itemType, err)
				}
				item["arguments"] = mustJSON(arguments)
			}
			itemChanged = true
		case "custom_tool_call_output", "mcp_tool_call_output":
			if !includeOutputs {
				continue
			}
			item["type"] = mustJSON("function_call_output")
			output, err := codexToolOutputText(item["output"])
			if err != nil {
				return nil, false, fmt.Errorf("normalize %s output: %w", itemType, err)
			}
			item["output"] = mustJSON(output)
			itemChanged = true
		case "function_call":
			if namespace := rawString(item["namespace"]); namespace != "" {
				item["name"] = mustJSON(codexSessionToolName(namespace, rawString(item["name"])))
				delete(item, "namespace")
				itemChanged = true
			}
		case "function_call_output":
			if !includeOutputs || jsonValueIsString(item["output"]) {
				continue
			}
			output, err := codexToolOutputText(item["output"])
			if err != nil {
				return nil, false, fmt.Errorf("normalize function_call_output output: %w", err)
			}
			item["output"] = mustJSON(output)
			itemChanged = true
		case "agent_message":
			if !includeOutputs {
				continue
			}
			content, _, err := normalizeCodexAgentMessageContent(item["content"])
			if err != nil {
				return nil, false, err
			}
			item["type"] = mustJSON("message")
			item["role"] = mustJSON("user")
			item["content"] = content
			delete(item, "author")
			delete(item, "recipient")
			itemChanged = true
		}
		if !itemChanged {
			continue
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, false, fmt.Errorf("encode normalized Codex tool item: %w", err)
		}
		items[index] = encoded
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, false, fmt.Errorf("encode normalized Codex tool items: %w", err)
	}
	return encoded, true, nil
}

// extractCodexSessionAdditionalTools moves Codex's input-scoped namespace
// declarations into the ordinary Responses tools array. The shared converter
// can then project them to Anthropic tool definitions. This is delivery-only:
// the captured Original request and live gateway body remain byte-for-byte
// unchanged.
func extractCodexSessionAdditionalTools(raw json.RawMessage) (json.RawMessage, []json.RawMessage, bool, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return raw, nil, false, nil
	}
	filtered := make([]json.RawMessage, 0, len(items))
	tools := make([]json.RawMessage, 0)
	changed := false
	for _, rawItem := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(rawItem, &item) != nil || rawString(item["type"]) != "additional_tools" {
			filtered = append(filtered, rawItem)
			continue
		}
		changed = true
		var namespaces []map[string]json.RawMessage
		if err := json.Unmarshal(item["tools"], &namespaces); err != nil {
			return nil, nil, false, fmt.Errorf("decode Codex additional_tools namespaces: %w", err)
		}
		for _, namespace := range namespaces {
			namespaceName := rawString(namespace["name"])
			var nested []map[string]json.RawMessage
			if err := json.Unmarshal(namespace["tools"], &nested); err != nil {
				return nil, nil, false, fmt.Errorf("decode Codex tool namespace %q: %w", namespaceName, err)
			}
			for _, tool := range nested {
				name := rawString(tool["name"])
				if name == "" {
					continue
				}
				tool["name"] = mustJSON(codexSessionToolName(namespaceName, name))
				if rawString(tool["type"]) == "custom" {
					tool["type"] = mustJSON("function")
					tool["parameters"] = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)
					delete(tool, "format")
				}
				encoded, err := json.Marshal(tool)
				if err != nil {
					return nil, nil, false, fmt.Errorf("encode Codex additional tool %q: %w", name, err)
				}
				tools = append(tools, encoded)
			}
		}
	}
	if !changed {
		return raw, nil, false, nil
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, nil, false, fmt.Errorf("encode Codex input without additional_tools: %w", err)
	}
	return encoded, tools, true, nil
}

func mergeCodexSessionTools(raw json.RawMessage, additional []json.RawMessage) (json.RawMessage, bool, error) {
	var existing []json.RawMessage
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return nil, false, fmt.Errorf("decode Responses tools for Session projection: %w", err)
		}
	}
	seen := make(map[string]struct{}, len(existing)+len(additional))
	for _, rawTool := range existing {
		var tool map[string]json.RawMessage
		if json.Unmarshal(rawTool, &tool) == nil {
			seen[rawString(tool["name"])] = struct{}{}
		}
	}
	changed := false
	for _, rawTool := range additional {
		var tool map[string]json.RawMessage
		if json.Unmarshal(rawTool, &tool) != nil {
			continue
		}
		name := rawString(tool["name"])
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		existing = append(existing, rawTool)
		seen[name] = struct{}{}
		changed = true
	}
	encoded, err := json.Marshal(existing)
	if err != nil {
		return nil, false, fmt.Errorf("encode merged Responses tools: %w", err)
	}
	return encoded, changed, nil
}

func normalizeCodexAgentMessageContent(raw json.RawMessage) (json.RawMessage, bool, error) {
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, false, fmt.Errorf("decode Codex agent_message content: %w", err)
	}
	filtered := make([]map[string]json.RawMessage, 0, len(parts))
	changed := false
	for _, part := range parts {
		if rawString(part["type"]) == "encrypted_content" {
			changed = true
			continue
		}
		filtered = append(filtered, part)
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, false, fmt.Errorf("encode Codex agent_message content: %w", err)
	}
	return encoded, changed, nil
}

func codexSessionToolName(namespace, name string) string {
	if namespace == "" || namespace == "functions" {
		return name
	}
	return namespace + "__" + name
}

func jsonValueIsString(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil
}

func codexToolArguments(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		if strings.TrimSpace(value) == "" {
			return "{}", nil
		}
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(value), &object) == nil {
			compact := bytes.NewBuffer(nil)
			if err := json.Compact(compact, []byte(value)); err != nil {
				return "", err
			}
			return compact.String(), nil
		}
		// Responses custom tools accept free-form text, while Anthropic
		// tool_use.input must be a JSON object. Preserve the exact value in a
		// neutral input field instead of interpreting tool-specific syntax.
		wrapped, err := json.Marshal(map[string]string{"input": value})
		if err != nil {
			return "", err
		}
		return string(wrapped), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("input is not a JSON object: %w", err)
	}
	compact := bytes.NewBuffer(nil)
	if err := json.Compact(compact, raw); err != nil {
		return "", err
	}
	return compact.String(), nil
}

func codexToolOutputText(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch rawString(part["type"]) {
		case "input_text", "output_text", "text":
			if text := rawString(part["text"]); text != "" {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n\n"), nil
}
