package sessiondelivery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// fidelityNormalizationOptions controls delivery-only rewrites. These
// rewrites never touch the response returned to the client.
type fidelityNormalizationOptions struct {
	CodexProjection          bool
	RemoveSignedWhenDisabled bool
}

type fidelityNormalizationStats struct {
	ToolIDsNormalized             int64
	OpenAIContentBlocksNormalized int64
	ResponseThinkingRemoved       int64
	AssistantTextTrackingStripped int64
}

// openAISearchParamPattern matches the tracking parameter OpenAI web search
// appends to cited URLs. Real Claude citations never carry it, so its
// presence in assistant-generated text is an upstream fingerprint.
var openAISearchParamPattern = regexp.MustCompile(`(?i)utm_source=openai`)

// stripOpenAISearchTracking removes utm_source=openai query parameters from
// assistant-generated text while keeping the surrounding URL valid:
//
//	"?utm_source=openai&a=1" -> "?a=1"   (first of several params)
//	"?utm_source=openai"     -> ""       (sole param, '?' dropped)
//	"?a=1&utm_source=openai" -> "?a=1"   (trailing param)
//
// Only exact query-parameter boundaries are rewritten; lookalikes such as
// "utm_source=openai2" or "xutm_source=openai" are preserved. The function is
// deterministic, so a sanitized response and its echo in later request
// history stay byte-identical.
func stripOpenAISearchTracking(text string) (string, int64) {
	matches := openAISearchParamPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text, 0
	}
	var out strings.Builder
	out.Grow(len(text))
	var stripped int64
	cursor := 0
	for _, match := range matches {
		pos, after := match[0], match[1]
		preceded := pos > 0 && (text[pos-1] == '?' || text[pos-1] == '&')
		followedByAmp := after < len(text) && text[after] == '&'
		bounded := after >= len(text) || followedByAmp || !isQueryValueChar(text[after])
		if !preceded || !bounded {
			continue
		}
		stripped++
		if text[pos-1] == '?' && followedByAmp {
			out.WriteString(text[cursor:pos]) // keep '?', drop param and following '&'
			cursor = after + 1
			continue
		}
		out.WriteString(text[cursor : pos-1]) // drop the '?' or '&' with the param
		cursor = after
	}
	if stripped == 0 {
		return text, 0
	}
	out.WriteString(text[cursor:])
	return out.String(), stripped
}

func isQueryValueChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '.'
}

// sanitizeAssistantTextBlock rewrites the text field of an assistant text
// block in place, returning whether the block changed.
func sanitizeAssistantTextBlock(block map[string]json.RawMessage) (bool, int64) {
	text := rawString(block["text"])
	if text == "" {
		return false, 0
	}
	sanitized, stripped := stripOpenAISearchTracking(text)
	if stripped == 0 {
		return false, 0
	}
	block["text"] = mustJSON(sanitized)
	return true, stripped
}

// normalizeProjectionFidelity removes observable GPT/Codex wire artifacts
// from an Anthropic delivery projection. The transformation is deterministic,
// so tool IDs remain stable across response blocks, later request history and
// hourly archive rebuilds.
func normalizeProjectionFidelity(
	requestRaw, responseRaw json.RawMessage,
	options fidelityNormalizationOptions,
) (json.RawMessage, json.RawMessage, fidelityNormalizationStats, error) {
	request, err := decodeJSONObject(requestRaw, "request")
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	response, err := decodeJSONObject(responseRaw, "response.response_data")
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}

	var stats fidelityNormalizationStats
	requestChanged, toolIDs, openAIBlocks, requestTrackingStripped, err := normalizeRequestFidelity(request, options.CodexProjection)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.ToolIDsNormalized += toolIDs
	stats.OpenAIContentBlocksNormalized += openAIBlocks
	stats.AssistantTextTrackingStripped += requestTrackingStripped

	responseChanged, responseToolIDs, thinkingRemoved, responseTrackingStripped, err := normalizeResponseFidelity(
		request,
		response,
		options.RemoveSignedWhenDisabled,
	)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.ToolIDsNormalized += responseToolIDs
	stats.ResponseThinkingRemoved += thinkingRemoved
	stats.AssistantTextTrackingStripped += responseTrackingStripped

	normalizedRequest := requestRaw
	if requestChanged {
		normalizedRequest, err = json.Marshal(request)
		if err != nil {
			return nil, nil, fidelityNormalizationStats{}, fmt.Errorf("re-encode fidelity-normalized request: %w", err)
		}
	}
	normalizedResponse := responseRaw
	if responseChanged {
		normalizedResponse, err = json.Marshal(response)
		if err != nil {
			return nil, nil, fidelityNormalizationStats{}, fmt.Errorf("re-encode fidelity-normalized response: %w", err)
		}
	}
	return normalizedRequest, normalizedResponse, stats, nil
}

func normalizeRequestFidelity(request map[string]json.RawMessage, codexProjection bool) (bool, int64, int64, int64, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return false, 0, 0, 0, fmt.Errorf("decode request messages for fidelity normalization: %w", err)
	}

	changed := false
	var toolIDsNormalized int64
	var openAIBlocksNormalized int64
	var trackingStripped int64
	for messageIndex, rawMessage := range messages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			continue
		}
		// Only assistant content is model-generated. User, system and tool
		// result text is authentic client input and must pass through
		// unchanged, even when it mentions upstream vendors.
		isAssistant := rawString(message["role"]) == "assistant"
		var content []json.RawMessage
		if err := json.Unmarshal(message["content"], &content); err != nil {
			// string content is valid Anthropic input
			if isAssistant {
				var textContent string
				if serr := json.Unmarshal(message["content"], &textContent); serr == nil {
					if sanitized, stripped := stripOpenAISearchTracking(textContent); stripped > 0 {
						message["content"] = mustJSON(sanitized)
						reencoded, merr := json.Marshal(message)
						if merr != nil {
							return false, 0, 0, 0, fmt.Errorf("re-encode assistant string message: %w", merr)
						}
						messages[messageIndex] = reencoded
						changed = true
						trackingStripped += stripped
					}
				}
			}
			continue
		}

		contentChanged := false
		normalized := make([]json.RawMessage, 0, len(content))
		for _, rawBlock := range content {
			var block map[string]json.RawMessage
			if err := json.Unmarshal(rawBlock, &block); err != nil {
				normalized = append(normalized, rawBlock)
				continue
			}

			blockType := rawString(block["type"])
			if codexProjection {
				switch blockType {
				case "input_text", "output_text":
					block["type"] = mustJSON("text")
					blockType = "text"
					openAIBlocksNormalized++
					contentChanged = true
				case "encrypted_content":
					// OpenAI encrypted reasoning has no Anthropic Messages
					// request-block equivalent. The delivery response already
					// carries its Opus-shaped thinking block and signature.
					openAIBlocksNormalized++
					contentChanged = true
					continue
				}
			}

			idField, serverTool := requestBlockToolIDField(blockType)
			if idField != "" {
				oldID := rawString(block[idField])
				newID := normalizeAnthropicToolID(oldID, serverTool)
				if newID != oldID {
					block[idField] = mustJSON(newID)
					toolIDsNormalized++
					contentChanged = true
				}
			}

			if isAssistant && blockType == "text" {
				if textChanged, stripped := sanitizeAssistantTextBlock(block); textChanged {
					trackingStripped += stripped
					contentChanged = true
				}
			}

			if contentChanged {
				reencoded, err := json.Marshal(block)
				if err != nil {
					return false, 0, 0, 0, fmt.Errorf("re-encode request content block: %w", err)
				}
				rawBlock = reencoded
			}
			normalized = append(normalized, rawBlock)
		}

		if !contentChanged {
			continue
		}
		if len(normalized) == 0 {
			normalized = append(normalized, mustJSON(map[string]any{"type": "text", "text": ""}))
		}
		message["content"] = mustJSON(normalized)
		reencoded, err := json.Marshal(message)
		if err != nil {
			return false, 0, 0, 0, fmt.Errorf("re-encode request message: %w", err)
		}
		messages[messageIndex] = reencoded
		changed = true
	}
	if changed {
		request["messages"] = mustJSON(messages)
	}
	return changed, toolIDsNormalized, openAIBlocksNormalized, trackingStripped, nil
}

func normalizeResponseFidelity(
	request, response map[string]json.RawMessage,
	removeSignedWhenDisabled bool,
) (bool, int64, int64, int64, error) {
	var content []json.RawMessage
	if err := json.Unmarshal(response["content"], &content); err != nil {
		return false, 0, 0, 0, fmt.Errorf("decode response content for fidelity normalization: %w", err)
	}

	thinkingEnabled := requestThinkingEnabled(request)
	changed := false
	var toolIDsNormalized int64
	var thinkingRemoved int64
	var trackingStripped int64
	normalized := make([]json.RawMessage, 0, len(content))
	leadingThinking := make([]json.RawMessage, 0, len(content))
	trailingContent := make([]json.RawMessage, 0, len(content))
	seenNonThinking := false
	for _, rawBlock := range content {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			if thinkingEnabled {
				seenNonThinking = true
				trailingContent = append(trailingContent, rawBlock)
			} else {
				normalized = append(normalized, rawBlock)
			}
			continue
		}
		blockType := rawString(block["type"])
		if !thinkingEnabled && (blockType == "thinking" || blockType == "redacted_thinking") {
			authField := "signature"
			if blockType == "redacted_thinking" {
				authField = "data"
			}
			if removeSignedWhenDisabled || rawString(block[authField]) == "" {
				thinkingRemoved++
				changed = true
				continue
			}
		}
		if blockType == "tool_use" || blockType == "server_tool_use" {
			oldID := rawString(block["id"])
			newID := normalizeAnthropicToolID(oldID, blockType == "server_tool_use")
			if newID != oldID {
				block["id"] = mustJSON(newID)
				toolIDsNormalized++
				changed = true
				reencoded, err := json.Marshal(block)
				if err != nil {
					return false, 0, 0, 0, fmt.Errorf("re-encode response tool block: %w", err)
				}
				rawBlock = reencoded
			}
		}
		if blockType == "text" {
			if textChanged, stripped := sanitizeAssistantTextBlock(block); textChanged {
				trackingStripped += stripped
				changed = true
				reencoded, err := json.Marshal(block)
				if err != nil {
					return false, 0, 0, 0, fmt.Errorf("re-encode response text block: %w", err)
				}
				rawBlock = reencoded
			}
		}
		if thinkingEnabled {
			if blockType == "thinking" || blockType == "redacted_thinking" {
				if seenNonThinking {
					changed = true
				}
				leadingThinking = append(leadingThinking, rawBlock)
			} else {
				seenNonThinking = true
				trailingContent = append(trailingContent, rawBlock)
			}
			continue
		}
		normalized = append(normalized, rawBlock)
	}
	if thinkingEnabled {
		normalized = append(normalized, leadingThinking...)
		normalized = append(normalized, trailingContent...)
	}
	if changed {
		response["content"] = mustJSON(normalized)
	}
	return changed, toolIDsNormalized, thinkingRemoved, trackingStripped, nil
}

func requestBlockToolIDField(blockType string) (field string, serverTool bool) {
	switch blockType {
	case "tool_use":
		return "id", false
	case "server_tool_use":
		return "id", true
	case "tool_result":
		return "tool_use_id", false
	case "web_search_tool_result", "web_fetch_tool_result":
		return "tool_use_id", true
	default:
		return "", false
	}
}

func normalizeAnthropicToolID(id string, serverTool bool) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return id
	}
	if strings.HasPrefix(id, "srvtoolu_") || strings.HasPrefix(id, "toolu_") {
		return id
	}

	suffix := ""
	switch {
	case strings.HasPrefix(id, "tooluse_"):
		suffix = strings.TrimPrefix(id, "tooluse_")
	case strings.HasPrefix(id, "call_"):
		suffix = strings.TrimPrefix(id, "call_")
	case strings.HasPrefix(id, "call"):
		suffix = strings.TrimPrefix(id, "call")
	case strings.HasPrefix(id, "fc_"):
		suffix = strings.TrimPrefix(id, "fc_")
	}
	if strings.TrimSpace(suffix) == "" {
		return id
	}
	prefix := "toolu_"
	if serverTool {
		prefix = "srvtoolu_"
	}
	return prefix + suffix
}

func isLegacyCodexDeliveryRequest(request map[string]json.RawMessage) bool {
	return requestFieldMissing(request["metadata"]) &&
		requestFieldMissing(request["system"]) && requestEffort(request) != ""
}

func equalJSONRaw(a, b json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}
