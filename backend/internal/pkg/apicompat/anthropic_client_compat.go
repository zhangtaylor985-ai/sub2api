package apicompat

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
)

type AnthropicCompatClientKind string

const (
	AnthropicCompatClientUnknown      AnthropicCompatClientKind = "unknown"
	AnthropicCompatClientClaudeCLI    AnthropicCompatClientKind = "claude-cli"
	AnthropicCompatClientClaudeVSCode AnthropicCompatClientKind = "claude_vscode"
	AnthropicCompatClientCodexVSCode  AnthropicCompatClientKind = "codex_exec_vscode"
)

// ResponsesToAnthropicOptions carries client-specific compatibility knobs for
// Anthropic Messages clients consuming OpenAI Responses events.
type ResponsesToAnthropicOptions struct {
	ClientKind             AnthropicCompatClientKind
	WebSearchFallbackQuery string
}

func NormalizeResponsesToAnthropicOptions(opts ResponsesToAnthropicOptions) ResponsesToAnthropicOptions {
	opts.WebSearchFallbackQuery = sanitizeLikelySearchQuery(opts.WebSearchFallbackQuery)
	switch opts.ClientKind {
	case AnthropicCompatClientClaudeCLI, AnthropicCompatClientClaudeVSCode, AnthropicCompatClientCodexVSCode:
	default:
		opts.ClientKind = AnthropicCompatClientUnknown
	}
	return opts
}

// DetectAnthropicCompatClientKind classifies known Claude/Codex clients by
// headers so compatibility output can match their rendering expectations.
func DetectAnthropicCompatClientKind(userAgent, originator string) AnthropicCompatClientKind {
	userAgent = strings.ToLower(strings.TrimSpace(userAgent))
	originator = strings.ToLower(strings.TrimSpace(originator))

	switch {
	case strings.Contains(userAgent, "claude-vscode"):
		return AnthropicCompatClientClaudeVSCode
	case strings.HasPrefix(userAgent, "claude-cli/"):
		return AnthropicCompatClientClaudeCLI
	case strings.Contains(userAgent, "vscode/"), originator == "codex_exec":
		return AnthropicCompatClientCodexVSCode
	default:
		return AnthropicCompatClientUnknown
	}
}

func shouldEmitSyntheticWebSearchTag(kind AnthropicCompatClientKind) bool {
	return kind == AnthropicCompatClientClaudeCLI
}

func shouldEmitVSCodeWebSearchProgress(kind AnthropicCompatClientKind) bool {
	switch kind {
	case AnthropicCompatClientClaudeVSCode, AnthropicCompatClientCodexVSCode:
		return true
	default:
		return false
	}
}

func shouldSurfaceReasoningSummaryAsThinking(kind AnthropicCompatClientKind) bool {
	switch kind {
	case AnthropicCompatClientClaudeCLI, AnthropicCompatClientClaudeVSCode, AnthropicCompatClientCodexVSCode:
		return false
	default:
		return true
	}
}

func buildVSCodeWebSearchProgressThinking(action *WebSearchAction, fallbackQuery string) string {
	if action != nil && strings.EqualFold(strings.TrimSpace(action.Type), "search") {
		query := sanitizeLikelySearchQuery(action.Query)
		if query == "" {
			query = sanitizeLikelySearchQuery(fallbackQuery)
		}
		if query != "" {
			return "Searching the web for: " + query
		}
	}

	if query := sanitizeLikelySearchQuery(fallbackQuery); query != "" {
		return "Searching the web for: " + query
	}
	return "Searching the web."
}

func buildSyntheticWebSearchToolCallText(action *WebSearchAction, fallbackQuery string, completed bool) string {
	prefix := "Searching the web.\n\n"
	if completed {
		prefix = "Searched the web.\n\n"
	}
	args := map[string]any{}
	query := webSearchActionQuery(action)
	if query == "" {
		query = sanitizeLikelySearchQuery(fallbackQuery)
	}
	if query != "" {
		args["query"] = query
		if completed {
			prefix = "Searched: " + query + "\n\n"
		}
	} else if action != nil {
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "search":
		default:
			if action.Type != "" || action.Query != "" {
				args["action"] = map[string]string{
					"type":  action.Type,
					"query": action.Query,
				}
			}
		}
	}

	if len(args) == 0 {
		args["query"] = ""
	}

	payload := map[string]any{
		"name":      "web_search",
		"arguments": args,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return prefix + "<tool_call>\n" + string(body) + "\n</tool_call>"
}

func suppressUnsafeWebSearchToolCallText(text string) bool {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "<tool_call>") ||
		!strings.Contains(lower, "</tool_call>") ||
		!strings.Contains(lower, "web_search") {
		return false
	}
	return looksLikeClaudeCodeConversationMeta(lower)
}

// InferBuiltinWebSearchQuery extracts a likely search label from the latest
// user message when the request declares a built-in web search tool.
func InferBuiltinWebSearchQuery(rawJSON []byte) string {
	if !HasBuiltinWebSearch(rawJSON) {
		return ""
	}

	if text := extractLatestUserText(rawJSON); text != "" {
		if query := normalizeLikelyWebSearchQuery(text); query != "" {
			return query
		}
	}

	return ""
}

// HasBuiltinWebSearch reports whether the Anthropic request declared a server
// web search tool or Claude Code's generic WebSearch function.
func HasBuiltinWebSearch(rawJSON []byte) bool {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.IsArray() {
		return false
	}

	for _, tool := range tools.Array() {
		if isBuiltinWebSearchTool(tool) {
			return true
		}
	}
	return false
}

func isBuiltinWebSearchTool(tool gjson.Result) bool {
	if !tool.Exists() {
		return false
	}
	if strings.EqualFold(tool.Get("type").String(), "web_search_20250305") {
		return true
	}
	if strings.EqualFold(tool.Get("name").String(), "web_search") && tool.Get("type").Exists() {
		return true
	}

	if !strings.EqualFold(strings.TrimSpace(tool.Get("name").String()), "WebSearch") {
		return false
	}

	schema := tool.Get("input_schema")
	if !schema.Exists() {
		return true
	}
	if schema.Get("properties.query").Exists() {
		return true
	}
	if required := schema.Get("required"); required.IsArray() {
		for _, item := range required.Array() {
			if strings.EqualFold(strings.TrimSpace(item.String()), "query") {
				return true
			}
		}
	}
	return false
}

func extractLatestUserText(rawJSON []byte) string {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return ""
	}

	items := messages.Array()
	for i := len(items) - 1; i >= 0; i-- {
		message := items[i]
		if !strings.EqualFold(strings.TrimSpace(message.Get("role").String()), "user") {
			continue
		}

		content := message.Get("content")
		if content.Type == gjson.String {
			return content.String()
		}
		if content.IsArray() {
			parts := content.Array()
			for j := len(parts) - 1; j >= 0; j-- {
				part := parts[j]
				if strings.EqualFold(strings.TrimSpace(part.Get("type").String()), "text") {
					return part.Get("text").String()
				}
			}
		}
	}
	return ""
}

func normalizeLikelyWebSearchQuery(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	if explicit := extractExplicitSearchQuery(text); explicit != "" {
		return explicit
	}

	prefixes := []string{
		"perform a web search for the query:",
		"perform web search for the query:",
		"perform a web search for:",
		"please use web search to look up",
		"use web search to look up",
		"please search the web for",
		"search the web for",
		"web search for the query:",
		"web search for:",
		"search for:",
		"请使用 web search 查询",
		"使用 web search 查询",
		"用 web search 查询",
		"web search 查询",
		"搜索：",
		"搜索:",
		"请搜索：",
		"请搜索:",
		"请查询",
		"查询",
	}

	lower := strings.ToLower(text)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			text = strings.TrimSpace(text[len(prefix):])
			break
		}
	}

	return sanitizeLikelySearchQuery(text)
}

func extractExplicitSearchQuery(text string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "arguments:"):
			return sanitizeLikelySearchQuery(strings.TrimSpace(line[len("arguments:"):]))
		case strings.HasPrefix(lower, "query:"):
			return sanitizeLikelySearchQuery(strings.TrimSpace(line[len("query:"):]))
		}
	}

	return ""
}

func sanitizeLikelySearchQuery(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	parts := make([]string, 0, 2)
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if len(parts) > 0 {
				break
			}
			continue
		}
		if looksLikeSearchQueryNoise(line) {
			break
		}
		parts = append(parts, line)
		if len(parts) >= 2 {
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}

	query := strings.Join(parts, " ")
	query = strings.Join(strings.Fields(query), " ")
	query = strings.Trim(query, " \t\r\n\"'“”‘’")
	query = trimSearchQueryInstructionSuffix(query)
	queryLower := strings.ToLower(query)
	switch {
	case strings.HasPrefix(queryLower, "web search "):
		query = strings.TrimSpace(query[len("web search "):])
	case strings.HasPrefix(queryLower, "search "):
		query = strings.TrimSpace(query[len("search "):])
	}
	if query == "" || len(query) > 220 || looksLikeSearchQueryNoise(query) {
		return ""
	}
	return query
}

func trimSearchQueryInstructionSuffix(query string) string {
	cutMarkers := []string{
		"，并",
		"，然后",
		"，用",
		"，请",
		", and ",
		", then ",
		" and answer",
		" then answer",
		"并用",
		"然后",
	}
	lower := strings.ToLower(query)
	cut := len(query)
	for _, marker := range cutMarkers {
		searchIn := query
		if strings.IndexFunc(marker, func(r rune) bool { return r > 127 }) == -1 {
			searchIn = lower
		}
		if idx := strings.Index(searchIn, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	return strings.TrimSpace(query[:cut])
}

func looksLikeSearchQueryNoise(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}

	lower := strings.ToLower(trimmed)
	if looksLikeClaudeCodeConversationMeta(lower) {
		return true
	}
	noisePrefixes := []string{
		"<system-reminder>",
		"arguments:",
		"query:",
		"import ",
		"from ",
		"def ",
		"class ",
		"func ",
		"package ",
		"python3 ",
		"curl ",
		"go test ",
		"sed -n ",
		"rg -n ",
		"cases =",
		"results =",
		"with open(",
		"for prompt in",
		"p = subprocess",
		"event:",
		"data:",
		"```",
		"read the output of the terminal command",
		"then fix the error",
		"then rerun the command",
		"repeat this debugging process",
		"use context7",
		"use brave-search",
		"读终端命令的输出",
		"然后修复该错误",
		"接着在终端中重新运行该命令",
		"如果再次出现错误",
		"使用 context7",
		"使用 brave-search",
	}
	for _, prefix := range noisePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return strings.Contains(lower, "with open(") ||
		strings.Contains(lower, "subprocess.run(") ||
		strings.Contains(lower, "json.dump(") ||
		strings.Contains(lower, "json.load(") ||
		strings.Contains(lower, "panic:") ||
		strings.Contains(lower, "traceback ") ||
		strings.Contains(lower, "stack trace")
}

func looksLikeClaudeCodeConversationMeta(lower string) bool {
	metaMarkers := []string{
		"this session is being continued from a previous conversation",
		"the summary below covers the earlier portion of the conversation",
		"previous conversation that ran out of context",
		"we need continue from summary",
		"need continue from summary",
		"continuing from a previous conversation",
		"conversation that ran out of context",
		"summary below covers the earlier portion",
	}
	for _, marker := range metaMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
