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
	normalized, changed, err := normalizeCodexSessionToolItems(request["input"], true)
	if err != nil {
		return nil, err
	}
	if !changed {
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
			changed = true
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
			changed = true
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, false, fmt.Errorf("encode normalized Codex tool item: %w", err)
		}
		items[index] = encoded
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
