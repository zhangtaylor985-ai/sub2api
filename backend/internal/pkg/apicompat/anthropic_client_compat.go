package apicompat

import "github.com/Wei-Shaw/sub2api/internal/pkg/claudegptcompat"

type AnthropicCompatClientKind = claudegptcompat.ClientKind

const (
	AnthropicCompatClientUnknown      = claudegptcompat.ClientUnknown
	AnthropicCompatClientClaudeCLI    = claudegptcompat.ClientClaudeCLI
	AnthropicCompatClientClaudeVSCode = claudegptcompat.ClientClaudeVSCode
	AnthropicCompatClientCodexVSCode  = claudegptcompat.ClientCodexVSCode
)

// ResponsesToAnthropicOptions carries client-specific compatibility knobs for
// Anthropic Messages clients consuming OpenAI Responses events.
type ResponsesToAnthropicOptions struct {
	ClientKind             AnthropicCompatClientKind
	WebSearchFallbackQuery string
}

func NormalizeResponsesToAnthropicOptions(opts ResponsesToAnthropicOptions) ResponsesToAnthropicOptions {
	opts.WebSearchFallbackQuery = claudegptcompat.SanitizeLikelySearchQuery(opts.WebSearchFallbackQuery)
	opts.ClientKind = claudegptcompat.NormalizeClientKind(opts.ClientKind)
	return opts
}

// DetectAnthropicCompatClientKind classifies known Claude/Codex clients by
// headers so compatibility output can match their rendering expectations.
func DetectAnthropicCompatClientKind(userAgent, originator string) AnthropicCompatClientKind {
	return claudegptcompat.DetectClientKind(userAgent, originator)
}

func shouldEmitSyntheticWebSearchTag(kind AnthropicCompatClientKind) bool {
	return claudegptcompat.ShouldEmitSyntheticWebSearchTag(kind)
}

func shouldEmitVSCodeWebSearchProgress(kind AnthropicCompatClientKind) bool {
	return claudegptcompat.ShouldEmitVSCodeWebSearchProgress(kind)
}

func shouldSurfaceReasoningSummaryAsThinking(kind AnthropicCompatClientKind) bool {
	return claudegptcompat.ShouldSurfaceReasoningSummaryAsThinking(kind)
}

func buildVSCodeWebSearchProgressThinking(action *WebSearchAction, fallbackQuery string) string {
	return claudegptcompat.BuildVSCodeWebSearchProgressThinking(toClaudeGPTWebSearchAction(action), fallbackQuery)
}

func buildSyntheticWebSearchToolCallText(action *WebSearchAction, fallbackQuery string, completed bool) string {
	return claudegptcompat.BuildSyntheticWebSearchToolCallText(toClaudeGPTWebSearchAction(action), fallbackQuery, completed)
}

func suppressUnsafeWebSearchToolCallText(text string) bool {
	return claudegptcompat.SuppressUnsafeWebSearchToolCallText(text)
}

// InferBuiltinWebSearchQuery extracts a likely search label from the latest
// user message when the request declares a built-in web search tool.
func InferBuiltinWebSearchQuery(rawJSON []byte) string {
	return claudegptcompat.InferBuiltinWebSearchQuery(rawJSON)
}

// HasBuiltinWebSearch reports whether the Anthropic request declared a server
// web search tool or Claude Code's generic WebSearch function.
func HasBuiltinWebSearch(rawJSON []byte) bool {
	return claudegptcompat.HasBuiltinWebSearch(rawJSON)
}

func sanitizeLikelySearchQuery(text string) string {
	return claudegptcompat.SanitizeLikelySearchQuery(text)
}

func toClaudeGPTWebSearchAction(action *WebSearchAction) *claudegptcompat.WebSearchAction {
	if action == nil {
		return nil
	}
	out := &claudegptcompat.WebSearchAction{
		Type:    action.Type,
		Query:   action.Query,
		Queries: append([]string(nil), action.Queries...),
		URL:     action.URL,
	}
	if len(action.Sources) > 0 {
		out.Sources = make([]claudegptcompat.WebSearchSource, 0, len(action.Sources))
		for _, source := range action.Sources {
			out.Sources = append(out.Sources, claudegptcompat.WebSearchSource{
				Type:    source.Type,
				URL:     source.URL,
				Title:   source.Title,
				PageAge: source.PageAge,
			})
		}
	}
	return out
}
