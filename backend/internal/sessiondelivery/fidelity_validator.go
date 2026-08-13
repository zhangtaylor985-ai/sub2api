package sessiondelivery

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/thinkingsig"
)

var forbiddenOpenAIContentBlockTypes = map[string]struct{}{
	"encrypted_content": {},
	"input_image":       {},
	"input_text":        {},
	"output_text":       {},
	"reasoning":         {},
}

// ValidateDeliveryFidelity applies the stricter Claude Code x Opus 5 shape
// checks used for final delivery acceptance. ValidateDelivery remains the
// compatibility validator so older archives can still be read and rebuilt.
func ValidateDeliveryFidelity(record *DeliveryRecord, publicModel string) error {
	if err := ValidateDelivery(record, publicModel); err != nil {
		return err
	}
	request, err := decodeJSONObject(record.Request, "request")
	if err != nil {
		return err
	}
	response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
	if err != nil {
		return err
	}

	var thinkingConfig struct {
		Type    string `json:"type"`
		Display string `json:"display"`
	}
	if raw := request["thinking"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &thinkingConfig); err != nil {
			return errors.New("request.thinking must be an object")
		}
	}
	if thinkingConfig.Type == "adaptive" && thinkingConfig.Display != "omitted" {
		return errors.New("adaptive request thinking must use display=omitted")
	}

	if err := validateRequestHistoryFidelity(request["messages"], publicModel); err != nil {
		return err
	}
	thinkingBlocks, redactedBlocks, err := validateResponseContentFidelity(
		response["content"],
		publicModel,
		thinkingConfig.Type == "adaptive" && thinkingConfig.Display == "omitted",
	)
	if err != nil {
		return err
	}
	thinkingEnabled := thinkingConfig.Type == "enabled" || thinkingConfig.Type == "adaptive"
	if thinkingEnabled && thinkingBlocks+redactedBlocks == 0 {
		return errors.New("thinking-enabled request must have a thinking response block")
	}
	if !thinkingEnabled && thinkingBlocks+redactedBlocks > 0 {
		return errors.New("thinking-disabled request must not have a thinking response block")
	}
	if thinkingEnabled {
		var content []map[string]json.RawMessage
		if err := json.Unmarshal(response["content"], &content); err != nil || len(content) == 0 {
			return errors.New("thinking-enabled response content must not be empty")
		}
		firstType := rawString(content[0]["type"])
		if firstType != "thinking" && firstType != "redacted_thinking" {
			return errors.New("thinking-enabled response must start with a thinking block")
		}
	}
	return validateUsageFidelity(response["usage"], response["content"])
}

func validateRequestHistoryFidelity(messagesRaw json.RawMessage, publicModel string) error {
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(messagesRaw, &messages); err != nil {
		return errors.New("request.messages must be an array of objects")
	}
	for messageIndex, message := range messages {
		var content []map[string]json.RawMessage
		if err := json.Unmarshal(message["content"], &content); err != nil {
			continue // string message content is valid
		}
		for blockIndex, block := range content {
			blockType := rawString(block["type"])
			if _, forbidden := forbiddenOpenAIContentBlockTypes[blockType]; forbidden {
				return fmt.Errorf("request.messages[%d].content[%d] contains OpenAI block type %q", messageIndex, blockIndex, blockType)
			}
			switch blockType {
			case "thinking":
				signature := rawString(block["signature"])
				if signature == "" {
					return fmt.Errorf("request.messages[%d].content[%d] thinking signature is empty", messageIndex, blockIndex)
				}
				if err := validateOpus5SignatureShape(signature, publicModel); err != nil {
					return fmt.Errorf("request.messages[%d].content[%d] thinking signature: %w", messageIndex, blockIndex, err)
				}
			case "tool_use":
				if !strings.HasPrefix(rawString(block["id"]), "toolu_") {
					return fmt.Errorf("request.messages[%d].content[%d] tool_use id must use toolu_", messageIndex, blockIndex)
				}
			case "server_tool_use":
				if !strings.HasPrefix(rawString(block["id"]), "srvtoolu_") {
					return fmt.Errorf("request.messages[%d].content[%d] server_tool_use id must use srvtoolu_", messageIndex, blockIndex)
				}
			case "tool_result":
				id := rawString(block["tool_use_id"])
				if !strings.HasPrefix(id, "toolu_") && !strings.HasPrefix(id, "srvtoolu_") {
					return fmt.Errorf("request.messages[%d].content[%d] tool_result id must use an Anthropic prefix", messageIndex, blockIndex)
				}
			case "web_search_tool_result", "web_fetch_tool_result":
				if !strings.HasPrefix(rawString(block["tool_use_id"]), "srvtoolu_") {
					return fmt.Errorf("request.messages[%d].content[%d] server tool result id must use srvtoolu_", messageIndex, blockIndex)
				}
			}
		}
	}
	return nil
}

func validateResponseContentFidelity(contentRaw json.RawMessage, publicModel string, displayOmitted bool) (int, int, error) {
	var content []map[string]json.RawMessage
	if err := json.Unmarshal(contentRaw, &content); err != nil {
		return 0, 0, errors.New("response.response_data.content must be an array of objects")
	}
	thinkingBlocks := 0
	redactedBlocks := 0
	for index, block := range content {
		switch rawString(block["type"]) {
		case "thinking":
			thinkingBlocks++
			if displayOmitted && rawString(block["thinking"]) != "" {
				return 0, 0, fmt.Errorf("response content[%d] display-omitted thinking must be empty", index)
			}
			if err := validateOpus5SignatureShape(rawString(block["signature"]), publicModel); err != nil {
				return 0, 0, fmt.Errorf("response content[%d] thinking signature: %w", index, err)
			}
		case "redacted_thinking":
			redactedBlocks++
			if rawString(block["data"]) == "" {
				return 0, 0, fmt.Errorf("response content[%d] redacted thinking data is empty", index)
			}
		case "tool_use":
			if !strings.HasPrefix(rawString(block["id"]), "toolu_") {
				return 0, 0, fmt.Errorf("response content[%d] tool_use id must use toolu_", index)
			}
		case "server_tool_use":
			if !strings.HasPrefix(rawString(block["id"]), "srvtoolu_") {
				return 0, 0, fmt.Errorf("response content[%d] server_tool_use id must use srvtoolu_", index)
			}
		}
	}
	return thinkingBlocks, redactedBlocks, nil
}

func validateUsageFidelity(usageRaw, responseContent json.RawMessage) error {
	usage, err := decodeJSONObject(usageRaw, "response.response_data.usage")
	if err != nil {
		return err
	}
	required := []string{
		"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens",
		"cache_creation", "server_tool_use", "service_tier", "inference_geo", "iterations", "speed",
	}
	for _, key := range required {
		if len(usage[key]) == 0 {
			return fmt.Errorf("response usage is missing %q", key)
		}
	}
	if rawInt(usage["input_tokens"]) != anthropicUncachedTail {
		return fmt.Errorf("response usage input_tokens must be %d", anthropicUncachedTail)
	}
	if rawInt(usage["output_tokens"]) < 0 || rawInt(usage["cache_creation_input_tokens"]) < 0 || rawInt(usage["cache_read_input_tokens"]) < 0 {
		return errors.New("response usage token counts must be non-negative")
	}
	var cacheCreation struct {
		Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
	}
	if err := json.Unmarshal(usage["cache_creation"], &cacheCreation); err != nil {
		return errors.New("response usage cache_creation must be an object")
	}
	if cacheCreation.Ephemeral5m != rawInt(usage["cache_creation_input_tokens"]) || cacheCreation.Ephemeral1h != 0 {
		return errors.New("response usage cache_creation split is inconsistent")
	}
	var serverUsage struct {
		WebSearch int `json:"web_search_requests"`
		WebFetch  int `json:"web_fetch_requests"`
	}
	if err := json.Unmarshal(usage["server_tool_use"], &serverUsage); err != nil {
		return errors.New("response usage server_tool_use must be an object")
	}
	if serverUsage.WebSearch != countServerToolCalls(responseContent, "web_search") ||
		serverUsage.WebFetch != countServerToolCalls(responseContent, "web_fetch") {
		return errors.New("response usage server tool counters do not match response content")
	}
	if rawString(usage["service_tier"]) != "standard" || rawString(usage["inference_geo"]) != "global" || rawString(usage["speed"]) != "standard" {
		return errors.New("response usage service_tier/inference_geo/speed does not match Opus 5 baseline")
	}
	var iterations []json.RawMessage
	if err := json.Unmarshal(usage["iterations"], &iterations); err != nil {
		return errors.New("response usage iterations must be an array")
	}
	return nil
}

type protobufField struct {
	number int
	wire   int
	value  uint64
	data   []byte
}

func validateOpus5SignatureShape(encoded, publicModel string) error {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return errors.New("is not standard base64")
	}
	outer, err := parseProtobufFields(raw)
	if err != nil || len(outer) != 3 || outer[0].number != 1 || outer[0].wire != 0 || outer[0].value != 2 ||
		outer[1].number != 2 || outer[1].wire != 2 || outer[2].number != 3 || outer[2].wire != 0 || outer[2].value != 1 {
		return errors.New("does not use the Opus 5 outer envelope")
	}
	inner, err := parseProtobufFields(outer[1].data)
	if err != nil || len(inner) != 5 {
		return errors.New("does not use the five-field encrypted envelope")
	}
	for index, field := range inner {
		if field.number != index+1 || field.wire != 2 {
			return errors.New("encrypted envelope fields are malformed")
		}
	}
	if len(inner[1].data) != 12 || len(inner[2].data) != 12 || len(inner[3].data) != 48 || len(inner[4].data) < 800 {
		return errors.New("encrypted envelope component lengths are implausible")
	}
	meta, err := parseProtobufFields(inner[0].data)
	if err != nil || len(meta) != 7 {
		return errors.New("signature metadata is malformed")
	}
	wantNumbers := []int{1, 3, 5, 6, 7, 8, 11}
	for index, number := range wantNumbers {
		if meta[index].number != number {
			return errors.New("signature metadata fields are malformed")
		}
	}
	if meta[0].value != 16 || meta[1].value != 2 || len(meta[2].data) != 64 ||
		string(meta[3].data) != publicModel || meta[4].value != 1 || string(meta[5].data) != "thinking" ||
		string(meta[6].data) != thinkingsig.DefaultReasoningUUID {
		return errors.New("signature metadata does not match the Opus 5 baseline")
	}
	return nil
}

func parseProtobufFields(raw []byte) ([]protobufField, error) {
	fields := make([]protobufField, 0, 8)
	for offset := 0; offset < len(raw); {
		key, used, err := consumeProtobufVarint(raw[offset:])
		if err != nil {
			return nil, err
		}
		offset += used
		field := protobufField{number: int(key >> 3), wire: int(key & 7)}
		switch field.wire {
		case 0:
			field.value, used, err = consumeProtobufVarint(raw[offset:])
			if err != nil {
				return nil, err
			}
			offset += used
		case 2:
			length, lengthUsed, lengthErr := consumeProtobufVarint(raw[offset:])
			if lengthErr != nil || length > uint64(len(raw)-offset-lengthUsed) {
				return nil, errors.New("truncated protobuf field")
			}
			offset += lengthUsed
			field.data = raw[offset : offset+int(length)]
			offset += int(length)
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", field.wire)
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func consumeProtobufVarint(raw []byte) (uint64, int, error) {
	var value uint64
	for index, b := range raw {
		if index >= 10 {
			return 0, 0, errors.New("protobuf varint overflow")
		}
		value |= uint64(b&0x7f) << (7 * index)
		if b&0x80 == 0 {
			return value, index + 1, nil
		}
	}
	return 0, 0, errors.New("truncated protobuf varint")
}
