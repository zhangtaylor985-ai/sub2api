package claudegptcompat

import (
	"strings"

	"github.com/tidwall/gjson"
)

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

	return SanitizeLikelySearchQuery(text)
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
			return SanitizeLikelySearchQuery(strings.TrimSpace(line[len("arguments:"):]))
		case strings.HasPrefix(lower, "query:"):
			return SanitizeLikelySearchQuery(strings.TrimSpace(line[len("query:"):]))
		}
	}

	return ""
}

func SanitizeLikelySearchQuery(text string) string {
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
