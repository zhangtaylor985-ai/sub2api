package service

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	OpenAIContextWindowExceededClientMessage    = "Conversation context is too long. Please compact the conversation or start a new conversation, then try again."
	AnthropicContextWindowExceededClientMessage = "Conversation context is too long. Run /compact or start a new conversation, then try again."
)

// IsOpenAIContextWindowExceededMessage recognizes only explicit context-window
// errors so unrelated invalid requests are not mislabeled as oversized context.
func IsOpenAIContextWindowExceededMessage(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"context_length_exceeded",
		"context length exceeded",
		"context window exceeded",
		"exceeds the context window",
		"maximum context length",
		"max context length",
		"conversation context is too long",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// IsOpenAIContextWindowExceededError allows handlers to avoid appending a
// generic retryable gateway error after a protocol-specific error was written.
func IsOpenAIContextWindowExceededError(err error) bool {
	return err != nil && IsOpenAIContextWindowExceededMessage(err.Error())
}

func rewriteOpenAIContextWindowErrorPayload(payload []byte) ([]byte, bool) {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload, false
	}
	message := extractOpenAISSEErrorMessage(payload)
	code := gjson.GetBytes(payload, "response.error.code").String()
	if code == "" {
		code = gjson.GetBytes(payload, "error.code").String()
	}
	if !IsOpenAIContextWindowExceededMessage(message + " " + code) {
		return payload, false
	}

	basePath := "error"
	if responseError := gjson.GetBytes(payload, "response.error"); responseError.Exists() {
		basePath = "response.error"
	}

	rewritten := append([]byte(nil), payload...)
	var err error
	rewritten, err = sjson.SetBytes(rewritten, basePath+".type", "invalid_request_error")
	if err != nil {
		return payload, false
	}
	rewritten, err = sjson.SetBytes(rewritten, basePath+".code", "context_length_exceeded")
	if err != nil {
		return payload, false
	}
	rewritten, err = sjson.SetBytes(rewritten, basePath+".message", OpenAIContextWindowExceededClientMessage)
	if err != nil {
		return payload, false
	}
	return rewritten, true
}
