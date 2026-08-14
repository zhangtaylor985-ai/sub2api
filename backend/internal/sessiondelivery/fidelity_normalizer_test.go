package sessiondelivery

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/thinkingsig"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProjectionFidelityRemovesCodexArtifactsAndNormalizesToolIDs(t *testing.T) {
	request := json.RawMessage(`{
		"model":"claude-opus-5",
		"max_tokens":1024,
		"thinking":{"type":"adaptive","display":"omitted"},
		"messages":[
			{"role":"user","content":[
				{"type":"input_text","text":"keep"},
				{"type":"encrypted_content","encrypted_content":"opaque"}
			]},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_abc123","name":"Read","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_abc123","content":"ok"}]}
		]
	}`)
	response := json.RawMessage(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5",
		"content":[{"type":"tool_use","id":"call_xyz789","name":"Read","input":{}}],
		"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}
	}`)

	normalizedRequest, normalizedResponse, stats, err := normalizeProjectionFidelity(
		request,
		response,
		fidelityNormalizationOptions{CodexProjection: true},
	)
	require.NoError(t, err)
	require.Equal(t, int64(3), stats.ToolIDsNormalized)
	require.Equal(t, int64(2), stats.OpenAIContentBlocksNormalized)
	require.NotContains(t, string(normalizedRequest), "input_text")
	require.NotContains(t, string(normalizedRequest), "encrypted_content")
	require.Contains(t, string(normalizedRequest), `"type":"text"`)
	require.Contains(t, string(normalizedRequest), `"id":"toolu_abc123"`)
	require.Contains(t, string(normalizedRequest), `"tool_use_id":"toolu_abc123"`)
	require.Contains(t, string(normalizedResponse), `"id":"toolu_xyz789"`)
}

func TestNormalizeProjectionFidelityNormalizesLegacyTooluseIDs(t *testing.T) {
	request := json.RawMessage(`{
		"model":"claude-opus-5",
		"max_tokens":1024,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"tooluse_F4dwodeQ","name":"Read","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tooluse_F4dwodeQ","content":"ok"}]}
		]
	}`)
	response := json.RawMessage(`{
		"id":"msg_legacy_tool","type":"message","role":"assistant","model":"claude-opus-5",
		"content":[{"type":"text","text":"done"}],
		"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}
	}`)

	normalizedRequest, _, stats, err := normalizeProjectionFidelity(
		request,
		response,
		fidelityNormalizationOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, int64(2), stats.ToolIDsNormalized)
	require.NotContains(t, string(normalizedRequest), "tooluse_")
	require.Contains(t, string(normalizedRequest), `"id":"toolu_F4dwodeQ"`)
	require.Contains(t, string(normalizedRequest), `"tool_use_id":"toolu_F4dwodeQ"`)
}

func TestNormalizeProjectionFidelityDropsHistoricalThinkingWhenDisabled(t *testing.T) {
	signature := thinkingsig.Generate(DefaultPublicModel, 900)
	request := json.RawMessage(`{
		"model":"claude-opus-5","max_tokens":1024,
		"thinking":{"type":"disabled"},
		"messages":[{"role":"user","content":"hello"}]
	}`)
	response := mustJSON(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": DefaultPublicModel,
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "", "signature": signature},
			map[string]any{"type": "text", "text": "ok"},
		},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
	})

	_, normalizedResponse, stats, err := normalizeProjectionFidelity(
		request,
		response,
		fidelityNormalizationOptions{RemoveSignedWhenDisabled: true},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.ResponseThinkingRemoved)
	require.NotContains(t, string(normalizedResponse), `"type":"thinking"`)
}

func TestNormalizeProjectionFidelityMovesThinkingBeforeServerTools(t *testing.T) {
	signature := thinkingsig.Generate(DefaultPublicModel, 900)
	request := json.RawMessage(`{
		"model":"claude-opus-5","max_tokens":1024,
		"thinking":{"type":"adaptive","display":"omitted"},
		"messages":[{"role":"user","content":"search"}]
	}`)
	response := mustJSON(map[string]any{
		"id": "msg_search", "type": "message", "role": "assistant", "model": DefaultPublicModel,
		"content": []any{
			map[string]any{"type": "server_tool_use", "id": "call_search", "name": "web_search", "input": map[string]any{"query": "example"}},
			map[string]any{"type": "web_search_tool_result", "tool_use_id": "call_search", "content": []any{}},
			map[string]any{"type": "thinking", "thinking": "", "signature": signature},
			map[string]any{"type": "text", "text": "done"},
		},
		"stop_reason": "end_turn",
		"usage": map[string]any{
			"input_tokens": 2, "output_tokens": 5,
			"cache_creation_input_tokens": 10, "cache_read_input_tokens": 0,
			"cache_creation":  map[string]any{"ephemeral_5m_input_tokens": 10, "ephemeral_1h_input_tokens": 0},
			"server_tool_use": map[string]any{"web_search_requests": 1, "web_fetch_requests": 0},
			"service_tier":    "standard", "inference_geo": "global", "iterations": []any{}, "speed": "standard",
		},
	})

	normalizedRequest, normalizedResponse, _, err := normalizeProjectionFidelity(
		request,
		response,
		fidelityNormalizationOptions{CodexProjection: true},
	)
	require.NoError(t, err)

	var content []map[string]json.RawMessage
	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(normalizedResponse, &decoded))
	require.NoError(t, json.Unmarshal(decoded["content"], &content))
	require.Equal(t, []string{"thinking", "server_tool_use", "web_search_tool_result", "text"}, []string{
		rawString(content[0]["type"]),
		rawString(content[1]["type"]),
		rawString(content[2]["type"]),
		rawString(content[3]["type"]),
	})
	require.Equal(t, "srvtoolu_search", rawString(content[1]["id"]))

	record := usageTestRecord("session_reorder", fixedTestTime(), 100, 10, "search")
	record.Request = normalizedRequest
	record.Response.ResponseData = normalizedResponse
	require.NoError(t, ValidateDeliveryFidelity(record, DefaultPublicModel))
}

func TestValidateOpus5SignatureShape(t *testing.T) {
	require.NoError(t, validateOpus5SignatureShape(thinkingsig.Generate(DefaultPublicModel, 1480), DefaultPublicModel))
	require.Error(t, validateOpus5SignatureShape("not-a-signature", DefaultPublicModel))
}

func TestRealOpus5ReferenceSignatureShape(t *testing.T) {
	path := os.Getenv("SESSION_FIDELITY_REAL_CLAUDE_JSONL")
	if path == "" {
		t.Skip("SESSION_FIDELITY_REAL_CLAUDE_JSONL is not configured")
	}
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	validated := 0
	for scanner.Scan() {
		var line struct {
			Type    string `json:"type"`
			Message struct {
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &line))
		if line.Type != "assistant" || line.Message.Model != DefaultPublicModel {
			continue
		}
		var blocks []struct {
			Type      string `json:"type"`
			Signature string `json:"signature"`
		}
		if json.Unmarshal(line.Message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type != "thinking" || block.Signature == "" {
				continue
			}
			require.NoError(t, validateOpus5SignatureShape(block.Signature, DefaultPublicModel))
			validated++
		}
	}
	require.NoError(t, scanner.Err())
	require.Greater(t, validated, 0)
}

func TestValidateDeliveryFidelityRejectsOpenAIContentBlock(t *testing.T) {
	record := usageTestRecord("session_fidelity", fixedTestTime(), 100, 10, "hello")
	record.Request = json.RawMessage(`{
		"model":"claude-opus-5","max_tokens":1024,
		"thinking":{"type":"adaptive","display":"omitted"},
		"messages":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]
	}`)
	signature := thinkingsig.Generate(DefaultPublicModel, 900)
	record.Response.ResponseData = mustJSON(map[string]any{
		"id": "msg_fidelity", "type": "message", "role": "assistant", "model": DefaultPublicModel,
		"content":     []any{map[string]any{"type": "thinking", "thinking": "", "signature": signature}},
		"stop_reason": "end_turn",
		"usage": map[string]any{
			"input_tokens": 2, "output_tokens": 1,
			"cache_creation_input_tokens": 10, "cache_read_input_tokens": 0,
			"cache_creation":  map[string]any{"ephemeral_5m_input_tokens": 10, "ephemeral_1h_input_tokens": 0},
			"server_tool_use": map[string]any{"web_search_requests": 0, "web_fetch_requests": 0},
			"service_tier":    "standard", "inference_geo": "global", "iterations": []any{}, "speed": "standard",
		},
	})
	require.ErrorContains(t, ValidateDeliveryFidelity(record, DefaultPublicModel), "OpenAI block type")
}

func TestRealClientSpoolProjectsToOpus5Fidelity(t *testing.T) {
	spoolDir := os.Getenv("SESSION_FIDELITY_CANARY_SPOOL")
	if spoolDir == "" {
		t.Skip("SESSION_FIDELITY_CANARY_SPOOL is not configured")
	}
	spool, err := NewSpool(spoolDir, 1<<30)
	require.NoError(t, err)
	paths, err := spool.ListPending()
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	envelopes := make([]*Envelope, 0, len(paths))
	for _, path := range paths {
		envelope, readErr := spool.ReadEnvelope(path)
		require.NoError(t, readErr)
		if envelope.Delivery != nil {
			envelopes = append(envelopes, envelope)
		}
	}
	require.NotEmpty(t, envelopes)
	sort.Slice(envelopes, func(i, j int) bool {
		if envelopes[i].SessionID == envelopes[j].SessionID {
			if envelopes[i].OccurredAt.Equal(envelopes[j].OccurredAt) {
				return envelopes[i].RequestID < envelopes[j].RequestID
			}
			return envelopes[i].OccurredAt.Before(envelopes[j].OccurredAt)
		}
		return envelopes[i].SessionID < envelopes[j].SessionID
	})

	echo := &echoRepair{}
	usage := &usageProjector{}
	auditStates := make(map[string]*fidelityAuditSession)
	report := &FidelityAuditReport{PublicModel: DefaultPublicModel}
	for _, envelope := range envelopes {
		record := *envelope.Delivery
		request, decodeErr := decodeJSONObject(record.Request, "request")
		require.NoError(t, decodeErr)
		normalizedRequest, normalizedResponse, _, normalizeErr := normalizeProjectionFidelity(
			record.Request,
			record.Response.ResponseData,
			fidelityNormalizationOptions{
				CodexProjection:          envelope.Source.Protocol == ProtocolOpenAIResponses || isLegacyCodexDeliveryRequest(request),
				RemoveSignedWhenDisabled: true,
			},
		)
		require.NoError(t, normalizeErr)
		record.Request = normalizedRequest
		record.Response.ResponseData = normalizedResponse
		require.NoError(t, echo.process(&record))
		require.NoError(t, usage.process(&record))
		require.NoError(t, ValidateDeliveryFidelity(&record, DefaultPublicModel))

		state := auditStates[record.SessionID]
		if state == nil {
			state = &fidelityAuditSession{}
			auditStates[record.SessionID] = state
		}
		report.Records++
		collectFidelityRecordStats(report, &record)
		auditThinkingEcho(report, state, &record)
		auditUsageChain(report, state, &record)
	}
	require.Zero(t, report.ViolationCount, report.Violations)
	require.Greater(t, report.ThinkingResponseRecords, int64(0))
	require.Greater(t, report.ToolUseRecords, int64(0))
	require.Greater(t, report.ExactEchoMatches, int64(0))
	t.Logf(
		"content-free canary audit: records=%d thinking_responses=%d request_echoes=%d exact_echoes=%d tool_records=%d cache_continuations=%d cache_restarts=%d",
		report.Records,
		report.ThinkingResponseRecords,
		report.RequestEchoThinking,
		report.ExactEchoMatches,
		report.ToolUseRecords,
		report.CacheContinuations,
		report.CacheRestarts,
	)
}

func TestStripOpenAISearchTracking(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		want     string
		stripped int64
	}{
		{"trailing param", "see [geo](https://ncbi.nlm.nih.gov/geo?acc=GSE1&utm_source=openai)", "see [geo](https://ncbi.nlm.nih.gov/geo?acc=GSE1)", 1},
		{"sole param drops question mark", "https://example.com?utm_source=openai", "https://example.com", 1},
		{"sole param before paren", "[x](https://example.com?utm_source=openai)", "[x](https://example.com)", 1},
		{"first of several", "https://example.com?utm_source=openai&acc=GSE1", "https://example.com?acc=GSE1", 1},
		{"middle param", "https://example.com?a=1&utm_source=openai&b=2", "https://example.com?a=1&b=2", 1},
		{"multiple urls", "a ?x=1&utm_source=openai b ?y=2&utm_source=openai c", "a ?x=1 b ?y=2 c", 2},
		{"uppercase", "https://example.com?a=1&UTM_SOURCE=OPENAI", "https://example.com?a=1", 1},
		{"longer value preserved", "https://example.com?utm_source=openai2", "https://example.com?utm_source=openai2", 0},
		{"not a query param preserved", "see xutm_source=openai here", "see xutm_source=openai here", 0},
		{"plain mention preserved", "the utm_source=openai parameter", "the utm_source=openai parameter", 0},
		{"no match", "https://example.com?acc=GSE1", "https://example.com?acc=GSE1", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, stripped := stripOpenAISearchTracking(tc.input)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.stripped, stripped)
		})
	}
}

func TestNormalizeProjectionFidelityStripsAssistantTracking(t *testing.T) {
	assistantText := "sources: [a](https://ncbi.nlm.nih.gov/geo?acc=GSE1&utm_source=openai) and [b](https://example.com?utm_source=openai)"
	userText := "I found this link https://chatgpt.example.com/?utm_source=openai myself"
	request := json.RawMessage(`{
		"model":"claude-opus-5","max_tokens":1024,
		"thinking":{"type":"adaptive","display":"omitted"},
		"messages":[
			{"role":"user","content":[{"type":"text","text":` + string(mustJSON(userText)) + `}]},
			{"role":"assistant","content":[{"type":"text","text":` + string(mustJSON(assistantText)) + `}]},
			{"role":"user","content":[{"type":"text","text":"next"}]}
		]
	}`)
	response := mustJSON(map[string]any{
		"id": "msg_utm", "type": "message", "role": "assistant", "model": DefaultPublicModel,
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "", "signature": thinkingsig.Generate(DefaultPublicModel, 900)},
			map[string]any{"type": "text", "text": assistantText},
		},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
	})

	normalizedRequest, normalizedResponse, stats, err := normalizeProjectionFidelity(
		request,
		response,
		fidelityNormalizationOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, int64(4), stats.AssistantTextTrackingStripped)
	require.NotContains(t, string(normalizedResponse), "utm_source=openai")
	// User-authored text is authentic client input and must stay untouched.
	require.Contains(t, string(normalizedRequest), "utm_source=openai")
	require.Contains(t, string(normalizedRequest), "chatgpt.example.com")

	// The sanitized response text and its echo in later request history must
	// stay byte-identical, like real Claude Code multi-turn traffic.
	var req struct {
		Messages []struct {
			Role    string                   `json:"role"`
			Content []map[string]interface{} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(normalizedRequest, &req))
	var resp struct {
		Content []map[string]interface{} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(normalizedResponse, &resp))
	require.Equal(t, req.Messages[1].Content[0]["text"], resp.Content[1]["text"])
	require.Contains(t, resp.Content[1]["text"], "acc=GSE1")
}

func TestValidateDeliveryFidelityRejectsOpenAISearchTracking(t *testing.T) {
	signature := thinkingsig.Generate(DefaultPublicModel, 900)
	validUsage := map[string]any{
		"input_tokens": 2, "output_tokens": 1,
		"cache_creation_input_tokens": 10, "cache_read_input_tokens": 0,
		"cache_creation":  map[string]any{"ephemeral_5m_input_tokens": 10, "ephemeral_1h_input_tokens": 0},
		"server_tool_use": map[string]any{"web_search_requests": 0, "web_fetch_requests": 0},
		"service_tier":    "standard", "inference_geo": "global", "iterations": []any{}, "speed": "standard",
	}
	buildRecord := func(request json.RawMessage, responseText string) *DeliveryRecord {
		record := usageTestRecord("session_utm_gate", fixedTestTime(), 100, 10, "hello")
		record.Request = request
		record.Response.ResponseData = mustJSON(map[string]any{
			"id": "msg_utm_gate", "type": "message", "role": "assistant", "model": DefaultPublicModel,
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "", "signature": signature},
				map[string]any{"type": "text", "text": responseText},
			},
			"stop_reason": "end_turn",
			"usage":       validUsage,
		})
		return record
	}

	t.Run("response text", func(t *testing.T) {
		record := buildRecord(
			json.RawMessage(`{"model":"claude-opus-5","max_tokens":1024,"thinking":{"type":"adaptive","display":"omitted"},"messages":[{"role":"user","content":"hello"}]}`),
			"see https://example.com?a=1&utm_source=openai",
		)
		require.ErrorContains(t, ValidateDeliveryFidelity(record, DefaultPublicModel), "OpenAI search tracking parameter")
	})

	t.Run("assistant history text", func(t *testing.T) {
		record := buildRecord(
			json.RawMessage(`{"model":"claude-opus-5","max_tokens":1024,"thinking":{"type":"adaptive","display":"omitted"},"messages":[{"role":"assistant","content":[{"type":"text","text":"see https://example.com?utm_source=openai"}]},{"role":"user","content":"next"}]}`),
			"clean",
		)
		require.ErrorContains(t, ValidateDeliveryFidelity(record, DefaultPublicModel), "OpenAI search tracking parameter")
	})

	t.Run("assistant tool input", func(t *testing.T) {
		record := buildRecord(
			json.RawMessage(`{"model":"claude-opus-5","max_tokens":1024,"thinking":{"type":"adaptive","display":"omitted"},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_abc","name":"WebFetch","input":{"url":"https://example.com?utm_source=openai"}}]},{"role":"user","content":"next"}]}`),
			"clean",
		)
		require.ErrorContains(t, ValidateDeliveryFidelity(record, DefaultPublicModel), "OpenAI search tracking parameter")
	})

	t.Run("user text passes", func(t *testing.T) {
		record := buildRecord(
			json.RawMessage(`{"model":"claude-opus-5","max_tokens":1024,"thinking":{"type":"adaptive","display":"omitted"},"messages":[{"role":"user","content":"look at https://example.com?utm_source=openai"}]}`),
			"clean",
		)
		require.NoError(t, ValidateDeliveryFidelity(record, DefaultPublicModel))
	})
}

func fixedTestTime() (value time.Time) {
	return time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
}
