package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestRedactClaudeOnlyModelStatsMapsGPTToClaudeAliases(t *testing.T) {
	stats := []usagestats.ModelStat{
		{Model: "gpt-5.4", Requests: 2, InputTokens: 10, OutputTokens: 3, CacheReadTokens: 4, TotalTokens: 17, Cost: 1, ActualCost: 1},
		{Model: "claude-opus-4-7", Requests: 1, InputTokens: 5, OutputTokens: 2, TotalTokens: 7, Cost: 0.5, ActualCost: 0.5},
		{Model: "gpt-5.5", Requests: 3, InputTokens: 30, OutputTokens: 6, TotalTokens: 36, Cost: 2, ActualCost: 2},
		{Model: "gpt-5.3-codex", Requests: 4, InputTokens: 40, OutputTokens: 8, TotalTokens: 48, Cost: 3, ActualCost: 3},
		{Model: "gpt-5.4-mini", Requests: 5, InputTokens: 50, OutputTokens: 10, TotalTokens: 60, Cost: 4, ActualCost: 4},
		{Model: "claude-fable-5", Requests: 6, InputTokens: 60, OutputTokens: 12, TotalTokens: 72, Cost: 5, ActualCost: 5},
	}

	got := redactClaudeOnlyModelStats(stats)

	require.Len(t, got, 5)
	require.Equal(t, "claude-fable-5", got[0].Model)
	require.Equal(t, int64(6), got[0].Requests)
	require.Equal(t, "claude-haiku-4-5-20251001", got[1].Model)
	require.Equal(t, int64(5), got[1].Requests)
	require.Equal(t, "claude-sonnet-4-6", got[2].Model)
	require.Equal(t, int64(4), got[2].Requests)
	require.Equal(t, "claude-opus-4-8", got[3].Model)
	require.Equal(t, int64(3), got[3].Requests)
	require.Equal(t, "claude-opus-4-7", got[4].Model)
	require.Equal(t, int64(3), got[4].Requests)
	require.Equal(t, int64(24), got[4].TotalTokens)
}

func TestLegacyPolicyAllowsClaudeOnly(t *testing.T) {
	require.True(t, legacyPolicyAllowsClaudeOnly([]byte(`{"excluded-models":["gpt-*","chatgpt-*","o1*","o3*","o4*"]}`)))
	require.False(t, legacyPolicyAllowsClaudeOnly([]byte(`{"excluded-models":["claude-*"]}`)))
	require.False(t, legacyPolicyAllowsClaudeOnly([]byte(`{"excluded-models":[]}`)))
	require.False(t, legacyPolicyAllowsClaudeOnly([]byte(`{}`)))
}
