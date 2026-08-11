package sessiondelivery

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var errMissingTerminalResponse = errors.New("stream is missing a complete terminal response")

type decodedResponse struct {
	Body     json.RawMessage
	Complete bool
	Failed   bool
}

func decodeCapturedResponse(protocol Protocol, raw []byte, maxEventBytes int) (decodedResponse, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return decodedResponse{}, errors.New("empty response body")
	}
	if trimmed[0] == '{' {
		if !json.Valid(trimmed) {
			return decodedResponse{}, errors.New("invalid JSON response body")
		}
		body := append(json.RawMessage(nil), trimmed...)
		return decodedResponse{
			Body:     body,
			Complete: true,
			Failed:   responseBodyFailed(protocol, body),
		}, nil
	}

	switch protocol {
	case ProtocolAnthropicMessages:
		return decodeAnthropicSSE(trimmed, maxEventBytes)
	case ProtocolOpenAIResponses:
		return decodeResponsesSSE(trimmed, maxEventBytes)
	default:
		return decodedResponse{}, fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func responseBodyFailed(protocol Protocol, body json.RawMessage) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return true
	}
	if protocol == ProtocolAnthropicMessages {
		return rawString(object["type"]) == "error"
	}
	return rawString(object["status"]) == "failed" || len(object["error"]) > 0 && string(object["error"]) != "null"
}

func decodeResponsesSSE(raw []byte, maxEventBytes int) (decodedResponse, error) {
	var terminal json.RawMessage
	failed := false
	err := forEachSSEData(raw, maxEventBytes, func(data []byte) error {
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			return nil
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("decode Responses SSE event: %w", err)
		}
		switch event.Type {
		case "response.completed", "response.done", "response.incomplete":
			if len(event.Response) == 0 || string(event.Response) == "null" {
				return nil
			}
			terminal = append(terminal[:0], event.Response...)
			failed = responseBodyFailed(ProtocolOpenAIResponses, terminal)
		case "response.failed", "error":
			failed = true
			if len(event.Response) > 0 && string(event.Response) != "null" {
				terminal = append(terminal[:0], event.Response...)
			}
		}
		return nil
	})
	if err != nil {
		return decodedResponse{}, err
	}
	if len(terminal) == 0 {
		return decodedResponse{Failed: failed}, errMissingTerminalResponse
	}
	return decodedResponse{Body: terminal, Complete: true, Failed: failed}, nil
}

type anthropicStreamBuilder struct {
	message            map[string]any
	blocks             map[int]map[string]any
	inputJSONFragments map[int]*strings.Builder
	terminal           bool
	failed             bool
}

func decodeAnthropicSSE(raw []byte, maxEventBytes int) (decodedResponse, error) {
	builder := &anthropicStreamBuilder{
		blocks:             make(map[int]map[string]any),
		inputJSONFragments: make(map[int]*strings.Builder),
	}
	err := forEachSSEData(raw, maxEventBytes, builder.consume)
	if err != nil {
		return decodedResponse{}, err
	}
	if builder.failed {
		return decodedResponse{Failed: true}, errMissingTerminalResponse
	}
	if !builder.terminal || builder.message == nil {
		return decodedResponse{}, errMissingTerminalResponse
	}

	indices := make([]int, 0, len(builder.blocks))
	for index := range builder.blocks {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	content := make([]any, 0, len(indices))
	for _, index := range indices {
		block := builder.blocks[index]
		if fragment := builder.inputJSONFragments[index]; fragment != nil {
			var input any
			if err := json.Unmarshal([]byte(fragment.String()), &input); err != nil {
				return decodedResponse{}, fmt.Errorf("decode Anthropic tool input at block %d: %w", index, err)
			}
			block["input"] = input
		}
		content = append(content, block)
	}
	builder.message["content"] = content
	body, err := json.Marshal(builder.message)
	if err != nil {
		return decodedResponse{}, fmt.Errorf("encode decoded Anthropic response: %w", err)
	}
	return decodedResponse{Body: body, Complete: true}, nil
}

func (b *anthropicStreamBuilder) consume(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return nil
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode Anthropic SSE event: %w", err)
	}
	typeName, _ := event["type"].(string)
	switch typeName {
	case "message_start":
		message, _ := event["message"].(map[string]any)
		if message == nil {
			return errors.New("Anthropic message_start is missing message")
		}
		b.message = message
		if existing, ok := message["content"].([]any); ok {
			for index, rawBlock := range existing {
				if block, ok := rawBlock.(map[string]any); ok {
					b.blocks[index] = block
				}
			}
		}
	case "content_block_start":
		index, err := sseIndex(event)
		if err != nil {
			return err
		}
		block, _ := event["content_block"].(map[string]any)
		if block == nil {
			return fmt.Errorf("Anthropic content_block_start %d is missing content_block", index)
		}
		b.blocks[index] = block
	case "content_block_delta":
		index, err := sseIndex(event)
		if err != nil {
			return err
		}
		block := b.blocks[index]
		if block == nil {
			return fmt.Errorf("Anthropic delta references unknown block %d", index)
		}
		delta, _ := event["delta"].(map[string]any)
		if delta == nil {
			return fmt.Errorf("Anthropic block %d delta is missing", index)
		}
		deltaType, _ := delta["type"].(string)
		switch deltaType {
		case "text_delta":
			appendMapString(block, "text", mapString(delta, "text"))
		case "thinking_delta":
			appendMapString(block, "thinking", mapString(delta, "thinking"))
		case "signature_delta":
			appendMapString(block, "signature", mapString(delta, "signature"))
		case "input_json_delta":
			fragment := b.inputJSONFragments[index]
			if fragment == nil {
				fragment = &strings.Builder{}
				b.inputJSONFragments[index] = fragment
			}
			_, _ = fragment.WriteString(mapString(delta, "partial_json"))
		case "citations_delta":
			if citation, ok := delta["citation"].(map[string]any); ok {
				citations, _ := block["citations"].([]any)
				block["citations"] = append(citations, citation)
			}
		}
	case "message_delta":
		if b.message == nil {
			return errors.New("Anthropic message_delta arrived before message_start")
		}
		if delta, ok := event["delta"].(map[string]any); ok {
			for _, key := range []string{"stop_reason", "stop_sequence"} {
				if value, exists := delta[key]; exists {
					b.message[key] = value
				}
			}
		}
		if usage, ok := event["usage"].(map[string]any); ok {
			current, _ := b.message["usage"].(map[string]any)
			if current == nil {
				current = make(map[string]any)
			}
			for key, value := range usage {
				current[key] = value
			}
			b.message["usage"] = current
		}
	case "message_stop":
		b.terminal = true
	case "error":
		b.failed = true
	}
	return nil
}

func forEachSSEData(raw []byte, maxEventBytes int, fn func([]byte) error) error {
	if maxEventBytes <= 0 {
		maxEventBytes = 256 << 20
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	initial := 64 << 10
	if maxEventBytes < initial {
		initial = maxEventBytes
	}
	scanner.Buffer(make([]byte, initial), maxEventBytes)

	var dataLines [][]byte
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		payload := bytes.Join(dataLines, []byte("\n"))
		dataLines = dataLines[:0]
		return fn(payload)
	}
	for scanner.Scan() {
		line := bytes.TrimSuffix(scanner.Bytes(), []byte{'\r'})
		if len(line) == 0 {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			value := bytes.TrimPrefix(line, []byte("data:"))
			value = bytes.TrimPrefix(value, []byte{' '})
			dataLines = append(dataLines, append([]byte(nil), value...))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan SSE response: %w", err)
	}
	return flush()
}

func sseIndex(event map[string]any) (int, error) {
	value, ok := event["index"].(float64)
	if !ok || value < 0 || value != float64(int(value)) {
		return 0, errors.New("Anthropic SSE event has invalid block index")
	}
	return int(value), nil
}

func mapString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func appendMapString(values map[string]any, key, suffix string) {
	current, _ := values[key].(string)
	values[key] = current + suffix
}
