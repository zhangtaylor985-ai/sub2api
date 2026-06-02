package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestedModelFamily(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  APIKeyModelFamily
	}{
		{model: "claude-opus-4-7", want: APIKeyModelFamilyClaude},
		{model: "gpt-5.5", want: APIKeyModelFamilyGPT},
		{model: "chatgpt-5", want: APIKeyModelFamilyGPT},
		{model: "o3", want: APIKeyModelFamilyGPT},
		{model: "o4-mini", want: APIKeyModelFamilyGPT},
		{model: "gpt-image-2", want: APIKeyModelFamilyGPT},
		{model: "dall-e-3", want: APIKeyModelFamilyGPT},
		{model: "gemini-2.5-pro", want: APIKeyModelFamilyUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			require.Equal(t, tt.want, RequestedModelFamily(tt.model))
		})
	}
}

func TestAPIKeyModelFamilyPolicy(t *testing.T) {
	t.Parallel()

	claudeOnly := &APIKey{AllowClaudeFamily: true, AllowGPTFamily: false, ModelFamilyPolicySet: true}
	gptOnly := &APIKey{AllowClaudeFamily: false, AllowGPTFamily: true, ModelFamilyPolicySet: true}
	both := &APIKey{AllowClaudeFamily: true, AllowGPTFamily: true, ModelFamilyPolicySet: true}
	legacyUnset := &APIKey{}

	require.False(t, claudeOnly.IsModelFamilyDenied("claude-opus-4-7"))
	require.True(t, claudeOnly.IsModelFamilyDenied("gpt-5.5"))
	require.True(t, claudeOnly.IsOpenAIEndpointDenied("claude-opus-4-7"))

	require.True(t, gptOnly.IsModelFamilyDenied("claude-sonnet-4-6"))
	require.False(t, gptOnly.IsOpenAIEndpointDenied("gpt-5.5"))

	require.False(t, both.IsOpenAIEndpointDenied("claude-opus-4-7"))
	require.False(t, legacyUnset.IsOpenAIEndpointDenied("gpt-5.5"))
	require.False(t, legacyUnset.IsModelFamilyDenied("claude-opus-4-7"))
}
