package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIsOpenAIContextWindowExceededMessage(t *testing.T) {
	t.Parallel()

	for _, message := range []string{
		"Your input exceeds the context window of this model. Please adjust your input and try again.",
		"maximum context length is 128000 tokens",
		`{"code":"context_length_exceeded"}`,
		"Context window exceeded",
	} {
		require.True(t, IsOpenAIContextWindowExceededMessage(message), message)
		require.True(t, IsOpenAIContextWindowExceededError(errors.New(message)), message)
	}

	for _, message := range []string{
		"Invalid property name: value is too long",
		"OpenAI stream disconnected before completion",
		"Selected model is at capacity",
		"Upstream request failed",
	} {
		require.False(t, IsOpenAIContextWindowExceededMessage(message), message)
		require.False(t, IsOpenAIContextWindowExceededError(errors.New(message)), message)
	}
}

func TestRewriteOpenAIContextWindowErrorPayload(t *testing.T) {
	t.Parallel()

	for _, payload := range [][]byte{
		[]byte(`{"type":"response.failed","response":{"error":{"message":"Your input exceeds the context window of this model."}}}`),
		[]byte(`{"type":"response.failed","error":{"message":"maximum context length is 128000 tokens"}}`),
		[]byte(`{"type":"response.failed","response":{"error":{"code":"context_length_exceeded"}}}`),
	} {
		rewritten, ok := rewriteOpenAIContextWindowErrorPayload(payload)
		require.True(t, ok)

		basePath := "error"
		if gjson.GetBytes(rewritten, "response.error").Exists() {
			basePath = "response.error"
		}
		require.Equal(t, "invalid_request_error", gjson.GetBytes(rewritten, basePath+".type").String())
		require.Equal(t, "context_length_exceeded", gjson.GetBytes(rewritten, basePath+".code").String())
		require.Equal(t, OpenAIContextWindowExceededClientMessage, gjson.GetBytes(rewritten, basePath+".message").String())
	}
}

func TestRewriteOpenAIContextWindowErrorPayloadDoesNotRewriteUnrelatedError(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"type":"response.failed","error":{"message":"Invalid property name: value is too long"}}`)
	rewritten, ok := rewriteOpenAIContextWindowErrorPayload(payload)
	require.False(t, ok)
	require.Equal(t, payload, rewritten)
}
