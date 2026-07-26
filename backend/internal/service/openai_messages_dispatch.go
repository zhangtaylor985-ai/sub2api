package service

import "strings"

const (
	defaultOpenAIMessagesDispatchOpusMappedModel   = "gpt-5.4"
	defaultOpenAIMessagesDispatchSonnetMappedModel = "gpt-5.4"
	defaultOpenAIMessagesDispatchHaikuMappedModel  = "gpt-5.4-mini"

	OpenAIMessagesDispatchTargetGPT54 = "gpt-5.4"
	OpenAIMessagesDispatchTargetGPT56 = "gpt-5.6-sol"

	// DefaultOpenAIMessagesDispatchTarget is the product default used when the
	// global setting has not been persisted yet.
	DefaultOpenAIMessagesDispatchTarget = OpenAIMessagesDispatchTargetGPT56
	// SafeOpenAIMessagesDispatchTarget is used only when the settings backend
	// is unavailable or contains an invalid value.
	SafeOpenAIMessagesDispatchTarget = OpenAIMessagesDispatchTargetGPT54

	OpenAIMessagesDispatchFableModel       = "claude-fable-5"
	OpenAIMessagesDispatchFableTargetModel = "gpt-5.4"
)

func normalizeOpenAIMessagesDispatchMappedModel(model string) string {
	model = NormalizeOpenAICompatRequestedModel(strings.TrimSpace(model))
	return strings.TrimSpace(model)
}

func IsValidOpenAIMessagesDispatchTarget(model string) bool {
	switch strings.TrimSpace(model) {
	case OpenAIMessagesDispatchTargetGPT54, OpenAIMessagesDispatchTargetGPT56:
		return true
	default:
		return false
	}
}

func NormalizeOpenAIMessagesDispatchTarget(model string) string {
	model = strings.TrimSpace(model)
	if IsValidOpenAIMessagesDispatchTarget(model) {
		return model
	}
	return DefaultOpenAIMessagesDispatchTarget
}

func normalizeOpenAIMessagesDispatchModelConfig(cfg OpenAIMessagesDispatchModelConfig) OpenAIMessagesDispatchModelConfig {
	out := OpenAIMessagesDispatchModelConfig{
		OpusMappedModel:   normalizeOpenAIMessagesDispatchMappedModel(cfg.OpusMappedModel),
		SonnetMappedModel: normalizeOpenAIMessagesDispatchMappedModel(cfg.SonnetMappedModel),
		HaikuMappedModel:  normalizeOpenAIMessagesDispatchMappedModel(cfg.HaikuMappedModel),
	}

	if len(cfg.ExactModelMappings) > 0 {
		out.ExactModelMappings = make(map[string]string, len(cfg.ExactModelMappings))
		for requestedModel, mappedModel := range cfg.ExactModelMappings {
			requestedModel = strings.TrimSpace(requestedModel)
			mappedModel = normalizeOpenAIMessagesDispatchMappedModel(mappedModel)
			if requestedModel == "" || mappedModel == "" {
				continue
			}
			out.ExactModelMappings[requestedModel] = mappedModel
		}
		if len(out.ExactModelMappings) == 0 {
			out.ExactModelMappings = nil
		}
	}

	return out
}

func claudeMessagesDispatchFamily(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(normalized, "claude") {
		return ""
	}
	switch {
	case strings.Contains(normalized, "opus"):
		return "opus"
	case strings.Contains(normalized, "sonnet"):
		return "sonnet"
	case strings.Contains(normalized, "haiku"):
		return "haiku"
	case strings.Contains(normalized, "fable"):
		return "fable"
	default:
		return ""
	}
}

func (g *Group) ResolveMessagesDispatchModel(requestedModel string) string {
	if g == nil {
		return ""
	}
	return resolveOpenAIMessagesDispatchModelConfig(g.MessagesDispatchModelConfig, requestedModel, true)
}

// ResolveMessagesDispatchModelOverride resolves only explicit group mappings.
// Global and code defaults are deliberately excluded so callers can apply the
// precedence API key > group > global > safe fallback.
func (g *Group) ResolveMessagesDispatchModelOverride(requestedModel string) string {
	if g == nil {
		return ""
	}
	return resolveOpenAIMessagesDispatchModelConfig(g.MessagesDispatchModelConfig, requestedModel, false)
}

func (k *APIKey) ResolveMessagesDispatchModel(requestedModel string) string {
	if k == nil {
		return ""
	}
	return resolveOpenAIMessagesDispatchModelConfig(k.MessagesDispatchModelConfig, requestedModel, false)
}

func ResolveOpenAIMessagesDispatchDefaultModel(requestedModel, target string) string {
	switch claudeMessagesDispatchFamily(requestedModel) {
	case "opus", "sonnet":
		if !IsValidOpenAIMessagesDispatchTarget(target) {
			target = SafeOpenAIMessagesDispatchTarget
		}
		return normalizeOpenAIMessagesDispatchMappedModel(target)
	case "haiku":
		return defaultOpenAIMessagesDispatchHaikuMappedModel
	case "fable":
		return OpenAIMessagesDispatchFableTargetModel
	default:
		return ""
	}
}

func resolveOpenAIMessagesDispatchModelConfig(cfg OpenAIMessagesDispatchModelConfig, requestedModel string, includeDefaults bool) string {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ""
	}

	cfg = normalizeOpenAIMessagesDispatchModelConfig(cfg)
	if mappedModel := strings.TrimSpace(cfg.ExactModelMappings[requestedModel]); mappedModel != "" {
		return mappedModel
	}

	switch claudeMessagesDispatchFamily(requestedModel) {
	case "opus":
		if mappedModel := strings.TrimSpace(cfg.OpusMappedModel); mappedModel != "" {
			return mappedModel
		}
		if !includeDefaults {
			return ""
		}
		return defaultOpenAIMessagesDispatchOpusMappedModel
	case "sonnet":
		if mappedModel := strings.TrimSpace(cfg.SonnetMappedModel); mappedModel != "" {
			return mappedModel
		}
		if !includeDefaults {
			return ""
		}
		return defaultOpenAIMessagesDispatchSonnetMappedModel
	case "haiku":
		if mappedModel := strings.TrimSpace(cfg.HaikuMappedModel); mappedModel != "" {
			return mappedModel
		}
		if !includeDefaults {
			return ""
		}
		return defaultOpenAIMessagesDispatchHaikuMappedModel
	case "fable":
		if !includeDefaults {
			return ""
		}
		return OpenAIMessagesDispatchFableTargetModel
	default:
		return ""
	}
}

func sanitizeGroupMessagesDispatchFields(g *Group) {
	if g == nil || g.Platform == PlatformOpenAI {
		return
	}
	g.AllowMessagesDispatch = false
	g.DefaultMappedModel = ""
	g.MessagesDispatchModelConfig = OpenAIMessagesDispatchModelConfig{}
}
