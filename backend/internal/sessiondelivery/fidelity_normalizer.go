package sessiondelivery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
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
	AssistantToolTrackingStripped int64
	ResponseIDsReshaped           int64
	ToolCallersCompleted          int64
	StopFieldsCompleted           int64
	SearchResultIDsReshaped       int64
	SystemRoleMessagesFolded      int64
	SystemModelIdentityRewritten  int64
	ModelTierParagraphsStripped   int64
	RequestTokenBudgetRaised      int64
	ClientRequestMembersDropped   int64
	ForeignToolsConverted         int64
	ForeignToolsDropped           int64
	// ForeignSystemPromptTools is non-zero when the system prompt still names a
	// tool the conversion removed, which leaves the record unable to be
	// delivered consistently.
	ForeignSystemPromptTools int64
	// ForeignModelSelfClaims is non-zero when assistant prose identifies the
	// model as something other than the one the record is delivered under.
	ForeignModelSelfClaims int64
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
	var stripped int64
	for {
		removed := false
		for _, match := range openAISearchParamPattern.FindAllStringIndex(text, -1) {
			pos, after := match[0], match[1]
			if pos == 0 || (text[pos-1] != '?' && text[pos-1] != '&') || !isTrackingParamEnd(text, after) {
				continue
			}
			if text[pos-1] == '?' && after < len(text) && text[after] == '&' {
				// Keep the query marker and promote the next parameter. Repeating
				// the scan also handles adjacent duplicate tracking parameters.
				text = text[:pos] + text[after+1:]
			} else {
				text = text[:pos-1] + text[after:]
			}
			stripped++
			removed = true
			break
		}
		if !removed {
			return text, stripped
		}
	}
}

// isTrackingParamEnd reports whether the parameter value ends at offset. The
// test is "the next character cannot continue a query value" rather than a list
// of known terminators, because an allow list silently keeps the parameter when
// it is followed by punctuation that was not enumerated — a comma, semicolon or
// slash, all of which occur when a model cites a URL inline in prose.
//
// A trailing '.' is treated as sentence punctuation only when it is itself at a
// boundary, so a genuine dotted value such as "openai.foo" is preserved.
func isTrackingParamEnd(text string, offset int) bool {
	if offset >= len(text) {
		return true
	}
	if text[offset] == '.' {
		return offset+1 >= len(text) || !isQueryValueChar(text[offset+1])
	}
	return !isQueryValueChar(text[offset])
}

func isQueryValueChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' ||
		b == '_' || b == '-' || b == '.' || b == '%' || b == '+'
}

// sanitizeAssistantTextBlock rewrites the text field of an assistant text
// block in place, returning whether the block changed. Web search citations
// on the block may carry the same URL either in url or inside cited_text, so
// every string in the assistant-generated citation payload is normalized.
func sanitizeAssistantTextBlock(block map[string]json.RawMessage) (bool, int64) {
	changed := false
	var stripped int64
	if text := rawString(block["text"]); text != "" {
		if sanitized, n := stripOpenAISearchTracking(text); n > 0 {
			block["text"] = mustJSON(sanitized)
			changed = true
			stripped += n
		}
	}
	if rawCitations := block["citations"]; len(rawCitations) > 0 {
		sanitized, n, err := sanitizeJSONStrings(rawCitations)
		if err == nil && n > 0 {
			block["citations"] = sanitized
			changed = true
			stripped += n
		}
	}
	return changed, stripped
}

// sanitizeJSONStrings removes tracking parameters from string values inside
// assistant-generated JSON while preserving every untouched raw subtree. It
// is used for tool inputs only; user and tool-result data never enters here.
func sanitizeJSONStrings(raw json.RawMessage) (json.RawMessage, int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return raw, 0, nil
	}
	switch trimmed[0] {
	case '"':
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, 0, err
		}
		sanitized, stripped := stripOpenAISearchTracking(value)
		if stripped == 0 {
			return raw, 0, nil
		}
		return mustJSON(sanitized), stripped, nil
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return nil, 0, err
		}
		var total int64
		for key, value := range object {
			sanitized, stripped, err := sanitizeJSONStrings(value)
			if err != nil {
				return nil, 0, err
			}
			if stripped > 0 {
				object[key] = sanitized
				total += stripped
			}
		}
		if total == 0 {
			return raw, 0, nil
		}
		encoded, err := json.Marshal(object)
		return encoded, total, err
	case '[':
		var array []json.RawMessage
		if err := json.Unmarshal(trimmed, &array); err != nil {
			return nil, 0, err
		}
		var total int64
		for index, value := range array {
			sanitized, stripped, err := sanitizeJSONStrings(value)
			if err != nil {
				return nil, 0, err
			}
			if stripped > 0 {
				array[index] = sanitized
				total += stripped
			}
		}
		if total == 0 {
			return raw, 0, nil
		}
		encoded, err := json.Marshal(array)
		return encoded, total, err
	default:
		return raw, 0, nil
	}
}

func sanitizeAssistantToolInput(block map[string]json.RawMessage) (bool, int64, error) {
	input, exists := block["input"]
	if !exists {
		return false, 0, nil
	}
	sanitized, stripped, err := sanitizeJSONStrings(input)
	if err != nil {
		return false, 0, fmt.Errorf("sanitize assistant tool input: %w", err)
	}
	if stripped == 0 {
		return false, 0, nil
	}
	block["input"] = sanitized
	return true, stripped, nil
}

// sanitizeServerToolResult cleans the result payload of a server-side search or
// fetch tool. These blocks live in the assistant turn and hold the cited URLs
// themselves, so leaving them out means the tracking parameter survives in the
// result set even when the prose citing it was cleaned. A user-supplied
// tool_result is never passed here.
func sanitizeServerToolResult(block map[string]json.RawMessage) (bool, int64, error) {
	content, exists := block["content"]
	if !exists {
		return false, 0, nil
	}
	sanitized, stripped, err := sanitizeJSONStrings(content)
	if err != nil {
		return false, 0, fmt.Errorf("sanitize server tool result: %w", err)
	}
	if stripped == 0 {
		return false, 0, nil
	}
	block["content"] = sanitized
	return true, stripped, nil
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

	// Folding runs first so the later passes see only user and assistant turns.
	folded, err := foldSystemRoleMessages(request)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.SystemRoleMessagesFolded += folded

	// Tool conversion runs before the tool-identifier and block passes so those
	// see the Claude Code tool surface rather than the originating client's.
	toolConversion, err := convertForeignClientTools(request, response)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.ForeignToolsConverted += int64(toolConversion.ToolsConverted)
	stats.ForeignToolsDropped += int64(toolConversion.ToolsDropped)
	stats.ForeignSystemPromptTools += int64(toolConversion.SystemPromptTools)

	system, systemRewrites, err := normalizeSystemModelIdentity(request["system"])
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	if systemRewrites.BlocksRewritten > 0 {
		request["system"] = system
	}
	stats.SystemModelIdentityRewritten += systemRewrites.BlocksRewritten
	stats.ModelTierParagraphsStripped += systemRewrites.ParagraphsStopped

	// The same display names also appear in tool descriptions and in the
	// environment block clients send as a conversation turn.
	displayNameRewrites, err := normalizeModelDisplayNames(request)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.SystemModelIdentityRewritten += displayNameRewrites

	// Prose naming a foreign model cannot be repaired without fabricating model
	// output, whether the assistant identified itself or a client sent the
	// model-tier paragraph as a conversation turn. Both share the hold-back path.
	stats.ForeignModelSelfClaims += countForeignModelSelfClaims(request, response) +
		countForeignModelTierProse(request, response)

	budgetRaised, err := alignRequestTokenBudget(request, response)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.RequestTokenBudgetRaised += budgetRaised

	shapeDropped, err := normalizeClientRequestShape(request)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.ClientRequestMembersDropped += shapeDropped

	_, toolIDs, openAIBlocks, requestTrackingStripped, requestToolTrackingStripped, err := normalizeRequestFidelity(request, options.CodexProjection)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.ToolIDsNormalized += toolIDs
	stats.OpenAIContentBlocksNormalized += openAIBlocks
	stats.AssistantTextTrackingStripped += requestTrackingStripped
	stats.AssistantToolTrackingStripped += requestToolTrackingStripped

	_, responseToolIDs, thinkingRemoved, responseTrackingStripped, responseToolTrackingStripped, err := normalizeResponseFidelity(
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
	stats.AssistantToolTrackingStripped += responseToolTrackingStripped

	// Completes public identifier shape, the stop members and tool_use.caller.
	// Both bodies are always re-encoded here, so the earlier passes no longer
	// need to report whether they mutated anything.
	normalizedRequest, normalizedResponse, shapeStats, err := normalizeAnthropicWireShape(request, response)
	if err != nil {
		return nil, nil, fidelityNormalizationStats{}, err
	}
	stats.ResponseIDsReshaped += shapeStats.ResponseIDsReshaped
	stats.ToolCallersCompleted += shapeStats.ToolCallersCompleted
	stats.StopFieldsCompleted += shapeStats.StopFieldsCompleted
	stats.SearchResultIDsReshaped += shapeStats.SearchResultIDsReshaped

	return normalizedRequest, normalizedResponse, stats, nil
}

func normalizeRequestFidelity(request map[string]json.RawMessage, codexProjection bool) (bool, int64, int64, int64, int64, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return false, 0, 0, 0, 0, fmt.Errorf("decode request messages for fidelity normalization: %w", err)
	}

	changed := false
	var toolIDsNormalized int64
	var openAIBlocksNormalized int64
	var trackingStripped int64
	var toolTrackingStripped int64
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
							return false, 0, 0, 0, 0, fmt.Errorf("re-encode assistant string message: %w", merr)
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
			blockChanged := false
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
					blockChanged = true
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
					blockChanged = true
				}
			}

			if isAssistant && blockType == "text" {
				if textChanged, stripped := sanitizeAssistantTextBlock(block); textChanged {
					trackingStripped += stripped
					contentChanged = true
					blockChanged = true
				}
			}

			if isAssistant && (blockType == "tool_use" || blockType == "server_tool_use") {
				inputChanged, stripped, err := sanitizeAssistantToolInput(block)
				if err != nil {
					return false, 0, 0, 0, 0, err
				}
				if inputChanged {
					toolTrackingStripped += stripped
					contentChanged = true
					blockChanged = true
				}
			}

			if isAssistant && (blockType == "web_search_tool_result" || blockType == "web_fetch_tool_result") {
				resultChanged, stripped, err := sanitizeServerToolResult(block)
				if err != nil {
					return false, 0, 0, 0, 0, err
				}
				if resultChanged {
					toolTrackingStripped += stripped
					contentChanged = true
					blockChanged = true
				}
			}

			if blockChanged {
				reencoded, err := json.Marshal(block)
				if err != nil {
					return false, 0, 0, 0, 0, fmt.Errorf("re-encode request content block: %w", err)
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
			return false, 0, 0, 0, 0, fmt.Errorf("re-encode request message: %w", err)
		}
		messages[messageIndex] = reencoded
		changed = true
	}
	if changed {
		request["messages"] = mustJSON(messages)
	}
	return changed, toolIDsNormalized, openAIBlocksNormalized, trackingStripped, toolTrackingStripped, nil
}

func normalizeResponseFidelity(
	request, response map[string]json.RawMessage,
	removeSignedWhenDisabled bool,
) (bool, int64, int64, int64, int64, error) {
	var content []json.RawMessage
	if err := json.Unmarshal(response["content"], &content); err != nil {
		return false, 0, 0, 0, 0, fmt.Errorf("decode response content for fidelity normalization: %w", err)
	}

	thinkingEnabled := requestThinkingEnabled(request)
	changed := false
	var toolIDsNormalized int64
	var thinkingRemoved int64
	var trackingStripped int64
	var toolTrackingStripped int64
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
		blockChanged := false
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
				blockChanged = true
			}
			inputChanged, stripped, err := sanitizeAssistantToolInput(block)
			if err != nil {
				return false, 0, 0, 0, 0, err
			}
			if inputChanged {
				toolTrackingStripped += stripped
				changed = true
				blockChanged = true
			}
		}
		if blockType == "web_search_tool_result" || blockType == "web_fetch_tool_result" {
			resultChanged, stripped, err := sanitizeServerToolResult(block)
			if err != nil {
				return false, 0, 0, 0, 0, err
			}
			if resultChanged {
				toolTrackingStripped += stripped
				changed = true
				blockChanged = true
			}
		}
		if blockType == "text" {
			if textChanged, stripped := sanitizeAssistantTextBlock(block); textChanged {
				trackingStripped += stripped
				changed = true
				blockChanged = true
			}
		}
		if blockChanged {
			reencoded, err := json.Marshal(block)
			if err != nil {
				return false, 0, 0, 0, 0, fmt.Errorf("re-encode response content block: %w", err)
			}
			rawBlock = reencoded
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
	return changed, toolIDsNormalized, thinkingRemoved, trackingStripped, toolTrackingStripped, nil
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

// normalizeAnthropicToolID projects a tool identifier onto the public Anthropic
// wire format. Swapping only the provider prefix is not enough: it left the
// OpenAI identifier body in place, so the delivery carried "toolu_" values
// without Anthropic's "01" version prefix and "srvtoolu_ws_<hex>" values that
// name OpenAI's web-search call directly.
func normalizeAnthropicToolID(id string, serverTool bool) string {
	return anthropicToolID(id, serverTool)
}

func isLegacyCodexDeliveryRequest(request map[string]json.RawMessage) bool {
	return requestFieldMissing(request["metadata"]) &&
		requestFieldMissing(request["system"]) && requestEffort(request) != ""
}

func equalJSONRaw(a, b json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}
