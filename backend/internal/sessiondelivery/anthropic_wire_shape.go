package sessiondelivery

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// This file normalizes the remaining observable differences between a
// delivery projection and a real Claude Code x Claude Opus 5 exchange:
// public identifier shape, response envelope completeness and JSON member
// ordering. Every transformation is delivery-only and deterministic on the
// input record, so ingest, hourly export and offline rebuild converge on the
// same bytes and later request-history echoes stay byte-identical.
//
// Values marked MEASURED were taken from real local Claude Code transcripts
// (~/.claude/projects) covering claude-opus-4-6 / 4-8 / 5. Values marked
// DOCUMENTED follow the published Anthropic Messages shape because no local
// capture was available; those are applied only where the current output is
// known to be wrong (Go map marshaling emits members alphabetically).

// Anthropic public identifiers are a two-character version prefix followed by
// 22 base58 characters. MEASURED: 96 distinct real msg_/toolu_ identifiers,
// 2,112 body characters, zero occurrences of '0', 'O', 'I' or 'l'.
const (
	anthropicIDAlphabet   = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	anthropicIDVersion    = "01"
	anthropicIDBodyLength = 22
)

// anthropicIDDomain keys the identifier derivation. It is deliberately a fixed
// public constant rather than the delivery HMAC secret: the offline rebuild
// must reproduce archived identifiers byte-for-byte without depending on
// runtime configuration. Nothing is protected by this value — the inputs are
// already opaque gateway identifiers.
const anthropicIDDomain = "sub2api-session-anthropic-public-id-v1"

// anthropicPublicID reshapes an opaque internal identifier into the public
// Anthropic wire format, preserving any identifier that already has that
// shape so repeated normalization is idempotent.
func anthropicPublicID(prefix, sourceID string) string {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || sourceID == prefix || hasAnthropicIDShape(prefix, sourceID) {
		return sourceID
	}
	mac := hmac.New(sha256.New, []byte(anthropicIDDomain))
	_, _ = mac.Write([]byte(prefix))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(sourceID))
	digest := mac.Sum(nil)

	body := make([]byte, anthropicIDBodyLength)
	for index := range body {
		body[index] = anthropicIDAlphabet[int(digest[index])%len(anthropicIDAlphabet)]
	}
	return prefix + anthropicIDVersion + string(body)
}

func hasAnthropicIDShape(prefix, id string) bool {
	if !strings.HasPrefix(id, prefix) {
		return false
	}
	body := id[len(prefix):]
	if len(body) != len(anthropicIDVersion)+anthropicIDBodyLength {
		return false
	}
	if !strings.HasPrefix(body, anthropicIDVersion) {
		return false
	}
	for index := len(anthropicIDVersion); index < len(body); index++ {
		if strings.IndexByte(anthropicIDAlphabet, body[index]) < 0 {
			return false
		}
	}
	return true
}

// anthropicIDSourcePrefixes are the provider prefixes an identifier may arrive
// with. Longer prefixes come first so "srvtoolu_" is not mistaken for a bare
// identifier and "call_" wins over "call".
var anthropicIDSourcePrefixes = []string{"srvtoolu_", "tooluse_", "toolu_", "call_", "fc_", "call", "msg_", "ws_"}

// anthropicIDSeed strips every provider prefix the identifier carries. The
// seed — not the full identifier — is what the derivation hashes, so a freshly
// captured "call_X" and the already-projected "toolu_X" in an existing archive
// resolve to the same public identifier. Without this, ingest and offline
// rebuild would disagree and every archive replay would churn.
//
// Prefixes are stripped repeatedly because an earlier projection stacked them:
// an OpenAI "ws_X" web search call became "srvtoolu_ws_X", so a single strip
// would leave "ws_X" as the seed on one path and "X" on the other. Each pass
// shortens the string, so the loop terminates.
func anthropicIDSeed(id string) string {
	for {
		stripped := false
		for _, prefix := range anthropicIDSourcePrefixes {
			if strings.HasPrefix(id, prefix) {
				id = id[len(prefix):]
				stripped = true
				break
			}
		}
		if !stripped {
			return id
		}
	}
}

// anthropicToolID reshapes a tool or server-tool identifier. It is the single
// entry point for every tool identifier in the projection — block ids, request
// history echoes, tool_result references and search result back-references —
// so a reshaped block and everything pointing at it stay consistent.
func anthropicToolID(id string, serverTool bool) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return id
	}
	// A real Anthropic identifier, or one this pass already produced.
	if hasAnthropicIDShape("toolu_", id) || hasAnthropicIDShape("srvtoolu_", id) {
		return id
	}
	seed := anthropicIDSeed(id)
	if seed == "" {
		return id
	}
	prefix := "toolu_"
	if serverTool {
		prefix = "srvtoolu_"
	}
	return anthropicPublicID(prefix, seed)
}

// anthropicResponseID reshapes the assistant message identifier.
func anthropicResponseID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || hasAnthropicIDShape("msg_", id) {
		return id
	}
	seed := anthropicIDSeed(id)
	if seed == "" {
		return id
	}
	return anthropicPublicID("msg_", seed)
}

// MEASURED member orders. Unlisted members are appended alphabetically, so an
// unexpected field still produces deterministic output.
var (
	// MEASURED 118/118 real assistant messages.
	anthropicResponseKeyOrder = []string{
		"id", "type", "role", "model", "content",
		"stop_reason", "stop_sequence", "stop_details", "usage",
		"container", "context_management",
	}
	// MEASURED 118/118.
	anthropicUsageKeyOrder = []string{
		"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens",
		"output_tokens", "server_tool_use", "service_tier", "cache_creation",
		"inference_geo", "iterations", "speed",
	}
	// MEASURED 126/126.
	anthropicCacheCreationKeyOrder  = []string{"ephemeral_1h_input_tokens", "ephemeral_5m_input_tokens"}
	anthropicServerToolUseKeyOrder  = []string{"web_search_requests", "web_fetch_requests"}
	anthropicRequestMessageKeyOrder = []string{"role", "content"}
	// MEASURED 8951/8951 captured tool declarations carry name, description and
	// input_schema in that order. Server tools declare "type" instead of a
	// description and lead with it, per the Anthropic reference; listing both
	// leading members lets one order serve either shape.
	anthropicToolKeyOrder = []string{"type", "name", "description", "input_schema"}

	// DOCUMENTED. No local capture of a raw Claude Code request body exists,
	// but alphabetical ordering is certainly wrong, and both the vendor
	// specification example and the Anthropic reference lead with "model".
	anthropicRequestKeyOrder = []string{
		"model", "max_tokens", "messages", "system", "tools", "tool_choice",
		"thinking", "output_config", "temperature", "top_p", "top_k",
		"stop_sequences", "stream", "metadata", "service_tier", "mcp_servers",
		"betas", "container", "context_management",
	}
)

// anthropicBlockKeyOrder is an allow list: a content block whose type is absent
// is left byte-identical rather than guessed at, which keeps user-supplied and
// unrecognized shapes untouched.
var anthropicBlockKeyOrder = map[string][]string{
	"text":                   {"type", "text", "citations"},                             // MEASURED 64/64
	"thinking":               {"type", "thinking", "signature"},                         // MEASURED 30/30
	"tool_use":               {"type", "id", "name", "input", "caller"},                 // MEASURED 39/39
	"tool_result":            {"tool_use_id", "type", "content", "is_error"},            // MEASURED 39/39
	"image":                  {"type", "source"},                                        // MEASURED 3/3
	"redacted_thinking":      {"type", "data"},                                          // DOCUMENTED
	"server_tool_use":        {"type", "id", "name", "input"},                           // DOCUMENTED
	"web_search_tool_result": {"type", "tool_use_id", "content"},                        // DOCUMENTED
	"web_fetch_tool_result":  {"type", "tool_use_id", "content"},                        // DOCUMENTED
	"web_search_result":      {"type", "title", "url", "encrypted_content", "page_age"}, // DOCUMENTED
}

// anthropicDirectToolCaller is the caller value real Opus emits on every
// directly invoked tool. MEASURED 39/39 across opus-4-6 / 4-8 / 5.
func anthropicDirectToolCaller() json.RawMessage {
	return json.RawMessage(`{"type":"direct"}`)
}

type anthropicWireShapeStats struct {
	ResponseIDsReshaped     int64
	ToolCallersCompleted    int64
	StopFieldsCompleted     int64
	SearchResultIDsReshaped int64
}

// normalizeAnthropicWireShape completes the delivery projection: public
// identifier shape, the stop members a Responses projection cannot supply, and
// tool_use.caller. Member ordering is deliberately NOT applied here — later
// pipeline stages (historical upgrade, echo repair, usage projection) re-encode
// these bodies from Go maps and would re-alphabetize them. Ordering is applied
// once at the end by applyAnthropicMemberOrder.
func normalizeAnthropicWireShape(
	request, response map[string]json.RawMessage,
) (json.RawMessage, json.RawMessage, anthropicWireShapeStats, error) {
	var stats anthropicWireShapeStats

	if current := rawString(response["id"]); current != "" {
		if reshaped := anthropicResponseID(current); reshaped != current {
			response["id"] = mustJSON(reshaped)
			stats.ResponseIDsReshaped++
		}
	}
	if completeAnthropicStopFields(response) {
		stats.StopFieldsCompleted++
	}

	responseContent, callers, searchIDs, err := completeAnthropicContentBlocks(response["content"])
	if err != nil {
		return nil, nil, stats, err
	}
	if len(responseContent) > 0 {
		response["content"] = responseContent
	}
	stats.ToolCallersCompleted += callers
	stats.SearchResultIDsReshaped += searchIDs

	if raw := request["messages"]; len(raw) > 0 {
		var messages []json.RawMessage
		if err := json.Unmarshal(raw, &messages); err != nil {
			return nil, nil, stats, fmt.Errorf("decode request messages for wire shaping: %w", err)
		}
		messagesChanged := false
		for index, rawMessage := range messages {
			message, err := decodeJSONObject(rawMessage, "request message")
			if err != nil || rawString(message["role"]) != "assistant" {
				continue
			}
			content, callers, searchIDs, err := completeAnthropicContentBlocks(message["content"])
			if err != nil {
				return nil, nil, stats, err
			}
			if len(content) == 0 {
				continue
			}
			message["content"] = content
			stats.ToolCallersCompleted += callers
			stats.SearchResultIDsReshaped += searchIDs
			encoded, err := json.Marshal(message)
			if err != nil {
				return nil, nil, stats, fmt.Errorf("re-encode request message for wire shaping: %w", err)
			}
			messages[index] = encoded
			messagesChanged = true
		}
		if messagesChanged {
			encoded, err := json.Marshal(messages)
			if err != nil {
				return nil, nil, stats, fmt.Errorf("re-encode request messages for wire shaping: %w", err)
			}
			request["messages"] = encoded
		}
	}

	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return nil, nil, stats, fmt.Errorf("re-encode request for wire shaping: %w", err)
	}
	encodedResponse, err := json.Marshal(response)
	if err != nil {
		return nil, nil, stats, fmt.Errorf("re-encode response for wire shaping: %w", err)
	}
	return encodedRequest, encodedResponse, stats, nil
}

// finalizeDeliveryRecord applies the last two delivery-only steps: it fills the
// client user agent in the troubleshooting metadata and rewrites JSON members
// into real Anthropic order instead of Go's alphabetical map output. It must run
// after every other projection stage, because those stages re-encode from maps
// and would re-alphabetize the result. Both steps are no-ops on an already
// finalized record, so archive replays stay byte-idempotent.
func finalizeDeliveryRecord(record *DeliveryRecord) error {
	if record == nil {
		return nil
	}
	if len(record.Request) > 0 {
		request, err := decodeJSONObject(record.Request, "request")
		if err != nil {
			return err
		}
		if record.Metadata.UserAgent == "" {
			record.Metadata.UserAgent = deliveryUserAgent(request)
		}
		if raw := request["messages"]; isJSONArray(raw) {
			var messages []json.RawMessage
			if err := json.Unmarshal(raw, &messages); err != nil {
				return fmt.Errorf("decode request messages for member ordering: %w", err)
			}
			for index, rawMessage := range messages {
				message, err := decodeJSONObject(rawMessage, "request message")
				if err != nil {
					continue
				}
				reordered, err := reorderAnthropicMessage(message)
				if err != nil {
					return err
				}
				messages[index] = reordered
			}
			encoded, err := json.Marshal(messages)
			if err != nil {
				return fmt.Errorf("re-encode request messages for member ordering: %w", err)
			}
			request["messages"] = encoded
		}
		if raw := request["system"]; isJSONArray(raw) {
			reordered, err := reorderAnthropicContentBlocks(raw)
			if err != nil {
				return err
			}
			request["system"] = reordered
		}
		if raw := request["tools"]; isJSONArray(raw) {
			reordered, err := reorderAnthropicToolDeclarations(raw)
			if err != nil {
				return err
			}
			request["tools"] = reordered
		}
		encoded, err := marshalOrderedObject(request, anthropicRequestKeyOrder)
		if err != nil {
			return fmt.Errorf("re-encode request for member ordering: %w", err)
		}
		record.Request = encoded
	}
	if len(record.Response.ResponseData) > 0 {
		response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
		if err != nil {
			return err
		}
		encoded, err := reorderAnthropicResponse(response)
		if err != nil {
			return err
		}
		record.Response.ResponseData = encoded
	}
	return nil
}

// completeAnthropicStopFields adds the stop members real Opus always emits.
// A Responses-protocol projection has no equivalent, and their absence is
// visible when the delivery is compared with a real response. MEASURED:
// stop_sequence present 118/118, stop_details present 126/126, both null
// except stop_sequence on an actual stop-sequence hit.
func completeAnthropicStopFields(response map[string]json.RawMessage) bool {
	changed := false
	for _, field := range []string{"stop_sequence", "stop_details"} {
		if _, exists := response[field]; !exists {
			response[field] = json.RawMessage("null")
			changed = true
		}
	}
	return changed
}

// completeAnthropicContentBlocks attaches tool_use.caller and reshapes the
// server-tool identifiers that the tool-ID pass does not reach, namely the
// tool_use_id back-reference on search result blocks.
func completeAnthropicContentBlocks(raw json.RawMessage) (json.RawMessage, int64, int64, error) {
	if !isJSONArray(raw) {
		return nil, 0, 0, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, 0, 0, fmt.Errorf("decode content blocks for wire shaping: %w", err)
	}

	var callers, searchIDs int64
	changed := false
	for index, rawBlock := range blocks {
		block, err := decodeJSONObject(rawBlock, "content block")
		if err != nil {
			continue
		}
		blockChanged := false
		switch rawString(block["type"]) {
		case "tool_use":
			if _, exists := block["caller"]; !exists {
				block["caller"] = anthropicDirectToolCaller()
				callers++
				blockChanged = true
			}
		case "web_search_tool_result", "web_fetch_tool_result":
			current := rawString(block["tool_use_id"])
			if reshaped := anthropicToolID(current, true); reshaped != current {
				block["tool_use_id"] = mustJSON(reshaped)
				searchIDs++
				blockChanged = true
			}
		}
		if blockChanged {
			encoded, err := json.Marshal(block)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("re-encode content block for wire shaping: %w", err)
			}
			blocks[index] = encoded
			changed = true
		}
	}
	if !changed {
		return nil, callers, searchIDs, nil
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("re-encode content blocks for wire shaping: %w", err)
	}
	return encoded, callers, searchIDs, nil
}

// canonicalOrderedResponse returns a response body with member ordering
// applied, so two bodies can be compared for real content changes without the
// alphabetical re-encoding that intermediate stages introduce showing up as a
// difference.
func canonicalOrderedResponse(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	response, err := decodeJSONObject(raw, "response.response_data")
	if err != nil {
		return nil, err
	}
	return reorderAnthropicResponse(response)
}

func reorderAnthropicResponse(response map[string]json.RawMessage) (json.RawMessage, error) {
	if raw := response["usage"]; isJSONObject(raw) {
		usage, err := decodeJSONObject(raw, "response usage")
		if err != nil {
			return nil, err
		}
		for key, order := range map[string][]string{
			"cache_creation":  anthropicCacheCreationKeyOrder,
			"server_tool_use": anthropicServerToolUseKeyOrder,
		} {
			if nested := usage[key]; isJSONObject(nested) {
				object, err := decodeJSONObject(nested, "response usage."+key)
				if err != nil {
					return nil, err
				}
				encoded, err := marshalOrderedObject(object, order)
				if err != nil {
					return nil, err
				}
				usage[key] = encoded
			}
		}
		encoded, err := marshalOrderedObject(usage, anthropicUsageKeyOrder)
		if err != nil {
			return nil, err
		}
		response["usage"] = encoded
	}
	if raw := response["content"]; len(raw) > 0 {
		reordered, err := reorderAnthropicContentBlocks(raw)
		if err != nil {
			return nil, err
		}
		response["content"] = reordered
	}
	return marshalOrderedObject(response, anthropicResponseKeyOrder)
}

// reorderAnthropicToolDeclarations puts tool members back into the measured
// order. Foreign tools rebuilt during conversion come back out of a Go map in
// alphabetical order, which leaves a request declaring most of its tools in one
// member order and the converted ones in another.
func reorderAnthropicToolDeclarations(raw json.RawMessage) (json.RawMessage, error) {
	var tools []json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return raw, nil
	}
	changed := false
	for index, rawTool := range tools {
		tool, err := decodeJSONObject(rawTool, "tool declaration")
		if err != nil {
			continue
		}
		reordered, err := marshalOrderedObject(tool, anthropicToolKeyOrder)
		if err != nil {
			return nil, fmt.Errorf("re-encode tool declaration for member ordering: %w", err)
		}
		if bytes.Equal(reordered, rawTool) {
			continue
		}
		tools[index] = reordered
		changed = true
	}
	if !changed {
		return raw, nil
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("re-encode request tools for member ordering: %w", err)
	}
	return encoded, nil
}

func reorderAnthropicMessage(message map[string]json.RawMessage) (json.RawMessage, error) {
	if raw := message["content"]; len(raw) > 0 {
		reordered, err := reorderAnthropicContentBlocks(raw)
		if err != nil {
			return nil, err
		}
		message["content"] = reordered
	}
	return marshalOrderedObject(message, anthropicRequestMessageKeyOrder)
}

// reorderAnthropicContentBlocks leaves a string content value and any block
// type outside the allow list byte-identical.
func reorderAnthropicContentBlocks(raw json.RawMessage) (json.RawMessage, error) {
	if !isJSONArray(raw) {
		return raw, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return raw, nil
	}
	for index, rawBlock := range blocks {
		reordered, err := reorderAnthropicContentBlock(rawBlock)
		if err != nil {
			return nil, err
		}
		blocks[index] = reordered
	}
	return json.Marshal(blocks)
}

func reorderAnthropicContentBlock(raw json.RawMessage) (json.RawMessage, error) {
	if !isJSONObject(raw) {
		return raw, nil
	}
	block, err := decodeJSONObject(raw, "content block")
	if err != nil {
		return raw, nil
	}
	order, known := anthropicBlockKeyOrder[rawString(block["type"])]
	if !known {
		return raw, nil
	}
	if nested := block["content"]; isJSONArray(nested) {
		reordered, err := reorderAnthropicContentBlocks(nested)
		if err != nil {
			return nil, err
		}
		block["content"] = reordered
	}
	return marshalOrderedObject(block, order)
}

// marshalOrderedObject encodes an object with the listed members first, in the
// listed order, followed by any remaining members sorted alphabetically.
// Absent members are skipped, so one order list serves every variant.
func marshalOrderedObject(object map[string]json.RawMessage, order []string) (json.RawMessage, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')

	written := 0
	emit := func(key string) error {
		value, exists := object[key]
		if !exists {
			return nil
		}
		if written > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return fmt.Errorf("encode member name %q: %w", key, err)
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 {
			trimmed = []byte("null")
		}
		buffer.Write(trimmed)
		written++
		return nil
	}

	emitted := make(map[string]struct{}, len(order))
	for _, key := range order {
		if _, seen := emitted[key]; seen {
			continue
		}
		emitted[key] = struct{}{}
		if err := emit(key); err != nil {
			return nil, err
		}
	}
	remaining := make([]string, 0, len(object))
	for key := range object {
		if _, seen := emitted[key]; !seen {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	for _, key := range remaining {
		if err := emit(key); err != nil {
			return nil, err
		}
	}

	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// clientVersionPattern reads the version the Claude Code client reports in its
// own billing header inside the system prompt.
var clientVersionPattern = regexp.MustCompile(`cc_version=(\d+\.\d+\.\d+)`)

// deliveryUserAgent reports the client user agent for the record's metadata,
// derived from the version the client itself sent. It returns an empty string
// when the request carries no version: metadata is troubleshooting data, and an
// absent optional member is preferable to inventing a client build number.
func deliveryUserAgent(request map[string]json.RawMessage) string {
	system := request["system"]
	if len(system) == 0 {
		return ""
	}
	match := clientVersionPattern.FindSubmatch(system)
	if match == nil {
		return ""
	}
	return "claude-cli/" + string(match[1])
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}
