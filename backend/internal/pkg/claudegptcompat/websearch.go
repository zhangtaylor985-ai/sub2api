package claudegptcompat

import (
	"encoding/json"
	"net/url"
	"strings"
)

type WebSearchAction struct {
	Type    string
	Query   string
	Queries []string
	URL     string
	Sources []WebSearchSource
}

type WebSearchSource struct {
	Type    string
	URL     string
	Title   string
	PageAge string
}

type WebSearchResult struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	PageAge string `json:"page_age,omitempty"`
}

func BuildVSCodeWebSearchProgressThinking(action *WebSearchAction, fallbackQuery string) string {
	if action != nil && strings.EqualFold(strings.TrimSpace(action.Type), "search") {
		query := SanitizeLikelySearchQuery(action.Query)
		if query == "" {
			query = SanitizeLikelySearchQuery(fallbackQuery)
		}
		if query != "" {
			return "Searching the web for: " + query
		}
	}

	if query := SanitizeLikelySearchQuery(fallbackQuery); query != "" {
		return "Searching the web for: " + query
	}
	return "Searching the web."
}

func BuildSyntheticWebSearchToolCallText(action *WebSearchAction, fallbackQuery string, completed bool) string {
	prefix := "Searching the web.\n\n"
	if completed {
		prefix = "Searched the web.\n\n"
	}

	args := map[string]any{}
	query := WebSearchActionQuery(action)
	if query == "" {
		query = SanitizeLikelySearchQuery(fallbackQuery)
	}
	if query != "" {
		args["query"] = query
		if completed {
			prefix = "Searched: " + query + "\n\n"
		}
		if queries := SanitizedWebSearchQueries(action); len(queries) > 0 {
			args["queries"] = queries
		}
		if u := WebSearchActionURL(action); u != "" {
			args["url"] = u
		}
	} else if action != nil {
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "search":
			if queries := SanitizedWebSearchQueries(action); len(queries) > 0 {
				args["queries"] = queries
			}
			if u := WebSearchActionURL(action); u != "" {
				args["url"] = u
			}
		case "open_page":
			if u := WebSearchActionURL(action); u != "" {
				args["url"] = u
			}
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

func WebSearchActionQuery(action *WebSearchAction) string {
	if action == nil {
		return ""
	}
	return SanitizeLikelySearchQuery(action.Query)
}

func WebSearchActionURL(action *WebSearchAction) string {
	if action == nil {
		return ""
	}
	return NormalizeWebSearchURL(action.URL)
}

func WebSearchQueryWithFallback(action *WebSearchAction, fallbackQuery string) string {
	if query := WebSearchActionQuery(action); query != "" {
		return query
	}
	return SanitizeLikelySearchQuery(fallbackQuery)
}

func SanitizedWebSearchQueries(action *WebSearchAction) []string {
	if action == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(action.Queries))
	queries := make([]string, 0, len(action.Queries))
	for _, raw := range action.Queries {
		query := SanitizeLikelySearchQuery(raw)
		if query == "" {
			continue
		}
		if _, exists := seen[query]; exists {
			continue
		}
		seen[query] = struct{}{}
		queries = append(queries, query)
	}
	return queries
}

func WebSearchToolInputJSON(action *WebSearchAction, query string) json.RawMessage {
	input := map[string]any{"query": query}
	if queries := SanitizedWebSearchQueries(action); len(queries) > 0 {
		input["queries"] = queries
	}
	if u := WebSearchActionURL(action); u != "" {
		input["url"] = u
	}
	raw, _ := json.Marshal(input)
	return raw
}

func WebSearchResultsFromAction(action *WebSearchAction) []WebSearchResult {
	if action == nil {
		return []WebSearchResult{}
	}

	results := make([]WebSearchResult, 0, len(action.Sources)+1)
	seen := map[string]struct{}{}
	appendResult := func(rawURL, title, pageAge string) {
		u := NormalizeWebSearchURL(rawURL)
		if u == "" {
			return
		}
		if _, exists := seen[u]; exists {
			return
		}
		seen[u] = struct{}{}
		results = append(results, WebSearchResult{
			Type:    "web_search_result",
			URL:     u,
			Title:   strings.TrimSpace(title),
			PageAge: strings.TrimSpace(pageAge),
		})
	}

	for _, source := range action.Sources {
		appendResult(source.URL, source.Title, source.PageAge)
	}
	appendResult(action.URL, "", "")

	if len(results) == 0 {
		return []WebSearchResult{}
	}
	return results
}

func NormalizeWebSearchURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}

func CitationTextSlice(text string, startIndex, endIndex int) string {
	runes := []rune(text)
	if startIndex < 0 || endIndex <= startIndex || startIndex >= len(runes) {
		return ""
	}
	if endIndex > len(runes) {
		endIndex = len(runes)
	}
	return string(runes[startIndex:endIndex])
}
