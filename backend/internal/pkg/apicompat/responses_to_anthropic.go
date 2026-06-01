package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claudegptcompat"
)

// ---------------------------------------------------------------------------
// Non-streaming: ResponsesResponse → AnthropicResponse
// ---------------------------------------------------------------------------

// ResponsesToAnthropic converts a Responses API response directly into an
// Anthropic Messages response. Reasoning output items are mapped to thinking
// blocks; function_call items become tool_use blocks.
func ResponsesToAnthropic(resp *ResponsesResponse, model string) *AnthropicResponse {
	return ResponsesToAnthropicWithOptions(resp, model, ResponsesToAnthropicOptions{})
}

func ResponsesToAnthropicWithOptions(resp *ResponsesResponse, model string, opts ResponsesToAnthropicOptions) *AnthropicResponse {
	opts = NormalizeResponsesToAnthropicOptions(opts)
	out := &AnthropicResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: model,
	}

	var blocks []AnthropicContentBlock

	for _, item := range resp.Output {
		switch item.Type {
		case "reasoning":
			if !shouldSurfaceReasoningSummaryAsThinking(opts.ClientKind) {
				continue
			}
			summaryText := ""
			for _, s := range item.Summary {
				if s.Type == "summary_text" && s.Text != "" {
					summaryText += s.Text
				}
			}
			if summaryText != "" {
				blocks = append(blocks, AnthropicContentBlock{
					Type:     "thinking",
					Thinking: summaryText,
				})
			}
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					if suppressUnsafeWebSearchToolCallText(part.Text) {
						continue
					}
					blocks = append(blocks, AnthropicContentBlock{
						Type:      "text",
						Text:      part.Text,
						Citations: responsesAnnotationsToAnthropicCitations(part.Annotations, part.Text),
					})
				}
			}
		case "function_call":
			blocks = append(blocks, AnthropicContentBlock{
				Type:  "tool_use",
				ID:    fromResponsesCallID(item.CallID),
				Name:  item.Name,
				Input: sanitizeAnthropicToolUseInput(item.Name, item.Arguments),
			})
		case "web_search_call":
			toolUseID := "srvtoolu_" + item.ID
			query := webSearchQueryWithFallback(item.Action, opts.WebSearchFallbackQuery)
			inputJSON := webSearchToolInputJSON(item.Action, query)
			blocks = append(blocks, AnthropicContentBlock{
				Type:  "server_tool_use",
				ID:    toolUseID,
				Name:  "web_search",
				Input: inputJSON,
			})
			blocks = append(blocks, AnthropicContentBlock{
				Type:      "web_search_tool_result",
				ToolUseID: toolUseID,
				Content:   webSearchToolResultContent(item.Action),
			})
			if shouldEmitSyntheticWebSearchTag(opts.ClientKind) {
				if syntheticText := buildSyntheticWebSearchToolCallText(item.Action, opts.WebSearchFallbackQuery, true); syntheticText != "" {
					blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: syntheticText})
				}
			}
		}
	}

	if len(blocks) == 0 {
		blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: ""})
	}
	out.Content = blocks

	out.StopReason = responsesStatusToAnthropicStopReason(resp.Status, resp.IncompleteDetails, blocks)

	if resp.Usage != nil {
		out.Usage = anthropicUsageFromResponsesUsage(resp.Usage)
	}

	return out
}

func anthropicUsageFromResponsesUsage(usage *ResponsesUsage) AnthropicUsage {
	if usage == nil {
		return AnthropicUsage{}
	}

	cachedTokens := 0
	if usage.InputTokensDetails != nil {
		cachedTokens = usage.InputTokensDetails.CachedTokens
	}

	inputTokens := usage.InputTokens - cachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}

	return AnthropicUsage{
		InputTokens:          inputTokens,
		OutputTokens:         usage.OutputTokens,
		CacheReadInputTokens: cachedTokens,
	}
}

func responsesStatusToAnthropicStopReason(status string, details *ResponsesIncompleteDetails, blocks []AnthropicContentBlock) string {
	switch status {
	case "incomplete":
		if details != nil && details.Reason == "max_output_tokens" {
			return "max_tokens"
		}
		return "end_turn"
	case "completed":
		if containsAnthropicToolUseBlock(blocks) {
			return "tool_use"
		}
		return "end_turn"
	default:
		return "end_turn"
	}
}

func containsAnthropicToolUseBlock(blocks []AnthropicContentBlock) bool {
	for _, block := range blocks {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

func sanitizeAnthropicToolUseInput(name string, raw string) json.RawMessage {
	if name != "Read" || raw == "" {
		return json.RawMessage(raw)
	}

	var input map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return json.RawMessage(raw)
	}

	if pages, ok := input["pages"]; !ok || string(pages) != `""` {
		return json.RawMessage(raw)
	}

	delete(input, "pages")
	sanitized, err := json.Marshal(input)
	if err != nil {
		return json.RawMessage(raw)
	}
	return sanitized
}

// ---------------------------------------------------------------------------
// Streaming: ResponsesStreamEvent → []AnthropicStreamEvent (stateful converter)
// ---------------------------------------------------------------------------

// ResponsesEventToAnthropicState tracks state for converting a sequence of
// Responses SSE events directly into Anthropic SSE events.
type ResponsesEventToAnthropicState struct {
	MessageStartSent bool
	MessageStopSent  bool

	ContentBlockIndex   int
	ContentBlockOpen    bool
	CurrentBlockType    string // "text" | "thinking" | "tool_use"
	CurrentToolName     string
	CurrentToolArgs     string
	CurrentToolHadDelta bool
	HasToolCall         bool

	// OutputIndexToBlockIdx maps Responses output_index → Anthropic content block index.
	OutputIndexToBlockIdx map[int]int

	InputTokens          int
	OutputTokens         int
	CacheReadInputTokens int

	ResponseID string
	Model      string
	Created    int64

	CompatOptions                   ResponsesToAnthropicOptions
	LastWebSearchQuery              string
	EmittedSyntheticWebSearchStarts map[string]struct{}
	EmittedSyntheticWebSearchDones  map[string]struct{}
	TextOutputIndexes               map[int]struct{}
}

// NewResponsesEventToAnthropicState returns an initialised stream state.
func NewResponsesEventToAnthropicState() *ResponsesEventToAnthropicState {
	return NewResponsesEventToAnthropicStateWithOptions(ResponsesToAnthropicOptions{})
}

func NewResponsesEventToAnthropicStateWithOptions(opts ResponsesToAnthropicOptions) *ResponsesEventToAnthropicState {
	opts = NormalizeResponsesToAnthropicOptions(opts)
	return &ResponsesEventToAnthropicState{
		OutputIndexToBlockIdx:           make(map[int]int),
		Created:                         time.Now().Unix(),
		CompatOptions:                   opts,
		LastWebSearchQuery:              opts.WebSearchFallbackQuery,
		EmittedSyntheticWebSearchStarts: make(map[string]struct{}),
		EmittedSyntheticWebSearchDones:  make(map[string]struct{}),
		TextOutputIndexes:               make(map[int]struct{}),
	}
}

// ResponsesEventToAnthropicEvents converts a single Responses SSE event into
// zero or more Anthropic SSE events, updating state as it goes.
func ResponsesEventToAnthropicEvents(
	evt *ResponsesStreamEvent,
	state *ResponsesEventToAnthropicState,
) []AnthropicStreamEvent {
	switch evt.Type {
	case "response.created":
		return resToAnthHandleCreated(evt, state)
	case "response.output_item.added":
		return resToAnthHandleOutputItemAdded(evt, state)
	case "response.output_text.delta":
		return resToAnthHandleTextDelta(evt, state)
	case "response.output_text.annotation.added":
		return resToAnthHandleTextAnnotationAdded(evt, state)
	case "response.output_text.done":
		return resToAnthHandleBlockDone(state)
	case "response.function_call_arguments.delta":
		return resToAnthHandleFuncArgsDelta(evt, state)
	case "response.function_call_arguments.done":
		return resToAnthHandleFuncArgsDone(evt, state)
	case "response.output_item.done":
		return resToAnthHandleOutputItemDone(evt, state)
	case "response.reasoning_summary_text.delta":
		if !shouldSurfaceReasoningSummaryAsThinking(state.CompatOptions.ClientKind) {
			return nil
		}
		return resToAnthHandleReasoningDelta(evt, state)
	case "response.reasoning_summary_text.done":
		if !shouldSurfaceReasoningSummaryAsThinking(state.CompatOptions.ClientKind) {
			return nil
		}
		return resToAnthHandleBlockDone(state)
	// response.done 是 Realtime/WS 与项目透传路径使用的终止别名；
	// 普通 Responses HTTP SSE 的公开终止事件仍以 response.completed 为主。
	case "response.completed", "response.done", "response.incomplete", "response.failed":
		return resToAnthHandleCompleted(evt, state)
	default:
		return nil
	}
}

// FinalizeResponsesAnthropicStream emits synthetic termination events if the
// stream ended without a proper completion event.
func FinalizeResponsesAnthropicStream(state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if !state.MessageStartSent || state.MessageStopSent {
		return nil
	}

	var events []AnthropicStreamEvent
	events = append(events, closeCurrentBlock(state)...)

	stopReason := "end_turn"
	if state.HasToolCall {
		stopReason = "tool_use"
	}

	events = append(events,
		AnthropicStreamEvent{
			Type: "message_delta",
			Delta: &AnthropicDelta{
				StopReason: stopReason,
			},
			Usage: &AnthropicUsage{
				InputTokens:          state.InputTokens,
				OutputTokens:         state.OutputTokens,
				CacheReadInputTokens: state.CacheReadInputTokens,
			},
		},
		AnthropicStreamEvent{Type: "message_stop"},
	)
	state.MessageStopSent = true
	return events
}

// ResponsesAnthropicEventToSSE formats an AnthropicStreamEvent as an SSE line pair.
func ResponsesAnthropicEventToSSE(evt AnthropicStreamEvent) (string, error) {
	if evt.Type == "content_block_delta" && evt.Delta != nil && suppressUnsafeWebSearchToolCallText(evt.Delta.Text) {
		return "", nil
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", evt.Type, data), nil
}

// --- internal handlers ---

func resToAnthHandleCreated(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if evt.Response != nil {
		state.ResponseID = evt.Response.ID
		// Only use upstream model if no override was set (e.g. originalModel)
		if state.Model == "" {
			state.Model = evt.Response.Model
		}
	}

	if state.MessageStartSent {
		return nil
	}
	state.MessageStartSent = true

	return []AnthropicStreamEvent{{
		Type: "message_start",
		Message: &AnthropicResponse{
			ID:      state.ResponseID,
			Type:    "message",
			Role:    "assistant",
			Content: []AnthropicContentBlock{},
			Model:   state.Model,
			Usage: AnthropicUsage{
				InputTokens:  0,
				OutputTokens: 0,
			},
		},
	}}
}

func resToAnthHandleOutputItemAdded(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if evt.Item == nil {
		return nil
	}

	switch evt.Item.Type {
	case "function_call":
		var events []AnthropicStreamEvent
		events = append(events, closeCurrentBlock(state)...)

		idx := state.ContentBlockIndex
		state.OutputIndexToBlockIdx[evt.OutputIndex] = idx
		state.ContentBlockOpen = true
		state.CurrentBlockType = "tool_use"
		state.CurrentToolName = evt.Item.Name
		state.CurrentToolArgs = ""
		state.CurrentToolHadDelta = false
		state.HasToolCall = true

		events = append(events, AnthropicStreamEvent{
			Type:  "content_block_start",
			Index: &idx,
			ContentBlock: &AnthropicContentBlock{
				Type:  "tool_use",
				ID:    fromResponsesCallID(evt.Item.CallID),
				Name:  evt.Item.Name,
				Input: json.RawMessage("{}"),
			},
		})
		return events

	case "reasoning":
		if !shouldSurfaceReasoningSummaryAsThinking(state.CompatOptions.ClientKind) {
			return nil
		}
		var events []AnthropicStreamEvent
		events = append(events, closeCurrentBlock(state)...)

		idx := state.ContentBlockIndex
		state.OutputIndexToBlockIdx[evt.OutputIndex] = idx
		state.ContentBlockOpen = true
		state.CurrentBlockType = "thinking"

		events = append(events, AnthropicStreamEvent{
			Type:  "content_block_start",
			Index: &idx,
			ContentBlock: &AnthropicContentBlock{
				Type:     "thinking",
				Thinking: "",
			},
		})
		return events

	case "message":
		return nil
	case "web_search_call":
		return resToAnthHandleWebSearchAdded(evt, state)
	}

	return nil
}

func resToAnthHandleWebSearchAdded(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if evt == nil || evt.Item == nil {
		return nil
	}
	itemID := strings.TrimSpace(evt.Item.ID)
	if query := webSearchActionQuery(evt.Item.Action); query != "" {
		state.LastWebSearchQuery = query
	}

	switch {
	case shouldEmitSyntheticWebSearchTag(state.CompatOptions.ClientKind):
		if itemID != "" {
			if _, exists := state.EmittedSyntheticWebSearchStarts[itemID]; exists {
				return nil
			}
		}
		text := buildSyntheticWebSearchToolCallText(evt.Item.Action, state.LastWebSearchQuery, false)
		if text == "" {
			return nil
		}
		if itemID != "" {
			state.EmittedSyntheticWebSearchStarts[itemID] = struct{}{}
		}
		return emitStandaloneTextBlock(state, text)

	case shouldEmitVSCodeWebSearchProgress(state.CompatOptions.ClientKind):
		if itemID != "" {
			if _, exists := state.EmittedSyntheticWebSearchStarts[itemID]; exists {
				return nil
			}
		}
		thinking := buildVSCodeWebSearchProgressThinking(evt.Item.Action, state.LastWebSearchQuery)
		if thinking == "" {
			return nil
		}
		if itemID != "" {
			state.EmittedSyntheticWebSearchStarts[itemID] = struct{}{}
		}
		return emitStandaloneThinkingBlock(state, thinking)
	default:
		return nil
	}
}

func resToAnthHandleTextDelta(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if evt.Delta == "" {
		return nil
	}
	if suppressUnsafeWebSearchToolCallText(evt.Delta) {
		return nil
	}
	state.TextOutputIndexes[evt.OutputIndex] = struct{}{}

	var events []AnthropicStreamEvent

	if !state.ContentBlockOpen || state.CurrentBlockType != "text" {
		events = append(events, closeCurrentBlock(state)...)

		idx := state.ContentBlockIndex
		state.ContentBlockOpen = true
		state.CurrentBlockType = "text"
		state.OutputIndexToBlockIdx[evt.OutputIndex] = idx

		events = append(events, AnthropicStreamEvent{
			Type:  "content_block_start",
			Index: &idx,
			ContentBlock: &AnthropicContentBlock{
				Type: "text",
				Text: "",
			},
		})
	}

	idx := state.ContentBlockIndex
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: &AnthropicDelta{
			Type: "text_delta",
			Text: evt.Delta,
		},
	})
	return events
}

func resToAnthHandleTextAnnotationAdded(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if evt == nil || evt.Annotation == nil {
		return nil
	}
	citation := responsesAnnotationToAnthropicCitation(*evt.Annotation, "")
	if citation == nil {
		return nil
	}

	blockIdx, ok := state.OutputIndexToBlockIdx[evt.OutputIndex]
	if !ok {
		if !state.ContentBlockOpen || state.CurrentBlockType != "text" {
			return nil
		}
		blockIdx = state.ContentBlockIndex
	}

	return []AnthropicStreamEvent{{
		Type:  "content_block_delta",
		Index: &blockIdx,
		Delta: &AnthropicDelta{
			Type:     "citations_delta",
			Citation: citation,
		},
	}}
}

func resToAnthHandleFuncArgsDelta(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if evt.Delta == "" {
		return nil
	}

	if state.CurrentBlockType == "tool_use" && state.CurrentToolName == "Read" {
		state.CurrentToolArgs += evt.Delta
		return nil
	}
	if state.CurrentBlockType == "tool_use" {
		state.CurrentToolHadDelta = true
	}

	blockIdx, ok := state.OutputIndexToBlockIdx[evt.OutputIndex]
	if !ok {
		return nil
	}

	return []AnthropicStreamEvent{{
		Type:  "content_block_delta",
		Index: &blockIdx,
		Delta: &AnthropicDelta{
			Type:        "input_json_delta",
			PartialJSON: evt.Delta,
		},
	}}
}

func resToAnthHandleFuncArgsDone(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if state.CurrentBlockType != "tool_use" {
		return resToAnthHandleBlockDone(state)
	}

	raw := evt.Arguments
	if raw == "" {
		raw = state.CurrentToolArgs
	}
	if raw == "" || state.CurrentToolHadDelta {
		return closeCurrentBlock(state)
	}
	if state.CurrentToolName == "Read" {
		sanitized := sanitizeAnthropicToolUseInput(state.CurrentToolName, raw)
		if len(sanitized) == 0 {
			return closeCurrentBlock(state)
		}
		raw = string(sanitized)
	}

	idx := state.ContentBlockIndex
	events := []AnthropicStreamEvent{{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: &AnthropicDelta{
			Type:        "input_json_delta",
			PartialJSON: raw,
		},
	}}
	events = append(events, closeCurrentBlock(state)...)
	return events
}

func resToAnthHandleReasoningDelta(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if evt.Delta == "" {
		return nil
	}

	blockIdx, ok := state.OutputIndexToBlockIdx[evt.OutputIndex]
	if !ok {
		return nil
	}

	return []AnthropicStreamEvent{{
		Type:  "content_block_delta",
		Index: &blockIdx,
		Delta: &AnthropicDelta{
			Type:     "thinking_delta",
			Thinking: evt.Delta,
		},
	}}
}

func resToAnthHandleBlockDone(state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if !state.ContentBlockOpen {
		return nil
	}
	return closeCurrentBlock(state)
}

func resToAnthHandleOutputItemDone(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if evt.Item == nil {
		return nil
	}

	// Handle web_search_call → synthesize server_tool_use + web_search_tool_result blocks.
	if evt.Item.Type == "web_search_call" && evt.Item.Status == "completed" {
		return resToAnthHandleWebSearchDone(evt, state)
	}
	if evt.Item.Type == "message" {
		if _, sawDelta := state.TextOutputIndexes[evt.OutputIndex]; !sawDelta {
			if text := responsesOutputMessageText(evt.Item); text != "" {
				state.TextOutputIndexes[evt.OutputIndex] = struct{}{}
				return emitStandaloneTextBlock(state, text)
			}
		}
	}

	if state.ContentBlockOpen {
		return closeCurrentBlock(state)
	}
	return nil
}

func responsesOutputMessageText(item *ResponsesOutput) string {
	if item == nil || item.Type != "message" {
		return ""
	}
	var parts []string
	for _, part := range item.Content {
		if part.Type == "output_text" && strings.TrimSpace(part.Text) != "" {
			if suppressUnsafeWebSearchToolCallText(part.Text) {
				continue
			}
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "")
}

// resToAnthHandleWebSearchDone converts an OpenAI web_search_call output item
// into Anthropic server_tool_use + web_search_tool_result content block pairs.
// This allows Claude Code to count the searches performed.
func resToAnthHandleWebSearchDone(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	var events []AnthropicStreamEvent
	events = append(events, closeCurrentBlock(state)...)

	toolUseID := "srvtoolu_" + evt.Item.ID
	query := webSearchQueryWithFallback(evt.Item.Action, state.LastWebSearchQuery)
	if query != "" {
		state.LastWebSearchQuery = query
	}
	inputJSON := webSearchToolInputJSON(evt.Item.Action, query)

	// Emit server_tool_use block (start + stop).
	idx1 := state.ContentBlockIndex
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_start",
		Index: &idx1,
		ContentBlock: &AnthropicContentBlock{
			Type:  "server_tool_use",
			ID:    toolUseID,
			Name:  "web_search",
			Input: inputJSON,
		},
	})
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_stop",
		Index: &idx1,
	})
	state.ContentBlockIndex++

	// Emit web_search_tool_result block (start + stop).
	idx2 := state.ContentBlockIndex
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_start",
		Index: &idx2,
		ContentBlock: &AnthropicContentBlock{
			Type:      "web_search_tool_result",
			ToolUseID: toolUseID,
			Content:   webSearchToolResultContent(evt.Item.Action),
		},
	})
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_stop",
		Index: &idx2,
	})
	state.ContentBlockIndex++

	if shouldEmitSyntheticWebSearchTag(state.CompatOptions.ClientKind) {
		itemID := strings.TrimSpace(evt.Item.ID)
		if itemID != "" {
			if _, exists := state.EmittedSyntheticWebSearchDones[itemID]; exists {
				return events
			}
		}
		if syntheticText := buildSyntheticWebSearchToolCallText(evt.Item.Action, state.LastWebSearchQuery, true); syntheticText != "" {
			events = append(events, emitStandaloneTextBlock(state, syntheticText)...)
			if itemID != "" {
				state.EmittedSyntheticWebSearchDones[itemID] = struct{}{}
			}
		}
	}

	return events
}

func webSearchActionQuery(action *WebSearchAction) string {
	return claudegptcompat.WebSearchActionQuery(toClaudeGPTWebSearchAction(action))
}

func webSearchActionURL(action *WebSearchAction) string {
	return claudegptcompat.WebSearchActionURL(toClaudeGPTWebSearchAction(action))
}

func webSearchQueryWithFallback(action *WebSearchAction, fallbackQuery string) string {
	return claudegptcompat.WebSearchQueryWithFallback(toClaudeGPTWebSearchAction(action), fallbackQuery)
}

func sanitizedWebSearchQueries(action *WebSearchAction) []string {
	return claudegptcompat.SanitizedWebSearchQueries(toClaudeGPTWebSearchAction(action))
}

func webSearchToolInputJSON(action *WebSearchAction, query string) json.RawMessage {
	return claudegptcompat.WebSearchToolInputJSON(toClaudeGPTWebSearchAction(action), query)
}

type anthropicWebSearchResult = claudegptcompat.WebSearchResult

func webSearchToolResultContent(action *WebSearchAction) json.RawMessage {
	results := webSearchResultsFromAction(action)
	raw, _ := json.Marshal(results)
	return raw
}

func webSearchResultsFromAction(action *WebSearchAction) []anthropicWebSearchResult {
	return claudegptcompat.WebSearchResultsFromAction(toClaudeGPTWebSearchAction(action))
}

func normalizeWebSearchURL(raw string) string {
	return claudegptcompat.NormalizeWebSearchURL(raw)
}

func responsesAnnotationsToAnthropicCitations(annotations []ResponsesAnnotation, text string) []AnthropicCitation {
	if len(annotations) == 0 {
		return nil
	}
	citations := make([]AnthropicCitation, 0, len(annotations))
	for _, annotation := range annotations {
		citation := responsesAnnotationToAnthropicCitation(annotation, text)
		if citation != nil {
			citations = append(citations, *citation)
		}
	}
	return citations
}

func responsesAnnotationToAnthropicCitation(annotation ResponsesAnnotation, text string) *AnthropicCitation {
	if !strings.EqualFold(strings.TrimSpace(annotation.Type), "url_citation") {
		return nil
	}
	u := normalizeWebSearchURL(annotation.URL)
	if u == "" {
		return nil
	}
	return &AnthropicCitation{
		Type:      "web_search_result_location",
		URL:       u,
		Title:     strings.TrimSpace(annotation.Title),
		CitedText: citationTextSlice(text, annotation.StartIndex, annotation.EndIndex),
	}
}

func citationTextSlice(text string, startIndex, endIndex int) string {
	return claudegptcompat.CitationTextSlice(text, startIndex, endIndex)
}

func emitStandaloneTextBlock(state *ResponsesEventToAnthropicState, text string) []AnthropicStreamEvent {
	events := closeCurrentBlock(state)
	idx := state.ContentBlockIndex
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_start",
		Index: &idx,
		ContentBlock: &AnthropicContentBlock{
			Type: "text",
			Text: "",
		},
	})
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: &AnthropicDelta{
			Type: "text_delta",
			Text: text,
		},
	})
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_stop",
		Index: &idx,
	})
	state.ContentBlockIndex++
	return events
}

func emitStandaloneThinkingBlock(state *ResponsesEventToAnthropicState, thinking string) []AnthropicStreamEvent {
	events := closeCurrentBlock(state)
	idx := state.ContentBlockIndex
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_start",
		Index: &idx,
		ContentBlock: &AnthropicContentBlock{
			Type:     "thinking",
			Thinking: "",
		},
	})
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: &AnthropicDelta{
			Type:     "thinking_delta",
			Thinking: thinking,
		},
	})
	events = append(events, AnthropicStreamEvent{
		Type:  "content_block_stop",
		Index: &idx,
	})
	state.ContentBlockIndex++
	return events
}

func resToAnthHandleCompleted(evt *ResponsesStreamEvent, state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if state.MessageStopSent {
		return nil
	}

	var events []AnthropicStreamEvent
	events = append(events, closeCurrentBlock(state)...)

	stopReason := "end_turn"
	if evt.Usage != nil {
		usage := anthropicUsageFromResponsesUsage(evt.Usage)
		state.InputTokens = usage.InputTokens
		state.OutputTokens = usage.OutputTokens
		state.CacheReadInputTokens = usage.CacheReadInputTokens
	}
	if evt.Response != nil {
		if evt.Response.Usage != nil {
			usage := anthropicUsageFromResponsesUsage(evt.Response.Usage)
			state.InputTokens = usage.InputTokens
			state.OutputTokens = usage.OutputTokens
			state.CacheReadInputTokens = usage.CacheReadInputTokens
		}
		switch evt.Response.Status {
		case "incomplete":
			if evt.Response.IncompleteDetails != nil && evt.Response.IncompleteDetails.Reason == "max_output_tokens" {
				stopReason = "max_tokens"
			}
		case "completed":
			if state.HasToolCall {
				stopReason = "tool_use"
			}
		}
	}

	events = append(events,
		AnthropicStreamEvent{
			Type: "message_delta",
			Delta: &AnthropicDelta{
				StopReason: stopReason,
			},
			Usage: &AnthropicUsage{
				InputTokens:          state.InputTokens,
				OutputTokens:         state.OutputTokens,
				CacheReadInputTokens: state.CacheReadInputTokens,
			},
		},
		AnthropicStreamEvent{Type: "message_stop"},
	)
	state.MessageStopSent = true
	return events
}

func closeCurrentBlock(state *ResponsesEventToAnthropicState) []AnthropicStreamEvent {
	if !state.ContentBlockOpen {
		return nil
	}
	idx := state.ContentBlockIndex
	state.ContentBlockOpen = false
	state.ContentBlockIndex++
	state.CurrentToolName = ""
	state.CurrentToolArgs = ""
	state.CurrentToolHadDelta = false
	return []AnthropicStreamEvent{{
		Type:  "content_block_stop",
		Index: &idx,
	}}
}
