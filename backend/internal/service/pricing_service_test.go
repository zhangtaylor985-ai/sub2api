package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePricingData_ParsesPriorityAndServiceTierFields(t *testing.T) {
	svc := &PricingService{}
	body := []byte(`{
		"gpt-5.4": {
			"input_cost_per_token": 0.0000025,
			"input_cost_per_token_priority": 0.000005,
			"output_cost_per_token": 0.000015,
			"output_cost_per_token_priority": 0.00003,
			"cache_creation_input_token_cost": 0.0000025,
			"cache_read_input_token_cost": 0.00000025,
			"cache_read_input_token_cost_priority": 0.0000005,
			"supports_service_tier": true,
			"supports_prompt_caching": true,
			"litellm_provider": "openai",
			"mode": "chat"
		}
	}`)

	data, err := svc.parsePricingData(body)
	require.NoError(t, err)
	pricing := data["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 5e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 3e-5, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 5e-7, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}

func TestGetModelPricing_Gpt53CodexSparkUsesGpt51CodexPricing(t *testing.T) {
	sparkPricing := &LiteLLMModelPricing{InputCostPerToken: 1}
	gpt53Pricing := &LiteLLMModelPricing{InputCostPerToken: 9}

	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": sparkPricing,
			"gpt-5.3":       gpt53Pricing,
		},
	}

	got := svc.GetModelPricing("gpt-5.3-codex-spark")
	require.Same(t, sparkPricing, got)
}

func TestGetModelPricing_Gpt53CodexFallbackStillUsesGpt52Codex(t *testing.T) {
	gpt52CodexPricing := &LiteLLMModelPricing{InputCostPerToken: 2}

	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.2-codex": gpt52CodexPricing,
		},
	}

	got := svc.GetModelPricing("gpt-5.3-codex")
	require.Same(t, gpt52CodexPricing, got)
}

func TestGetModelPricing_OpenAIFallbackMatchedLoggedAsInfo(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	gpt52CodexPricing := &LiteLLMModelPricing{InputCostPerToken: 2}
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.2-codex": gpt52CodexPricing,
		},
	}

	got := svc.GetModelPricing("gpt-5.3-codex")
	require.Same(t, gpt52CodexPricing, got)

	require.True(t, logSink.ContainsMessageAtLevel("[Pricing] OpenAI fallback matched gpt-5.3-codex -> gpt-5.2-codex", "info"))
	require.False(t, logSink.ContainsMessageAtLevel("[Pricing] OpenAI fallback matched gpt-5.3-codex -> gpt-5.2-codex", "warn"))
}

func TestGetModelPricing_Gpt54UsesStaticFallbackWhenRemoteMissing(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": &LiteLLMModelPricing{InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.GetModelPricing("gpt-5.4")
	require.NotNil(t, got)
	require.InDelta(t, 2.5e-6, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.5e-5, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 2.5e-7, got.CacheReadInputTokenCost, 1e-12)
	require.Equal(t, 272000, got.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, got.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, got.LongContextOutputCostMultiplier, 1e-12)
}

func TestGetModelPricing_GPT56StaticFallbacksAreTierSpecific(t *testing.T) {
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}

	tests := []struct {
		model                 string
		input                 float64
		output                float64
		cacheRead             float64
		cacheCreation         float64
		priorityInput         float64
		priorityOutput        float64
		priorityCacheCreation float64
		priorityCacheRead     float64
	}{
		{"gpt-5.6-sol-high", 5e-6, 30e-6, 0.5e-6, 6.25e-6, 10e-6, 60e-6, 12.5e-6, 1e-6},
		{"openai/gpt5.6terra-2026-07-08", 2.5e-6, 15e-6, 0.25e-6, 3.125e-6, 5e-6, 30e-6, 6.25e-6, 0.5e-6},
		{"gpt5.6luna-openai-compact", 1e-6, 6e-6, 0.1e-6, 1.25e-6, 2e-6, 12e-6, 2.5e-6, 0.2e-6},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := svc.GetModelPricing(tt.model)
			require.NotNil(t, got)
			require.InDelta(t, tt.input, got.InputCostPerToken, 1e-12)
			require.InDelta(t, tt.output, got.OutputCostPerToken, 1e-12)
			require.InDelta(t, tt.cacheRead, got.CacheReadInputTokenCost, 1e-12)
			require.InDelta(t, tt.cacheCreation, got.CacheCreationInputTokenCost, 1e-12)
			require.InDelta(t, tt.priorityInput, got.InputCostPerTokenPriority, 1e-12)
			require.InDelta(t, tt.priorityOutput, got.OutputCostPerTokenPriority, 1e-12)
			require.InDelta(t, tt.priorityCacheCreation, got.CacheCreationInputTokenCostPriority, 1e-12)
			require.InDelta(t, tt.priorityCacheRead, got.CacheReadInputTokenCostPriority, 1e-12)
			require.Equal(t, 272000, got.LongContextInputTokenThreshold)
			require.InDelta(t, 2.0, got.LongContextInputCostMultiplier, 1e-12)
			require.InDelta(t, 1.5, got.LongContextOutputCostMultiplier, 1e-12)
		})
	}
}

func TestParsePricingData_GPT56CacheCreationPriority(t *testing.T) {
	svc := &PricingService{}
	data, err := svc.parsePricingData([]byte(`{
		"gpt-5.6-sol": {
			"input_cost_per_token": 0.000005,
			"output_cost_per_token": 0.00003,
			"cache_creation_input_token_cost": 0.00000625,
			"cache_creation_input_token_cost_priority": 0.0000125
		}
	}`))
	require.NoError(t, err)
	require.Contains(t, data, "gpt-5.6-sol")
	require.InDelta(t, 6.25e-6, data["gpt-5.6-sol"].CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 12.5e-6, data["gpt-5.6-sol"].CacheCreationInputTokenCostPriority, 1e-12)
}

func TestGetModelPricing_GPT56KnownSuffixUsesDynamicBasePricing(t *testing.T) {
	base := &LiteLLMModelPricing{InputCostPerToken: 123e-6}
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.6-sol": base,
		},
	}

	got := svc.GetModelPricing("openai/gpt5.6sol-xhigh-openai-compact")
	require.Same(t, base, got)
}

func TestPricingService_GPT56RejectsBareAndNearMissIDs(t *testing.T) {
	invalidPricing := &LiteLLMModelPricing{InputCostPerToken: 999}
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.6":          invalidPricing,
			"gpt-5.6-solstice": invalidPricing,
		},
	}

	for _, model := range []string{"gpt-5.6", "openai/gpt-5.6", "gpt-5.6-solstice", "gpt5.6solstice", "gpt-5.6-sol-unknown"} {
		t.Run(model, func(t *testing.T) {
			require.Nil(t, svc.GetModelPricing(model))
		})
	}
}

func TestDefaultPricingIncludesGPT56ProductionSourceMetadata(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	type metadata struct {
		Input                  float64 `json:"input_cost_per_token"`
		InputAbove272K         float64 `json:"input_cost_per_token_above_272k_tokens"`
		Output                 float64 `json:"output_cost_per_token"`
		OutputAbove272K        float64 `json:"output_cost_per_token_above_272k_tokens"`
		CacheCreation          float64 `json:"cache_creation_input_token_cost"`
		CacheCreationAbove272K float64 `json:"cache_creation_input_token_cost_above_272k_tokens"`
		CacheRead              float64 `json:"cache_read_input_token_cost"`
		CacheReadAbove272K     float64 `json:"cache_read_input_token_cost_above_272k_tokens"`
		PriorityInput          float64 `json:"input_cost_per_token_priority"`
		PriorityOutput         float64 `json:"output_cost_per_token_priority"`
		PriorityCacheCreation  float64 `json:"cache_creation_input_token_cost_priority"`
		PriorityCacheRead      float64 `json:"cache_read_input_token_cost_priority"`
		MaxInputTokens         int     `json:"max_input_tokens"`
		MaxOutputTokens        int     `json:"max_output_tokens"`
		SupportsPromptCaching  bool    `json:"supports_prompt_caching"`
		SupportsServiceTier    bool    `json:"supports_service_tier"`
	}
	var catalog map[string]metadata
	require.NoError(t, json.Unmarshal(data, &catalog))

	tests := []struct {
		model             string
		input             float64
		output            float64
		cacheRead         float64
		cacheCreation     float64
		priorityInput     float64
		priorityOutput    float64
		priorityCacheRead float64
	}{
		{"gpt-5.6-sol", 5e-6, 30e-6, 0.5e-6, 6.25e-6, 10e-6, 60e-6, 1e-6},
		{"gpt-5.6-terra", 2.5e-6, 15e-6, 0.25e-6, 3.125e-6, 5e-6, 30e-6, 0.5e-6},
		{"gpt-5.6-luna", 1e-6, 6e-6, 0.1e-6, 1.25e-6, 2e-6, 12e-6, 0.2e-6},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := catalog[tt.model]
			require.True(t, ok)
			require.InDelta(t, tt.input, got.Input, 1e-12)
			require.InDelta(t, tt.input*2, got.InputAbove272K, 1e-12)
			require.InDelta(t, tt.output, got.Output, 1e-12)
			require.InDelta(t, tt.output*1.5, got.OutputAbove272K, 1e-12)
			require.InDelta(t, tt.cacheCreation, got.CacheCreation, 1e-12)
			require.InDelta(t, tt.cacheCreation*2, got.CacheCreationAbove272K, 1e-12)
			require.InDelta(t, tt.cacheRead, got.CacheRead, 1e-12)
			require.InDelta(t, tt.cacheRead*2, got.CacheReadAbove272K, 1e-12)
			require.InDelta(t, tt.priorityInput, got.PriorityInput, 1e-12)
			require.InDelta(t, tt.priorityOutput, got.PriorityOutput, 1e-12)
			require.InDelta(t, tt.cacheCreation*2, got.PriorityCacheCreation, 1e-12)
			require.InDelta(t, tt.priorityCacheRead, got.PriorityCacheRead, 1e-12)
			require.Equal(t, 400000, got.MaxInputTokens)
			require.Equal(t, 128000, got.MaxOutputTokens)
			require.True(t, got.SupportsPromptCaching)
			require.True(t, got.SupportsServiceTier)
		})
	}
}

func TestGetModelPricing_OpenAICompactAliasUsesStaticFallback(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.GetModelPricing("openai/gpt5.5")
	require.NotNil(t, got)
	require.InDelta(t, 2.5e-6, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.5e-5, got.OutputCostPerToken, 1e-12)
}

func TestDefaultPricingIncludesCodexAutoReview(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	svc := &PricingService{}
	pricingData, err := svc.parsePricingData(data)
	require.NoError(t, err)
	svc.pricingData = pricingData

	got := svc.GetModelPricing("codex-auto-review")
	require.NotNil(t, got)
	require.InDelta(t, 2.5e-6, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.5e-5, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 2.5e-7, got.CacheReadInputTokenCost, 1e-12)
}

func TestGetModelPricing_Gpt54MiniUsesDedicatedStaticFallbackWhenRemoteMissing(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.GetModelPricing("gpt-5.4-mini")
	require.NotNil(t, got)
	require.InDelta(t, 7.5e-7, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 4.5e-6, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 7.5e-8, got.CacheReadInputTokenCost, 1e-12)
	require.Zero(t, got.LongContextInputTokenThreshold)
}

func TestGetModelPricing_Gpt54NanoUsesDedicatedStaticFallbackWhenRemoteMissing(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.GetModelPricing("gpt-5.4-nano")
	require.NotNil(t, got)
	require.InDelta(t, 2e-7, got.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.25e-6, got.OutputCostPerToken, 1e-12)
	require.InDelta(t, 2e-8, got.CacheReadInputTokenCost, 1e-12)
	require.Zero(t, got.LongContextInputTokenThreshold)
}

func TestGetModelPricing_ImageModelDoesNotFallbackToTextModel(t *testing.T) {
	imagePricing := &LiteLLMModelPricing{InputCostPerToken: 3}
	textPricing := &LiteLLMModelPricing{InputCostPerToken: 9}

	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-image-2": imagePricing,
			"gpt-5.4":     textPricing,
		},
	}

	got := svc.GetModelPricing("gpt-image-3")
	require.Same(t, imagePricing, got)
}

func TestParsePricingData_PreservesPriorityAndServiceTierFields(t *testing.T) {
	raw := map[string]any{
		"gpt-5.4": map[string]any{
			"input_cost_per_token":                 2.5e-6,
			"input_cost_per_token_priority":        5e-6,
			"output_cost_per_token":                15e-6,
			"output_cost_per_token_priority":       30e-6,
			"cache_read_input_token_cost":          0.25e-6,
			"cache_read_input_token_cost_priority": 0.5e-6,
			"supports_service_tier":                true,
			"supports_prompt_caching":              true,
			"litellm_provider":                     "openai",
			"mode":                                 "chat",
		},
	}
	body, err := json.Marshal(raw)
	require.NoError(t, err)

	svc := &PricingService{}
	pricingMap, err := svc.parsePricingData(body)
	require.NoError(t, err)

	pricing := pricingMap["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 2.5e-6, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 5e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 15e-6, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 30e-6, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.25e-6, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 0.5e-6, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}

func TestParsePricingData_PreservesServiceTierPriorityFields(t *testing.T) {
	svc := &PricingService{}
	pricingData, err := svc.parsePricingData([]byte(`{
		"gpt-5.4": {
			"input_cost_per_token": 0.0000025,
			"input_cost_per_token_priority": 0.000005,
			"output_cost_per_token": 0.000015,
			"output_cost_per_token_priority": 0.00003,
			"cache_read_input_token_cost": 0.00000025,
			"cache_read_input_token_cost_priority": 0.0000005,
			"supports_service_tier": true,
			"litellm_provider": "openai",
			"mode": "chat"
		}
	}`))
	require.NoError(t, err)

	pricing := pricingData["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 0.0000025, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 0.000005, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.000015, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 0.00003, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.00000025, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 0.0000005, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}

// ---------------------------------------------------------------------------
// ListModelNamesByProvider
// ---------------------------------------------------------------------------

func TestListModelNamesByProvider_ReturnsMatchingModels(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"claude-opus-4-5-20251101": {LiteLLMProvider: "anthropic", InputCostPerToken: 1.5e-5},
			"claude-sonnet-4-5":        {LiteLLMProvider: "anthropic", InputCostPerToken: 3e-6},
			"gpt-4o":                   {LiteLLMProvider: "openai", InputCostPerToken: 5e-6},
			"gemini-2.5-pro":           {LiteLLMProvider: "google", InputCostPerToken: 1.25e-6},
		},
	}

	got := svc.ListModelNamesByProvider("anthropic")
	require.ElementsMatch(t, []string{"claude-opus-4-5-20251101", "claude-sonnet-4-5"}, got)
	// Must be sorted
	require.Equal(t, "claude-opus-4-5-20251101", got[0])
	require.Equal(t, "claude-sonnet-4-5", got[1])
}

func TestListModelNamesByProvider_CaseInsensitive(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-4o": {LiteLLMProvider: "OpenAI", InputCostPerToken: 5e-6},
		},
	}

	got := svc.ListModelNamesByProvider("openai")
	require.Equal(t, []string{"gpt-4o"}, got)

	got2 := svc.ListModelNamesByProvider("OPENAI")
	require.Equal(t, []string{"gpt-4o"}, got2)
}

func TestListModelNamesByProvider_NoMatch(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{
			"gpt-4o": {LiteLLMProvider: "openai", InputCostPerToken: 5e-6},
		},
	}

	got := svc.ListModelNamesByProvider("anthropic")
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestListModelNamesByProvider_EmptyCatalog(t *testing.T) {
	svc := &PricingService{
		pricingData: map[string]*LiteLLMModelPricing{},
	}

	got := svc.ListModelNamesByProvider("openai")
	require.NotNil(t, got)
	require.Empty(t, got)
}
