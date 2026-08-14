package sessiondelivery

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// RebuildArchivesConfig controls an offline, archive-to-archive projection.
// The operation never connects to the Session database and never mutates an
// input archive.
type RebuildArchivesConfig struct {
	InputDir    string
	OutputDir   string
	PublicModel string
	Allow       bool
}

// RebuildChangeStats reports which latest projection stages changed records.
type RebuildChangeStats struct {
	Records                         int64 `json:"records"`
	ChangedRecords                  int64 `json:"changed_records"`
	RequestShapeNormalized          int64 `json:"request_shape_normalized"`
	CodexBootstrapFragmentsRemoved  int64 `json:"codex_bootstrap_fragments_removed"`
	ToolIDsNormalized               int64 `json:"tool_ids_normalized"`
	OpenAIContentBlocksNormalized   int64 `json:"openai_content_blocks_normalized"`
	ResponseThinkingRemoved         int64 `json:"response_thinking_removed"`
	ResponseThinkingCompleted       int64 `json:"response_thinking_completed"`
	RequestThinkingEchoRepaired     int64 `json:"request_thinking_echo_repaired"`
	RequestHistoryThinkingCompleted int64 `json:"request_history_thinking_completed"`
	UsageReprojected                int64 `json:"usage_reprojected"`
}

// RebuildArchiveResult describes one validated input and its independently
// validated local replacement object.
type RebuildArchiveResult struct {
	Hour          time.Time          `json:"hour"`
	InputPath     string             `json:"input_path"`
	InputSHA256   string             `json:"input_sha256"`
	InputSize     int64              `json:"input_size"`
	OutputPath    string             `json:"output_path"`
	OutputSHA256  string             `json:"output_sha256"`
	OutputSize    int64              `json:"output_size"`
	RecordCount   int64              `json:"record_count"`
	ExcludedCount int64              `json:"excluded_count"`
	Manifest      ExportManifest     `json:"-"`
	Changes       RebuildChangeStats `json:"changes"`
}

// RebuildArchivesResult is the complete offline rebuild report.
type RebuildArchivesResult struct {
	InputDir      string                 `json:"input_dir"`
	OutputDir     string                 `json:"output_dir"`
	Sessions      int                    `json:"sessions"`
	Changes       RebuildChangeStats     `json:"changes"`
	Archives      []RebuildArchiveResult `json:"archives"`
	FidelityAudit *FidelityAuditReport   `json:"fidelity_audit"`
}

type rebuildArchiveInput struct {
	path       string
	sha256     string
	size       int64
	hour       time.Time
	validation *ArchiveValidation
}

// RebuildArchives replays previously delivered JSONL records through the
// latest delivery-only normalization, thinking completion, cross-turn echo,
// and usage projection. Inputs are validated in full before any output is
// written, and outputs must live in a separate directory.
func RebuildArchives(ctx context.Context, config RebuildArchivesConfig) (*RebuildArchivesResult, error) {
	if !config.Allow {
		return nil, errors.New("archive rebuild requires an explicit allow-rebuild flag")
	}
	publicModel := strings.TrimSpace(config.PublicModel)
	if publicModel == "" {
		publicModel = DefaultPublicModel
	}
	inputDir, outputDir, err := prepareRebuildDirectories(config.InputDir, config.OutputDir)
	if err != nil {
		return nil, err
	}
	inputs, err := inventoryRebuildInputs(ctx, inputDir, publicModel)
	if err != nil {
		return nil, err
	}
	if err := requireEmptyArchiveOutput(outputDir); err != nil {
		return nil, err
	}
	backend, err := NewLocalArchiveBackend(outputDir)
	if err != nil {
		return nil, err
	}

	echoBySession := make(map[string]*echoRepair)
	usageBySession := make(map[string]*usageProjector)
	lastTimestamp := make(map[string]time.Time)
	result := &RebuildArchivesResult{InputDir: inputDir, OutputDir: outputDir}
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rebuilt, err := rebuildArchive(ctx, input, outputDir, backend, publicModel, echoBySession, usageBySession, lastTimestamp)
		if err != nil {
			return nil, fmt.Errorf("rebuild Session archive %s: %w", filepath.Base(input.path), err)
		}
		result.Archives = append(result.Archives, rebuilt)
		addRebuildStats(&result.Changes, rebuilt.Changes)
	}
	result.Sessions = len(lastTimestamp)
	fidelityAudit, err := AuditArchivesFidelity(ctx, outputDir, publicModel)
	if err != nil {
		return nil, fmt.Errorf("audit rebuilt Session archives: %w", err)
	}
	result.FidelityAudit = fidelityAudit
	if !fidelityAudit.Passed {
		return nil, fmt.Errorf("rebuilt Session archives failed fidelity audit with %d violation(s)", fidelityAudit.ViolationCount)
	}
	return result, nil
}

func prepareRebuildDirectories(inputValue, outputValue string) (string, string, error) {
	inputValue = strings.TrimSpace(inputValue)
	outputValue = strings.TrimSpace(outputValue)
	if inputValue == "" || outputValue == "" {
		return "", "", errors.New("archive rebuild input and output directories are required")
	}
	inputDir, err := filepath.Abs(inputValue)
	if err != nil {
		return "", "", fmt.Errorf("resolve archive rebuild input directory: %w", err)
	}
	outputDir, err := filepath.Abs(outputValue)
	if err != nil {
		return "", "", fmt.Errorf("resolve archive rebuild output directory: %w", err)
	}
	inputInfo, err := os.Stat(inputDir)
	if err != nil {
		return "", "", fmt.Errorf("stat archive rebuild input directory: %w", err)
	}
	if !inputInfo.IsDir() {
		return "", "", errors.New("archive rebuild input path is not a directory")
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create archive rebuild output directory: %w", err)
	}
	outputInfo, err := os.Stat(outputDir)
	if err != nil {
		return "", "", fmt.Errorf("stat archive rebuild output directory: %w", err)
	}
	if !outputInfo.IsDir() {
		return "", "", errors.New("archive rebuild output path is not a directory")
	}
	if os.SameFile(inputInfo, outputInfo) {
		return "", "", errors.New("archive rebuild output directory must differ from input directory")
	}
	return inputDir, outputDir, nil
}

func requireEmptyArchiveOutput(outputDir string) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("read archive rebuild output directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tar.zst") {
			return errors.New("archive rebuild output directory already contains a .tar.zst object")
		}
	}
	return nil
}

func inventoryRebuildInputs(ctx context.Context, inputDir, publicModel string) ([]rebuildArchiveInput, error) {
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return nil, fmt.Errorf("read archive rebuild input directory: %w", err)
	}
	inputs := make([]rebuildArchiveInput, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.zst") {
			continue
		}
		path := filepath.Join(inputDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat archive rebuild input %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("archive rebuild input %s is not a regular file", entry.Name())
		}
		validation, err := ValidateArchive(path, publicModel)
		if err != nil {
			return nil, fmt.Errorf("validate archive rebuild input %s: %w", entry.Name(), err)
		}
		hour, err := time.Parse(time.RFC3339, validation.Manifest.ExportHour)
		if err != nil {
			return nil, fmt.Errorf("parse archive rebuild input hour %s: %w", entry.Name(), err)
		}
		sha, size, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, rebuildArchiveInput{
			path: path, sha256: sha, size: size, hour: hourUTC(hour), validation: validation,
		})
	}
	if len(inputs) == 0 {
		return nil, errors.New("archive rebuild input directory contains no .tar.zst objects")
	}
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].hour.Equal(inputs[j].hour) {
			return inputs[i].path < inputs[j].path
		}
		return inputs[i].hour.Before(inputs[j].hour)
	})
	for index := 1; index < len(inputs); index++ {
		if inputs[index-1].hour.Equal(inputs[index].hour) {
			return nil, fmt.Errorf("archive rebuild input repeats UTC hour %s", inputs[index].hour.Format(time.RFC3339))
		}
	}
	return inputs, nil
}

func rebuildArchive(
	ctx context.Context,
	input rebuildArchiveInput,
	outputDir string,
	backend *LocalArchiveBackend,
	publicModel string,
	echoBySession map[string]*echoRepair,
	usageBySession map[string]*usageProjector,
	lastTimestamp map[string]time.Time,
) (RebuildArchiveResult, error) {
	workDir, err := os.MkdirTemp(outputDir, ".rebuild-"+input.hour.Format("20060102-15")+"-*")
	if err != nil {
		return RebuildArchiveResult{}, fmt.Errorf("create archive rebuild work directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	stagedPath := filepath.Join(workDir, "session-delivery-"+input.hour.Format("20060102-15")+".tar.zst")

	manifest, changes, err := buildReprojectedArchive(
		ctx, input, workDir, stagedPath, publicModel, echoBySession, usageBySession, lastTimestamp,
	)
	if err != nil {
		return RebuildArchiveResult{}, err
	}
	validation, err := ValidateArchive(stagedPath, publicModel)
	if err != nil {
		return RebuildArchiveResult{}, fmt.Errorf("validate rebuilt staged archive: %w", err)
	}
	if validation.Manifest.RecordCount != input.validation.Manifest.RecordCount ||
		validation.Manifest.ExcludedCount != input.validation.Manifest.ExcludedCount {
		return RebuildArchiveResult{}, errors.New("rebuilt archive counts differ from validated input manifest")
	}
	stagedSHA, _, err := fileSHA256(stagedPath)
	if err != nil {
		return RebuildArchiveResult{}, err
	}
	name := fmt.Sprintf("session-delivery-%s-%s.tar.zst", input.hour.Format("20060102-15"), stagedSHA[:16])
	object, err := backend.Put(ctx, name, stagedPath)
	if err != nil {
		return RebuildArchiveResult{}, fmt.Errorf("commit rebuilt local archive: %w", err)
	}
	if err := backend.Verify(ctx, object); err != nil {
		return RebuildArchiveResult{}, fmt.Errorf("verify rebuilt local archive read-back: %w", err)
	}
	return RebuildArchiveResult{
		Hour:          input.hour,
		InputPath:     input.path,
		InputSHA256:   input.sha256,
		InputSize:     input.size,
		OutputPath:    object.Name,
		OutputSHA256:  object.SHA256,
		OutputSize:    object.Size,
		RecordCount:   manifest.RecordCount,
		ExcludedCount: manifest.ExcludedCount,
		Manifest:      manifest,
		Changes:       changes,
	}, nil
}

func buildReprojectedArchive(
	ctx context.Context,
	input rebuildArchiveInput,
	workDir string,
	archivePath string,
	publicModel string,
	echoBySession map[string]*echoRepair,
	usageBySession map[string]*usageProjector,
	lastTimestamp map[string]time.Time,
) (ExportManifest, RebuildChangeStats, error) {
	archiveFile, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ExportManifest{}, RebuildChangeStats{}, fmt.Errorf("create rebuilt staged archive: %w", err)
	}
	encoder, err := zstd.NewWriter(archiveFile, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		_ = archiveFile.Close()
		return ExportManifest{}, RebuildChangeStats{}, fmt.Errorf("create rebuilt archive zstd writer: %w", err)
	}
	tarWriter := tar.NewWriter(encoder)
	closed := false
	closeArchive := func() error {
		if closed {
			return nil
		}
		closed = true
		var result error
		if err := tarWriter.Close(); err != nil {
			result = errors.Join(result, err)
		}
		if err := encoder.Close(); err != nil {
			result = errors.Join(result, err)
		}
		if err := archiveFile.Sync(); err != nil {
			result = errors.Join(result, err)
		}
		if err := archiveFile.Close(); err != nil {
			result = errors.Join(result, err)
		}
		return result
	}
	defer func() { _ = closeArchive() }()

	hourEnd := input.hour.Add(time.Hour)
	manifest := input.validation.Manifest
	manifest.Files = nil
	manifest.TokenUsage = nil
	var changes RebuildChangeStats
	var tokenUsage DeliveryTokenMetrics
	sessionWriter := newSessionEntryWriter(workDir, tarWriter, hourEnd)
	iterateErr := forEachArchiveSession(input.path, func(sessionID string, records []*DeliveryRecord) error {
		echo := echoBySession[sessionID]
		if echo == nil {
			echo = &echoRepair{}
			echoBySession[sessionID] = echo
		}
		usage := usageBySession[sessionID]
		if usage == nil {
			usage = &usageProjector{}
			usageBySession[sessionID] = usage
		}
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return err
			}
			if previous := lastTimestamp[sessionID]; !previous.IsZero() && record.Timestamp.Before(previous) {
				return fmt.Errorf("session %s is not ordered across input archives", sessionID)
			}
			beforeRecord, err := json.Marshal(record)
			if err != nil {
				return err
			}
			requestShape, err := decodeJSONObject(record.Request, "request")
			if err != nil {
				return fmt.Errorf("decode record %s before fidelity normalization: %w", record.RequestID, err)
			}
			normalizedRequest, normalizedResponse, fidelityStats, err := normalizeProjectionFidelity(
				record.Request,
				record.Response.ResponseData,
				fidelityNormalizationOptions{
					CodexProjection:          isLegacyCodexDeliveryRequest(requestShape),
					RemoveSignedWhenDisabled: true,
				},
			)
			if err != nil {
				return fmt.Errorf("fidelity-normalize record %s: %w", record.RequestID, err)
			}
			record.Request = normalizedRequest
			record.Response.ResponseData = normalizedResponse
			requestChanged, bootstrapFragmentsRemoved, responseCompleted, err := normalizeHistoricalDelivery(record)
			if err != nil {
				return fmt.Errorf("normalize record %s: %w", record.RequestID, err)
			}
			beforeEcho := append(json.RawMessage(nil), record.Request...)
			if err := echo.process(record); err != nil {
				return fmt.Errorf("echo repair record %s: %w", record.RequestID, err)
			}
			echoChanged := !bytes.Equal(beforeEcho, record.Request)
			historyThinkingCompleted, err := ensureRequestHistoryThinkingSignatures(record)
			if err != nil {
				return fmt.Errorf("complete request history thinking for record %s: %w", record.RequestID, err)
			}
			beforeUsage := append(json.RawMessage(nil), record.Response.ResponseData...)
			if err := usage.process(record); err != nil {
				return fmt.Errorf("usage projection record %s: %w", record.RequestID, err)
			}
			if err := ValidateDelivery(record, publicModel); err != nil {
				return fmt.Errorf("validate rebuilt record %s: %w", record.RequestID, err)
			}
			tokens, err := ExtractDeliveryTokenMetrics(record)
			if err != nil {
				return fmt.Errorf("extract rebuilt token metrics for record %s: %w", record.RequestID, err)
			}
			if err := tokenUsage.Add(tokens); err != nil {
				return fmt.Errorf("aggregate rebuilt token metrics for record %s: %w", record.RequestID, err)
			}
			if err := sessionWriter.write(record); err != nil {
				return err
			}
			changes.Records++
			if requestChanged {
				changes.RequestShapeNormalized++
			}
			changes.CodexBootstrapFragmentsRemoved += bootstrapFragmentsRemoved
			changes.ToolIDsNormalized += fidelityStats.ToolIDsNormalized
			changes.OpenAIContentBlocksNormalized += fidelityStats.OpenAIContentBlocksNormalized
			changes.ResponseThinkingRemoved += fidelityStats.ResponseThinkingRemoved
			if responseCompleted {
				changes.ResponseThinkingCompleted++
			}
			if echoChanged {
				changes.RequestThinkingEchoRepaired++
			}
			changes.RequestHistoryThinkingCompleted += historyThinkingCompleted
			if !bytes.Equal(beforeUsage, record.Response.ResponseData) {
				changes.UsageReprojected++
			}
			afterRecord, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if !bytes.Equal(beforeRecord, afterRecord) {
				changes.ChangedRecords++
			}
			lastTimestamp[sessionID] = record.Timestamp
		}
		return nil
	})
	if iterateErr != nil {
		return ExportManifest{}, RebuildChangeStats{}, iterateErr
	}
	entries, err := sessionWriter.close()
	if err != nil {
		return ExportManifest{}, RebuildChangeStats{}, err
	}
	manifest.Files = append(manifest.Files, entries...)
	manifest.RecordCount = changes.Records
	manifest.DeliveryCount = changes.Records
	manifest.TokenUsage = &tokenUsage
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return ExportManifest{}, RebuildChangeStats{}, err
	}
	if err := writeTarBytes(tarWriter, "manifest.json", manifestJSON, hourEnd); err != nil {
		return ExportManifest{}, RebuildChangeStats{}, err
	}
	if err := closeArchive(); err != nil {
		return ExportManifest{}, RebuildChangeStats{}, fmt.Errorf("finalize rebuilt Session archive: %w", err)
	}
	return manifest, changes, nil
}

// normalizeHistoricalDelivery applies only information recoverable from an
// already delivered record. Historical archives do not contain the internal
// source protocol, so a no-metadata/no-system request carrying output_config
// effort is the retained Codex fingerprint used to replay the latest
// Responses-to-Claude request projection. Existing Anthropic signatures are
// passed to ensureThinkingSignatures unchanged.
func normalizeHistoricalDelivery(record *DeliveryRecord) (bool, int64, bool, error) {
	request, err := decodeJSONObject(record.Request, "request")
	if err != nil {
		return false, 0, false, err
	}
	requestChanged := false
	var thinking map[string]json.RawMessage
	if raw := request["thinking"]; len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &thinking); err != nil {
			return false, 0, false, fmt.Errorf("decode request thinking: %w", err)
		}
	}
	thinkingType := rawString(thinking["type"])
	thinkingDisplay := rawString(thinking["display"])
	thinkingFields := len(thinking)
	legacyCodex := requestFieldMissing(request["metadata"]) &&
		requestFieldMissing(request["system"]) && requestEffort(request) != ""
	var bootstrapFragmentsRemoved int64
	if legacyCodex && (thinkingType == "" || thinkingType == "enabled" || thinkingType == "adaptive") {
		thinking = map[string]json.RawMessage{
			"type":    mustJSON("adaptive"),
			"display": mustJSON("omitted"),
		}
		request["thinking"] = mustJSON(thinking)
		requestChanged = thinkingType != "adaptive" || thinkingDisplay != "omitted" || thinkingFields != 2
	} else if thinkingType == "adaptive" && rawString(thinking["display"]) != "omitted" {
		thinking["display"] = mustJSON("omitted")
		request["thinking"] = mustJSON(thinking)
		requestChanged = true
	}
	if legacyCodex {
		bootstrapFragmentsRemoved, err = stripHistoricalCodexBootstrap(request)
		if err != nil {
			return false, 0, false, err
		}
	}
	if requestChanged || bootstrapFragmentsRemoved > 0 {
		encoded, err := json.Marshal(request)
		if err != nil {
			return false, 0, false, fmt.Errorf("re-encode normalized request: %w", err)
		}
		record.Request = encoded
	}

	response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
	if err != nil {
		return false, 0, false, err
	}
	thinkingEnabled := requestThinkingEnabled(request)
	responseCompleted := responseNeedsThinkingCompletion(response, thinkingEnabled)
	if err := ensureThinkingSignatures(response, thinkingEnabled, false, responseOutputTokens(response)); err != nil {
		return false, 0, false, err
	}
	encodedResponse, err := json.Marshal(response)
	if err != nil {
		return false, 0, false, fmt.Errorf("re-encode completed response: %w", err)
	}
	record.Response.ResponseData = encodedResponse
	return requestChanged, bootstrapFragmentsRemoved, responseCompleted, nil
}

func requestFieldMissing(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func requestEffort(request map[string]json.RawMessage) string {
	var outputConfig map[string]json.RawMessage
	if raw := request["output_config"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &outputConfig); err != nil {
			return ""
		}
	}
	return rawString(outputConfig["effort"])
}

func responseNeedsThinkingCompletion(response map[string]json.RawMessage, thinkingEnabled bool) bool {
	var content []json.RawMessage
	if raw := response["content"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &content); err != nil {
			return false
		}
	}
	hasThinking := false
	for _, rawBlock := range content {
		var block map[string]json.RawMessage
		if json.Unmarshal(rawBlock, &block) != nil || rawString(block["type"]) != "thinking" {
			continue
		}
		hasThinking = true
		if rawString(block["signature"]) == "" {
			return true
		}
	}
	return thinkingEnabled && !hasThinking
}

func stripHistoricalCodexBootstrap(request map[string]json.RawMessage) (int64, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return 0, fmt.Errorf("decode historical Codex messages: %w", err)
	}
	filteredMessages := make([]json.RawMessage, 0, len(messages))
	var removed int64
	for _, rawMessage := range messages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil || rawString(message["role"]) != "user" {
			filteredMessages = append(filteredMessages, rawMessage)
			continue
		}
		var stringContent string
		if json.Unmarshal(message["content"], &stringContent) == nil {
			cleaned, fragments := stripHistoricalCodexBootstrapText(stringContent)
			if fragments > 0 {
				removed += fragments
				if strings.TrimSpace(cleaned) == "" {
					continue
				}
				message["content"] = mustJSON(cleaned)
				reencoded, err := json.Marshal(message)
				if err != nil {
					return 0, fmt.Errorf("re-encode historical Codex string message: %w", err)
				}
				filteredMessages = append(filteredMessages, reencoded)
				continue
			}
			filteredMessages = append(filteredMessages, rawMessage)
			continue
		}
		var content []json.RawMessage
		if err := json.Unmarshal(message["content"], &content); err != nil {
			filteredMessages = append(filteredMessages, rawMessage)
			continue
		}
		filteredContent := make([]json.RawMessage, 0, len(content))
		contentChanged := false
		for _, rawBlock := range content {
			var block map[string]json.RawMessage
			if json.Unmarshal(rawBlock, &block) == nil && rawString(block["type"]) == "text" {
				cleaned, fragments := stripHistoricalCodexBootstrapText(rawString(block["text"]))
				if fragments > 0 {
					removed += fragments
					contentChanged = true
					if strings.TrimSpace(cleaned) == "" {
						continue
					}
					block["text"] = mustJSON(cleaned)
					reencoded, err := json.Marshal(block)
					if err != nil {
						return 0, fmt.Errorf("re-encode historical Codex text block: %w", err)
					}
					rawBlock = reencoded
				}
			}
			filteredContent = append(filteredContent, rawBlock)
		}
		if len(filteredContent) == 0 && len(content) > 0 {
			continue
		}
		if contentChanged {
			message["content"] = mustJSON(filteredContent)
			reencoded, err := json.Marshal(message)
			if err != nil {
				return 0, fmt.Errorf("re-encode historical Codex message: %w", err)
			}
			rawMessage = reencoded
		}
		filteredMessages = append(filteredMessages, rawMessage)
	}
	if removed > 0 {
		if len(filteredMessages) == 0 {
			return 0, errors.New("historical Codex bootstrap removal left no request messages")
		}
		request["messages"] = mustJSON(filteredMessages)
	}
	return removed, nil
}

func isHistoricalCodexBootstrapText(text string) bool {
	if isStandaloneCodexBootstrapPart(text) {
		return true
	}
	markers := []string{
		"# AGENTS.md instructions for ",
		"<environment_context>",
		"<permissions instructions>",
		"<collaboration_mode>",
		"<apps_instructions>",
		"<plugins_instructions>",
		"<skills_instructions>",
		"<recommended_plugins>",
		"# Codex desktop context",
	}
	markerCount := 0
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			markerCount++
		}
	}
	hasContextAnchor := strings.Contains(text, "# AGENTS.md instructions for ") ||
		strings.Contains(text, "<environment_context>")
	return hasContextAnchor && markerCount >= 2
}

func stripHistoricalCodexBootstrapText(text string) (string, int64) {
	if isHistoricalCodexBootstrapText(text) {
		return "", 1
	}
	wrappers := [][2]string{
		{"<environment_context>", "</environment_context>"},
		{"<permissions instructions>", "</permissions instructions>"},
		{"<collaboration_mode>", "</collaboration_mode>"},
		{"<apps_instructions>", "</apps_instructions>"},
		{"<plugins_instructions>", "</plugins_instructions>"},
		{"<skills_instructions>", "</skills_instructions>"},
		{"<recommended_plugins>", "</recommended_plugins>"},
		{"<app-context>", "</app-context>"},
		{"<multi_agent_mode>", "</multi_agent_mode>"},
	}
	var removed int64
	for _, wrapper := range wrappers {
		for {
			start := strings.Index(text, wrapper[0])
			if start < 0 {
				break
			}
			relativeEnd := strings.Index(text[start+len(wrapper[0]):], wrapper[1])
			if relativeEnd < 0 {
				break
			}
			end := start + len(wrapper[0]) + relativeEnd + len(wrapper[1])
			prefix := strings.TrimRight(text[:start], "\r\n")
			suffix := strings.TrimLeft(text[end:], "\r\n")
			switch {
			case prefix == "":
				text = suffix
			case suffix == "":
				text = prefix
			default:
				text = prefix + "\n" + suffix
			}
			removed++
		}
	}
	return text, removed
}

func ensureRequestHistoryThinkingSignatures(record *DeliveryRecord) (int64, error) {
	request, err := decodeJSONObject(record.Request, "request")
	if err != nil {
		return 0, err
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return 0, fmt.Errorf("decode request messages: %w", err)
	}
	var completed int64
	for index, rawMessage := range messages {
		var message map[string]json.RawMessage
		if json.Unmarshal(rawMessage, &message) != nil || rawString(message["role"]) != "assistant" {
			continue
		}
		var content []json.RawMessage
		if json.Unmarshal(message["content"], &content) != nil {
			continue
		}
		var unsigned int64
		messageChanged := false
		for blockIndex, rawBlock := range content {
			var block map[string]json.RawMessage
			if json.Unmarshal(rawBlock, &block) != nil || rawString(block["type"]) != "thinking" {
				continue
			}
			signature := rawString(block["signature"])
			if signature == "" {
				unsigned++
				continue
			}
			if validateOpus5SignatureShape(signature, DefaultPublicModel) == nil {
				continue
			}
			thinkingBytes := len(rawString(block["thinking"]))
			block["thinking"] = mustJSON("")
			block["signature"] = mustJSON(deterministicRequestHistorySignature(
				record.SessionID,
				signature,
				DefaultPublicModel,
				thinkingBytes,
			))
			reencoded, err := json.Marshal(block)
			if err != nil {
				return 0, fmt.Errorf("re-encode request history thinking block: %w", err)
			}
			content[blockIndex] = reencoded
			messageChanged = true
			completed++
		}
		if unsigned == 0 && !messageChanged {
			continue
		}
		message["content"] = mustJSON(content)
		if unsigned > 0 {
			responseShape := map[string]json.RawMessage{
				"model":   request["model"],
				"content": message["content"],
			}
			if err := ensureThinkingSignatures(responseShape, false, false, 0); err != nil {
				return 0, err
			}
			message["content"] = responseShape["content"]
			completed += unsigned
		}
		reencoded, err := json.Marshal(message)
		if err != nil {
			return 0, fmt.Errorf("re-encode request assistant message: %w", err)
		}
		messages[index] = reencoded
	}
	if completed == 0 {
		return 0, nil
	}
	request["messages"] = mustJSON(messages)
	encoded, err := json.Marshal(request)
	if err != nil {
		return 0, fmt.Errorf("re-encode request history thinking: %w", err)
	}
	record.Request = encoded
	return completed, nil
}

func addRebuildStats(total *RebuildChangeStats, value RebuildChangeStats) {
	total.Records += value.Records
	total.ChangedRecords += value.ChangedRecords
	total.RequestShapeNormalized += value.RequestShapeNormalized
	total.CodexBootstrapFragmentsRemoved += value.CodexBootstrapFragmentsRemoved
	total.ToolIDsNormalized += value.ToolIDsNormalized
	total.OpenAIContentBlocksNormalized += value.OpenAIContentBlocksNormalized
	total.ResponseThinkingRemoved += value.ResponseThinkingRemoved
	total.ResponseThinkingCompleted += value.ResponseThinkingCompleted
	total.RequestThinkingEchoRepaired += value.RequestThinkingEchoRepaired
	total.RequestHistoryThinkingCompleted += value.RequestHistoryThinkingCompleted
	total.UsageReprojected += value.UsageReprojected
}
