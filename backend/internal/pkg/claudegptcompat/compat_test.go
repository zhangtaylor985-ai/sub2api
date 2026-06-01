package claudegptcompat

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectClientKind(t *testing.T) {
	assert.Equal(t, ClientClaudeCLI, DetectClientKind("claude-cli/2.1.133 (external, sdk-cli)", ""))
	assert.Equal(t, ClientClaudeVSCode, DetectClientKind("Claude-VSCode/1.0", ""))
	assert.Equal(t, ClientCodexVSCode, DetectClientKind("Mozilla vscode/1.112.0", ""))
	assert.Equal(t, ClientCodexVSCode, DetectClientKind("", "codex_exec"))
	assert.Equal(t, ClientUnknown, DetectClientKind("curl/8.0", ""))
}

func TestInferBuiltinWebSearchQueryExtractsAndSanitizes(t *testing.T) {
	raw := []byte(`{
		"messages":[{"role":"user","content":"请使用 web search 查询 OpenAI official website homepage title，并用一句中文回答。"}],
		"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]
	}`)

	assert.Equal(t, "OpenAI official website homepage title", InferBuiltinWebSearchQuery(raw))
}

func TestInferBuiltinWebSearchQueryIgnoresContinuationSummary(t *testing.T) {
	raw := []byte(`{
		"messages":[{"role":"user","content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion."}],
		"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]
	}`)

	assert.Empty(t, InferBuiltinWebSearchQuery(raw))
}

func TestWebSearchResultsFromActionPreservesSourcesAndDedupes(t *testing.T) {
	results := WebSearchResultsFromAction(&WebSearchAction{
		Type: "search",
		Sources: []WebSearchSource{
			{URL: "https://apnews.com/article/example", Title: "AP News Example", PageAge: "May 29, 2026"},
			{URL: "https://apnews.com/article/example", Title: "Duplicate"},
			{URL: "javascript:alert(1)", Title: "Bad"},
		},
		URL: "https://www.reuters.com/world/example",
	})

	require.Len(t, results, 2)
	assert.Equal(t, "web_search_result", results[0].Type)
	assert.Equal(t, "https://apnews.com/article/example", results[0].URL)
	assert.Equal(t, "AP News Example", results[0].Title)
	assert.Equal(t, "https://www.reuters.com/world/example", results[1].URL)
}

func TestBuildSyntheticWebSearchToolCallTextIncludesQueriesAndURL(t *testing.T) {
	text := BuildSyntheticWebSearchToolCallText(&WebSearchAction{
		Type:    "open_page",
		URL:     "https://www.reuters.com/world/example",
		Queries: []string{"latest Reuters AI news"},
	}, "", true)

	assert.True(t, strings.HasPrefix(text, "Searched the web."))
	assert.Contains(t, text, `"url":"https://www.reuters.com/world/example"`)
	assert.NotContains(t, text, "javascript:")
}
