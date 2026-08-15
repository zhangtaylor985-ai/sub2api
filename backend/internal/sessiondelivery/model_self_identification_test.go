package sessiondelivery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func selfClaimRecord(t *testing.T, responseText string, historyText string) (map[string]json.RawMessage, map[string]json.RawMessage) {
	t.Helper()
	messages := []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "who are you"}}},
	}
	if historyText != "" {
		messages = append(messages, map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": historyText}},
		})
	}
	request := map[string]json.RawMessage{"messages": mustJSON(messages)}
	response := map[string]json.RawMessage{
		"content": mustJSON([]any{map[string]any{"type": "text", "text": responseText}}),
	}
	return request, response
}

// The exact sentence measured in the corpus.
func TestCountForeignModelSelfClaimsDetectsChineseDisclosure(t *testing.T) {
	request, response := selfClaimRecord(t,
		"我是 **GPT-5 系列的 Codex 编程智能体**。当前系统未向我暴露更精确的模型版本标识。", "")
	require.Equal(t, int64(1), countForeignModelSelfClaims(request, response))
}

func TestCountForeignModelSelfClaimsDetectsDisclosureEchoedInHistory(t *testing.T) {
	request, response := selfClaimRecord(t, "Sure, here is the patch.",
		"我是 GPT-5 系列的 Codex 编程智能体。")
	require.Equal(t, int64(1), countForeignModelSelfClaims(request, response))
}

func TestCountForeignModelSelfClaimsDetectsEnglishDisclosures(t *testing.T) {
	for _, text := range []string{
		"I'm ChatGPT, a large language model.",
		"I am a GPT-5 class model.",
		"This assistant was trained by OpenAI.",
		"由 OpenAI 训练的模型。",
		"作为 Codex 编程智能体，我建议先跑测试。",
	} {
		request, response := selfClaimRecord(t, text, "")
		require.Equal(t, int64(1), countForeignModelSelfClaims(request, response), "text=%q", text)
	}
}

// Discussing other models is ordinary subject matter, not a disclosure.
func TestCountForeignModelSelfClaimsIgnoresThirdPartyModelDiscussion(t *testing.T) {
	for _, text := range []string{
		"Mem0 默认依赖 LLM，README 中提到默认使用 `gpt-5-mini`。",
		"支持与 LangGraph、CrewAI、ChatGPT Memory Demo 等集成。",
		"The group maps claude-opus-5 to gpt-5.5 for dispatch.",
		"我是来帮你排查这个 OpenAI 兼容层问题的。",
		"我为你对比了 gpt-5.6 和 claude-opus-5 的价格。",
	} {
		request, response := selfClaimRecord(t, text, "")
		require.Zero(t, countForeignModelSelfClaims(request, response), "text=%q", text)
	}
}

func TestCountForeignModelSelfClaimsScansThinkingBlocks(t *testing.T) {
	request := map[string]json.RawMessage{"messages": mustJSON([]any{})}
	response := map[string]json.RawMessage{
		"content": mustJSON([]any{
			map[string]any{"type": "thinking", "thinking": "我是 GPT-5 系列的 Codex，先看用户要什么。", "signature": "sig"},
			map[string]any{"type": "text", "text": "好的。"},
		}),
	}
	require.Equal(t, int64(1), countForeignModelSelfClaims(request, response))
}

func TestCountForeignModelSelfClaimsIsZeroForCleanRecord(t *testing.T) {
	request, response := selfClaimRecord(t, "I'm Claude Code. Let me read the file first.", "")
	require.Zero(t, countForeignModelSelfClaims(request, response))
}
