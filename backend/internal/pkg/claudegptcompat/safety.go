package claudegptcompat

import "strings"

func SuppressUnsafeWebSearchToolCallText(text string) bool {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "<tool_call>") ||
		!strings.Contains(lower, "</tool_call>") ||
		!strings.Contains(lower, "web_search") {
		return false
	}
	return looksLikeClaudeCodeConversationMeta(lower)
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
