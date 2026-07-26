package service

import "testing"

import "github.com/stretchr/testify/require"

func TestNormalizeOpenAIMessagesDispatchModelConfig(t *testing.T) {
	t.Parallel()

	cfg := normalizeOpenAIMessagesDispatchModelConfig(OpenAIMessagesDispatchModelConfig{
		OpusMappedModel:   " gpt-5.4-high ",
		SonnetMappedModel: "gpt-5.3-codex",
		HaikuMappedModel:  " gpt-5.4-mini-medium ",
		ExactModelMappings: map[string]string{
			" claude-sonnet-4-5-20250929 ": " gpt-5.2-high ",
			"":                             "gpt-5.4",
			"claude-opus-4-6":              " ",
		},
	})

	require.Equal(t, "gpt-5.4", cfg.OpusMappedModel)
	require.Equal(t, "gpt-5.3-codex", cfg.SonnetMappedModel)
	require.Equal(t, "gpt-5.4-mini", cfg.HaikuMappedModel)
	require.Equal(t, map[string]string{
		"claude-sonnet-4-5-20250929": "gpt-5.2",
	}, cfg.ExactModelMappings)
}

func TestAPIKeyResolveMessagesDispatchModel_DoesNotUseDefaults(t *testing.T) {
	t.Parallel()

	key := &APIKey{
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			OpusMappedModel: "gpt-5.4",
		},
	}

	require.Equal(t, "gpt-5.4", key.ResolveMessagesDispatchModel("claude-opus-4-7"))
	require.Empty(t, key.ResolveMessagesDispatchModel("claude-sonnet-4-6"))
	require.Empty(t, key.ResolveMessagesDispatchModel("claude-sonnet-5"))
	require.Empty(t, key.ResolveMessagesDispatchModel("claude-haiku-4-5"))
}

func TestGroupResolveMessagesDispatchModel_Sonnet5UsesGPT54Default(t *testing.T) {
	t.Parallel()

	group := &Group{}
	require.Equal(t, "gpt-5.4", group.ResolveMessagesDispatchModel("claude-sonnet-5"))
	require.Equal(t, "gpt-5.4", group.ResolveMessagesDispatchModel("claude-sonnet-5-20260701"))
}

func TestAPIKeyResolveMessagesDispatchModel_Sonnet5OverrideWins(t *testing.T) {
	t.Parallel()

	key := &APIKey{
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: "gpt-5.3-codex",
		},
		Group: &Group{
			MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
				SonnetMappedModel: "gpt-5.4",
			},
		},
	}

	require.Equal(t, "gpt-5.3-codex", key.ResolveMessagesDispatchModel("claude-sonnet-5"))
}

func TestAPIKeyResolveMessagesDispatchModel_ExactMappingWins(t *testing.T) {
	t.Parallel()

	key := &APIKey{
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: "gpt-5.4",
			ExactModelMappings: map[string]string{
				"claude-sonnet-4-6": "gpt-5.5",
			},
		},
	}

	require.Equal(t, "gpt-5.5", key.ResolveMessagesDispatchModel("claude-sonnet-4-6"))
	require.Equal(t, "gpt-5.4", key.ResolveMessagesDispatchModel("claude-sonnet-4-7"))
}

func TestResolveMessagesDispatchModel_FableUsesGroupDefault(t *testing.T) {
	t.Parallel()

	key := &APIKey{
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			OpusMappedModel: OpenAIMessagesDispatchFableTargetModel,
		},
		Group: &Group{},
	}

	require.Empty(t, key.ResolveMessagesDispatchModel(OpenAIMessagesDispatchFableModel))
	require.Equal(t, OpenAIMessagesDispatchFableTargetModel, key.Group.ResolveMessagesDispatchModel(OpenAIMessagesDispatchFableModel))

	key.MessagesDispatchModelConfig.ExactModelMappings = map[string]string{
		OpenAIMessagesDispatchFableModel: OpenAIMessagesDispatchFableTargetModel,
	}
	require.Equal(t, OpenAIMessagesDispatchFableTargetModel, key.ResolveMessagesDispatchModel(OpenAIMessagesDispatchFableModel))
}
