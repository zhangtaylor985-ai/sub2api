package sessiondelivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxFidelityAuditViolations = 100

type FidelityAuditSample struct {
	Category           string `json:"category"`
	RequestFingerprint string `json:"request_fingerprint"`
	ExportHour         string `json:"export_hour"`
}

// FidelityAuditReport contains content-free evidence from a complete archive
// scan. Request text, response text and signatures are never included.
type FidelityAuditReport struct {
	Passed                  bool                  `json:"passed"`
	PublicModel             string                `json:"public_model"`
	Archives                int                   `json:"archives"`
	Sessions                int                   `json:"sessions"`
	CrossHourSessions       int                   `json:"cross_hour_sessions"`
	CrossArchiveOverlaps    int64                 `json:"cross_archive_timestamp_overlaps"`
	Records                 int64                 `json:"records"`
	ClaudeShapeRecords      int64                 `json:"claude_shape_records"`
	CodexShapeRecords       int64                 `json:"codex_shape_records"`
	ThinkingResponseRecords int64                 `json:"thinking_response_records"`
	ThinkingBlocks          int64                 `json:"thinking_blocks"`
	RequestEchoThinking     int64                 `json:"request_echo_thinking_blocks"`
	ToolUseRecords          int64                 `json:"tool_use_records"`
	ServerToolRecords       int64                 `json:"server_tool_records"`
	ExactEchoMatches        int64                 `json:"exact_echo_matches"`
	CacheContinuations      int64                 `json:"cache_continuations"`
	CacheRestarts           int64                 `json:"cache_restarts"`
	ViolationCount          int64                 `json:"violation_count"`
	Violations              []string              `json:"violations,omitempty"`
	Samples                 []FidelityAuditSample `json:"samples"`
}

type fidelityAuditSession struct {
	maxTimestamp time.Time
	hours        map[string]struct{}
	echo         echoRepair
	usage        usageProjector
}

// AuditArchivesFidelity validates every record and then checks cross-record
// Opus 5 invariants: exact thinking echo and the measured prompt-cache chain.
func AuditArchivesFidelity(ctx context.Context, inputDir, publicModel string) (*FidelityAuditReport, error) {
	inputDir, err := filepath.Abs(strings.TrimSpace(inputDir))
	if err != nil {
		return nil, fmt.Errorf("resolve fidelity audit input directory: %w", err)
	}
	publicModel = strings.TrimSpace(publicModel)
	if publicModel == "" {
		publicModel = DefaultPublicModel
	}
	inputs, err := inventoryRebuildInputs(ctx, inputDir, publicModel)
	if err != nil {
		return nil, err
	}

	report := &FidelityAuditReport{PublicModel: publicModel, Archives: len(inputs)}
	sessions := make(map[string]*fidelityAuditSession)
	sampleByCategory := make(map[string]FidelityAuditSample)
	for _, input := range inputs {
		exportHour := input.hour.Format("2006-01-02T15:00:00Z")
		if err := forEachArchiveSession(input.path, func(sessionID string, records []*DeliveryRecord) error {
			state := sessions[sessionID]
			if state == nil {
				state = &fidelityAuditSession{hours: make(map[string]struct{})}
				sessions[sessionID] = state
			}
			state.hours[exportHour] = struct{}{}
			for _, record := range records {
				if err := ctx.Err(); err != nil {
					return err
				}
				report.Records++
				// Archives are partitioned by ingested_at while the delivered
				// timestamp is the request start. Concurrent requests can therefore
				// overlap at an archive boundary without corrupting either archive.
				// The per-archive reader already enforces timestamp order within a
				// Session entry; retain cross-archive overlap as audit evidence.
				if !state.maxTimestamp.IsZero() && record.Timestamp.Before(state.maxTimestamp) {
					report.CrossArchiveOverlaps++
				} else {
					state.maxTimestamp = record.Timestamp.Time
				}

				request, requestErr := decodeJSONObject(record.Request, "request")
				if requestErr != nil {
					addFidelityViolation(report, record, requestErr.Error())
					continue
				}
				codexShape := isLegacyCodexDeliveryRequest(request)
				if codexShape {
					report.CodexShapeRecords++
				} else {
					report.ClaudeShapeRecords++
				}
				if fidelityErr := ValidateDeliveryFidelity(record, publicModel); fidelityErr != nil {
					addFidelityViolation(report, record, fidelityErr.Error())
				}
				collectFidelityRecordStats(report, record)
				auditThinkingEcho(report, state, record)
				auditUsageChain(report, state, record)
				collectFidelitySamples(sampleByCategory, record, request, codexShape, exportHour)
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("audit archive %s: %w", filepath.Base(input.path), err)
		}
	}

	report.Sessions = len(sessions)
	for _, state := range sessions {
		if len(state.hours) > 1 {
			report.CrossHourSessions++
		}
	}
	categories := make([]string, 0, len(sampleByCategory))
	for category := range sampleByCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		report.Samples = append(report.Samples, sampleByCategory[category])
	}
	report.Passed = report.ViolationCount == 0
	return report, nil
}

func collectFidelityRecordStats(report *FidelityAuditReport, record *DeliveryRecord) {
	response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
	if err != nil {
		return
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(response["content"], &content) != nil {
		return
	}
	hasThinking := false
	hasTool := false
	hasServerTool := false
	for _, block := range content {
		switch rawString(block["type"]) {
		case "thinking":
			hasThinking = true
			report.ThinkingBlocks++
		case "tool_use":
			hasTool = true
		case "server_tool_use":
			hasServerTool = true
		}
	}
	if hasThinking {
		report.ThinkingResponseRecords++
	}
	if hasTool {
		report.ToolUseRecords++
	}
	if hasServerTool {
		report.ServerToolRecords++
	}

	request, err := decodeJSONObject(record.Request, "request")
	if err != nil {
		return
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(request["messages"], &messages) != nil {
		return
	}
	for _, message := range messages {
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(message["content"], &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if rawString(block["type"]) == "thinking" {
				report.RequestEchoThinking++
			}
		}
	}
}

func auditThinkingEcho(report *FidelityAuditReport, state *fidelityAuditSession, record *DeliveryRecord) {
	request, err := decodeJSONObject(record.Request, "request")
	if err != nil || !requestThinkingEnabled(request) {
		_ = state.echo.collectResponse(record.Response.ResponseData)
		return
	}
	var messages []json.RawMessage
	if json.Unmarshal(request["messages"], &messages) != nil {
		_ = state.echo.collectResponse(record.Response.ResponseData)
		return
	}
	priorIndex := len(state.echo.prior) - 1
	for index := len(messages) - 1; index >= 0 && priorIndex >= 0; index-- {
		var message map[string]json.RawMessage
		if json.Unmarshal(messages[index], &message) != nil || rawString(message["role"]) != "assistant" {
			continue
		}
		var content []json.RawMessage
		if json.Unmarshal(message["content"], &content) != nil || len(content) == 0 {
			continue
		}
		key := assistantContentKey(content)
		if key == "" {
			continue
		}
		matched := -1
		for candidate := priorIndex; candidate >= 0; candidate-- {
			if state.echo.prior[candidate].key == key {
				matched = candidate
				break
			}
		}
		if matched < 0 {
			continue
		}
		priorIndex = matched - 1
		want := state.echo.prior[matched].thinking
		if len(want) == 0 {
			continue
		}
		var got []json.RawMessage
		for _, rawBlock := range content {
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(rawBlock, &head) == nil && head.Type == "thinking" {
				got = append(got, rawBlock)
			}
		}
		if len(got) != len(want) {
			addFidelityViolation(report, record, "request history is missing a prior response thinking echo")
			continue
		}
		exact := true
		for blockIndex := range want {
			if !bytes.Equal(bytes.TrimSpace(got[blockIndex]), bytes.TrimSpace(want[blockIndex])) {
				exact = false
				break
			}
		}
		if !exact {
			addFidelityViolation(report, record, "request history thinking echo is not byte-exact")
			continue
		}
		report.ExactEchoMatches++
	}
	if err := state.echo.collectResponse(record.Response.ResponseData); err != nil {
		addFidelityViolation(report, record, "could not collect response thinking for echo audit")
	}
}

func auditUsageChain(report *FidelityAuditReport, state *fidelityAuditSession, record *DeliveryRecord) {
	response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
	if err != nil {
		return
	}
	var usage map[string]json.RawMessage
	if json.Unmarshal(response["usage"], &usage) != nil {
		return
	}
	read := rawInt(usage["cache_read_input_tokens"])
	creation := rawInt(usage["cache_creation_input_tokens"])
	prefix := read + creation
	messageKey := firstUserMessageKey(record.Request)
	newChain := usageChainRestarts(
		state.usage.haveState,
		messageKey,
		state.usage.firstMsgKey,
		record.Timestamp.Time,
		state.usage.prevOccurred,
	)
	wantRead := 0
	if !newChain {
		wantRead = state.usage.prevPrefix
		if wantRead > prefix {
			wantRead = prefix
		}
	}
	wantCreation := prefix - wantRead
	if read != wantRead || creation != wantCreation {
		addFidelityViolation(report, record, "usage cache read/create chain does not match the Opus 5 progression")
	}
	if newChain {
		report.CacheRestarts++
	} else {
		report.CacheContinuations++
	}
	state.usage.prevPrefix = prefix
	state.usage.firstMsgKey = messageKey
	state.usage.prevOccurred = record.Timestamp.Time
	state.usage.haveState = true
}

func collectFidelitySamples(
	samples map[string]FidelityAuditSample,
	record *DeliveryRecord,
	request map[string]json.RawMessage,
	codexShape bool,
	exportHour string,
) {
	categoryPrefix := "claude"
	if codexShape {
		categoryPrefix = "codex"
	}
	effort := requestEffort(request)
	if effort == "" {
		effort = "no-effort"
	}
	addFidelitySample(samples, categoryPrefix+"-"+effort, record, exportHour)

	response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
	if err != nil {
		return
	}
	var content []map[string]json.RawMessage
	if json.Unmarshal(response["content"], &content) != nil {
		return
	}
	for _, block := range content {
		switch rawString(block["type"]) {
		case "tool_use":
			addFidelitySample(samples, categoryPrefix+"-tool-use", record, exportHour)
		case "server_tool_use":
			addFidelitySample(samples, categoryPrefix+"-server-tool", record, exportHour)
		}
	}
}

func addFidelitySample(samples map[string]FidelityAuditSample, category string, record *DeliveryRecord, exportHour string) {
	digest := sha256.Sum256([]byte(record.RequestID))
	candidate := FidelityAuditSample{
		Category:           category,
		RequestFingerprint: hex.EncodeToString(digest[:6]),
		ExportHour:         exportHour,
	}
	current, exists := samples[category]
	if !exists || candidate.RequestFingerprint < current.RequestFingerprint {
		samples[category] = candidate
	}
}

func addFidelityViolation(report *FidelityAuditReport, record *DeliveryRecord, message string) {
	report.ViolationCount++
	if len(report.Violations) >= maxFidelityAuditViolations {
		return
	}
	digest := sha256.Sum256([]byte(record.RequestID))
	report.Violations = append(report.Violations, fmt.Sprintf("request=%s: %s", hex.EncodeToString(digest[:6]), message))
}
