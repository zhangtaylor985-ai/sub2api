package service

import "strings"

type APIKeyModelFamily string

const (
	APIKeyModelFamilyUnknown APIKeyModelFamily = ""
	APIKeyModelFamilyClaude  APIKeyModelFamily = "claude"
	APIKeyModelFamilyGPT     APIKeyModelFamily = "gpt"
)

const APIKeyModelAccessDeniedMessage = "Model access denied"

// RequestedModelFamily classifies the client-requested model namespace.
// It intentionally does not inspect any internal routing target.
func RequestedModelFamily(model string) APIKeyModelFamily {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(normalized, "claude-"):
		return APIKeyModelFamilyClaude
	case strings.HasPrefix(normalized, "gpt-"),
		strings.HasPrefix(normalized, "chatgpt-"),
		strings.HasPrefix(normalized, "o1"),
		strings.HasPrefix(normalized, "o3"),
		strings.HasPrefix(normalized, "o4"),
		strings.HasPrefix(normalized, "gpt_image"),
		strings.HasPrefix(normalized, "gpt-image"),
		strings.HasPrefix(normalized, "dall-e"):
		return APIKeyModelFamilyGPT
	default:
		return APIKeyModelFamilyUnknown
	}
}

func (k *APIKey) AllowsClaudeFamily() bool {
	if k == nil || !k.ModelFamilyPolicySet {
		return true
	}
	return k.AllowClaudeFamily
}

func (k *APIKey) AllowsGPTFamily() bool {
	if k == nil || !k.ModelFamilyPolicySet {
		return true
	}
	return k.AllowGPTFamily
}

func (k *APIKey) IsModelFamilyDenied(model string) bool {
	switch RequestedModelFamily(model) {
	case APIKeyModelFamilyClaude:
		return !k.AllowsClaudeFamily()
	case APIKeyModelFamilyGPT:
		return !k.AllowsGPTFamily()
	default:
		return false
	}
}

// IsOpenAIEndpointDenied reports whether an OpenAI-shaped endpoint is denied
// by this API key. Claude-only keys must not use OpenAI route shapes even if
// the model string itself is unknown.
func (k *APIKey) IsOpenAIEndpointDenied(model string) bool {
	if !k.AllowsGPTFamily() {
		return true
	}
	return k.IsModelFamilyDenied(model)
}

func NormalizeAPIKeyModelFamilyPolicy(k *APIKey) {
	if k == nil {
		return
	}
	if !k.ModelFamilyPolicySet {
		k.AllowClaudeFamily = true
		k.AllowGPTFamily = true
		k.ModelFamilyPolicySet = true
	}
}
