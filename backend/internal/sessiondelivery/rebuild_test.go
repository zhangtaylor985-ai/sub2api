package sessiondelivery

import (
	"archive/tar"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/thinkingsig"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestRebuildArchivesReplaysLatestProjectionAcrossHours(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := t.TempDir()
	firstHour := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	secondHour := firstHour.Add(time.Hour)

	first := historicalRebuildRecord(firstHour.Add(58*time.Minute), 1, 100)
	firstSignature := responseThinkingSignature(t, first)
	second := historicalRebuildRecord(secondHour.Add(time.Minute), 2, 130)
	writeRebuildFixtureArchive(t, inputDir, firstHour, 2, []*DeliveryRecord{first})
	writeRebuildFixtureArchive(t, inputDir, secondHour, 1, []*DeliveryRecord{second})

	_, err := RebuildArchives(t.Context(), RebuildArchivesConfig{
		InputDir: inputDir, OutputDir: outputDir, PublicModel: DefaultPublicModel,
	})
	require.ErrorContains(t, err, "allow-rebuild")

	result, err := RebuildArchives(t.Context(), RebuildArchivesConfig{
		InputDir: inputDir, OutputDir: outputDir, PublicModel: DefaultPublicModel, Allow: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Sessions)
	require.Len(t, result.Archives, 2)
	require.Equal(t, int64(2), result.Changes.Records)
	require.Equal(t, int64(2), result.Changes.RequestShapeNormalized)
	require.Zero(t, result.Changes.CodexBootstrapFragmentsRemoved)
	require.Equal(t, int64(1), result.Changes.ResponseThinkingCompleted)
	require.Equal(t, int64(1), result.Changes.RequestThinkingEchoRepaired)
	require.Zero(t, result.Changes.RequestHistoryThinkingCompleted)
	require.Equal(t, int64(2), result.Changes.UsageReprojected)
	require.Equal(t, int64(2), result.Archives[0].Manifest.ExcludedCount)
	require.Equal(t, int64(1), result.Archives[1].Manifest.ExcludedCount)

	firstRecords := readRebuildDeliveryRecords(t, result.Archives[0].OutputPath)
	secondRecords := readRebuildDeliveryRecords(t, result.Archives[1].OutputPath)
	require.Len(t, firstRecords, 1)
	require.Len(t, secondRecords, 1)
	requireAdaptiveOmitted(t, firstRecords[0].Request)
	requireAdaptiveOmitted(t, secondRecords[0].Request)
	require.NotContains(t, string(firstRecords[0].Request), "budget_tokens")
	require.Equal(t, firstSignature, responseThinkingSignature(t, &firstRecords[0]))
	secondSignature := responseThinkingSignature(t, &secondRecords[0])
	require.NotEmpty(t, secondSignature)
	require.Equal(t, firstSignature, requestAssistantThinkingSignature(t, &secondRecords[0]))

	input, creation, read, output := usageNumbers(t, &firstRecords[0])
	require.Equal(t, 2, input)
	require.Equal(t, 98, creation)
	require.Equal(t, 0, read)
	require.Equal(t, 10, output)
	input, creation, read, output = usageNumbers(t, &secondRecords[0])
	require.Equal(t, 2, input)
	require.Equal(t, 30, creation)
	require.Equal(t, 98, read)
	require.Equal(t, 10, output)

	// Replaying the rebuilt objects is byte-idempotent: generated signatures
	// and repaired history are retained rather than regenerated.
	secondOutputDir := t.TempDir()
	repeated, err := RebuildArchives(t.Context(), RebuildArchivesConfig{
		InputDir: outputDir, OutputDir: secondOutputDir, PublicModel: DefaultPublicModel, Allow: true,
	})
	require.NoError(t, err)
	require.Len(t, repeated.Archives, 2)
	for index := range result.Archives {
		require.Equal(t, result.Archives[index].OutputSHA256, repeated.Archives[index].OutputSHA256)
		require.Equal(t, result.Archives[index].OutputSize, repeated.Archives[index].OutputSize)
	}
	require.Zero(t, repeated.Changes.RequestShapeNormalized)
	require.Zero(t, repeated.Changes.CodexBootstrapFragmentsRemoved)
	require.Zero(t, repeated.Changes.ResponseThinkingCompleted)
	require.Zero(t, repeated.Changes.RequestThinkingEchoRepaired)
	require.Zero(t, repeated.Changes.RequestHistoryThinkingCompleted)
	require.Zero(t, repeated.Changes.UsageReprojected)
	require.Zero(t, repeated.Changes.ChangedRecords)
}

func TestRebuildArchivesRejectsInputAsOutput(t *testing.T) {
	dir := t.TempDir()
	_, err := RebuildArchives(t.Context(), RebuildArchivesConfig{
		InputDir: dir, OutputDir: dir, PublicModel: DefaultPublicModel, Allow: true,
	})
	require.ErrorContains(t, err, "must differ")
}

func TestNormalizeHistoricalDeliveryLeavesDisabledThinkingDisabled(t *testing.T) {
	record := historicalRebuildRecord(time.Now().UTC(), 2, 130)
	record.Request = json.RawMessage(`{
		"model":"claude-opus-5",
		"thinking":{"type":"disabled"},
		"output_config":{"effort":"low"},
		"messages":[{"role":"user","content":[{"type":"text","text":"question"}]}]
	}`)
	requestChanged, bootstrapRemoved, responseCompleted, err := normalizeHistoricalDelivery(record)
	require.NoError(t, err)
	require.False(t, requestChanged)
	require.Zero(t, bootstrapRemoved)
	require.False(t, responseCompleted)
	require.Contains(t, string(record.Request), `"type":"disabled"`)
	require.Empty(t, responseThinkingSignature(t, record))
}

func TestNormalizeHistoricalDeliveryStripsOnlyCodexBootstrapParts(t *testing.T) {
	record := historicalRebuildRecord(time.Now().UTC(), 2, 130)
	record.Request = json.RawMessage(`{
		"model":"claude-opus-5",
		"output_config":{"effort":"high"},
		"messages":[{
			"role":"user",
			"content":[
				{"type":"text","text":"keep the real prompt"},
				{"type":"text","text":"# AGENTS.md instructions for /workspace\nprivate machine context"},
				{"type":"text","text":"<environment_context>\n<cwd>/workspace</cwd>\n</environment_context>"},
				{"type":"text","text":"preserve before\n<environment_context>\n<cwd>/embedded</cwd>\n</environment_context>\npreserve after"}
			]
		}]
	}`)
	requestChanged, bootstrapRemoved, responseCompleted, err := normalizeHistoricalDelivery(record)
	require.NoError(t, err)
	require.True(t, requestChanged)
	require.Equal(t, int64(3), bootstrapRemoved)
	require.True(t, responseCompleted)
	require.Contains(t, string(record.Request), "keep the real prompt")
	require.Contains(t, string(record.Request), "preserve before")
	require.Contains(t, string(record.Request), "preserve after")
	require.NotContains(t, string(record.Request), "AGENTS.md")
	require.NotContains(t, string(record.Request), "environment_context")
	requireAdaptiveOmitted(t, record.Request)
}

func TestEnsureRequestHistoryThinkingSignaturesIsIdempotent(t *testing.T) {
	record := historicalRebuildRecord(time.Now().UTC(), 2, 130)
	record.Request = json.RawMessage(`{
		"model":"claude-opus-5",
		"thinking":{"type":"adaptive","display":"omitted"},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first"}]},
			{"role":"assistant","content":[{"type":"thinking","thinking":"old"},{"type":"text","text":"answer"}]},
			{"role":"user","content":[{"type":"text","text":"second"}]}
		]
	}`)
	completed, err := ensureRequestHistoryThinkingSignatures(record)
	require.NoError(t, err)
	require.Equal(t, int64(1), completed)
	signature := requestAssistantThinkingSignature(t, record)
	require.NotEmpty(t, signature)
	require.NotContains(t, string(record.Request), `"thinking":"old"`)

	completed, err = ensureRequestHistoryThinkingSignatures(record)
	require.NoError(t, err)
	require.Zero(t, completed)
	require.Equal(t, signature, requestAssistantThinkingSignature(t, record))
}

func TestEnsureRequestHistoryThinkingSignaturesUpgradesOpaqueSignatureDeterministically(t *testing.T) {
	request := json.RawMessage(`{
		"model":"claude-opus-5",
		"thinking":{"type":"adaptive","display":"omitted"},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first"}]},
			{"role":"assistant","content":[{"type":"thinking","thinking":"legacy visible reasoning","signature":"opaque-client-history-signature"},{"type":"text","text":"answer"}]},
			{"role":"user","content":[{"type":"text","text":"second"}]}
		]
	}`)
	first := historicalRebuildRecord(time.Now().UTC(), 2, 130)
	first.SessionID = "session_opaque_history"
	first.Request = append(json.RawMessage(nil), request...)
	second := historicalRebuildRecord(time.Now().UTC(), 2, 130)
	second.SessionID = first.SessionID
	second.Request = append(json.RawMessage(nil), request...)

	completed, err := ensureRequestHistoryThinkingSignatures(first)
	require.NoError(t, err)
	require.Equal(t, int64(1), completed)
	completed, err = ensureRequestHistoryThinkingSignatures(second)
	require.NoError(t, err)
	require.Equal(t, int64(1), completed)

	firstSignature := requestAssistantThinkingSignature(t, first)
	require.NotEqual(t, "opaque-client-history-signature", firstSignature)
	require.Equal(t, firstSignature, requestAssistantThinkingSignature(t, second))
	require.NoError(t, validateOpus5SignatureShape(firstSignature, DefaultPublicModel))
	require.NotContains(t, string(first.Request), "legacy visible reasoning")

	completed, err = ensureRequestHistoryThinkingSignatures(first)
	require.NoError(t, err)
	require.Zero(t, completed)
	require.Equal(t, firstSignature, requestAssistantThinkingSignature(t, first))
}

func TestStripHistoricalCodexBootstrapRewritesEmbeddedOnlyBlock(t *testing.T) {
	request := map[string]json.RawMessage{
		"messages": json.RawMessage(`[{
			"role":"user",
			"content":[{"type":"text","text":"before\n<environment_context>\n<cwd>/private</cwd>\n</environment_context>\nafter"}]
		}]`),
	}
	removed, err := stripHistoricalCodexBootstrap(request)
	require.NoError(t, err)
	require.Equal(t, int64(1), removed)
	require.Contains(t, string(request["messages"]), "before")
	require.Contains(t, string(request["messages"]), "after")
	require.NotContains(t, string(request["messages"]), "environment_context")
}

func historicalRebuildRecord(timestamp time.Time, turn, totalInput int) *DeliveryRecord {
	request := `{
		"model":"claude-opus-5",
		"max_tokens":1024,
		"thinking":{"type":"enabled","budget_tokens":16000},
		"output_config":{"effort":"high"},
		"messages":[{"role":"user","content":[{"type":"text","text":"first question"}]}]
	}`
	content := fmt.Sprintf(`[{"type":"thinking","thinking":"","signature":%s},{"type":"text","text":"answer one"}]`,
		mustJSONString(thinkingsig.Generate(DefaultPublicModel, 900)))
	if turn == 2 {
		request = `{
			"model":"claude-opus-5",
			"max_tokens":1024,
			"output_config":{"effort":"low"},
			"messages":[
				{"role":"user","content":[{"type":"text","text":"first question"}]},
				{"role":"assistant","content":[{"type":"text","text":"answer one"}]},
				{"role":"user","content":[{"type":"text","text":"second question"}]}
			]
		}`
		content = `[{"type":"text","text":"answer two"}]`
	}
	response := json.RawMessage(fmt.Sprintf(
		`{"id":"msg_rebuild_%d","type":"message","role":"assistant","model":"claude-opus-5","content":%s,"stop_reason":"end_turn","usage":{"input_tokens":%d,"output_tokens":10}}`,
		turn, content, totalInput,
	))
	return &DeliveryRecord{
		SessionID: "session_rebuild_cross_hour",
		RequestID: fmt.Sprintf("req_rebuild_%d", turn),
		Timestamp: timestamp,
		Metadata:  DeliveryMetadata{HTTPStatus: 200, LatencyMS: 50},
		Request:   json.RawMessage(request),
		Response:  DeliveryResponse{StatusCode: 200, ResponseData: response},
	}
}

func writeRebuildFixtureArchive(
	t *testing.T,
	dir string,
	hour time.Time,
	excluded int64,
	records []*DeliveryRecord,
) string {
	t.Helper()
	hour = hourUTC(hour)
	path := filepath.Join(dir, "session-delivery-"+hour.Format("20060102-15")+"-fixture.tar.zst")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	encoder, err := zstd.NewWriter(file, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	require.NoError(t, err)
	tarWriter := tar.NewWriter(encoder)
	workDir := t.TempDir()
	writer := newSessionEntryWriter(workDir, tarWriter, hour.Add(time.Hour))
	sort.Slice(records, func(i, j int) bool {
		if records[i].SessionID == records[j].SessionID {
			return records[i].Timestamp.Before(records[j].Timestamp)
		}
		return records[i].SessionID < records[j].SessionID
	})
	for _, record := range records {
		require.NoError(t, ValidateDelivery(record, DefaultPublicModel))
		require.NoError(t, writer.write(record))
	}
	files, err := writer.close()
	require.NoError(t, err)
	manifest := ExportManifest{
		FormatVersion: deliveryFormatVersion,
		SchemaVersion: SchemaVersion,
		PublicModel:   DefaultPublicModel,
		ExportDay:     hour.Format("2006-01-02"),
		ExportHour:    hour.Format(time.RFC3339),
		RangeStart:    hour.Format(time.RFC3339),
		RangeEnd:      hour.Add(time.Hour).Format(time.RFC3339),
		RecordCount:   int64(len(records)),
		DeliveryCount: int64(len(records)),
		ExcludedCount: excluded,
		Specification: "vendor-delivery-spec-claude-20260811",
		Files:         files,
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, writeTarBytes(tarWriter, "manifest.json", manifestJSON, hour.Add(time.Hour)))
	require.NoError(t, tarWriter.Close())
	require.NoError(t, encoder.Close())
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())
	_, err = ValidateArchive(path, DefaultPublicModel)
	require.NoError(t, err)
	return path
}

func requireAdaptiveOmitted(t *testing.T, request json.RawMessage) {
	t.Helper()
	var parsed struct {
		Thinking struct {
			Type    string `json:"type"`
			Display string `json:"display"`
		} `json:"thinking"`
	}
	require.NoError(t, json.Unmarshal(request, &parsed))
	require.Equal(t, "adaptive", parsed.Thinking.Type)
	require.Equal(t, "omitted", parsed.Thinking.Display)
}

func responseThinkingSignature(t *testing.T, record *DeliveryRecord) string {
	t.Helper()
	var response struct {
		Content []struct {
			Type      string `json:"type"`
			Signature string `json:"signature"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(record.Response.ResponseData, &response))
	for _, block := range response.Content {
		if block.Type == "thinking" {
			return block.Signature
		}
	}
	return ""
}

func readRebuildDeliveryRecords(t *testing.T, archivePath string) []DeliveryRecord {
	t.Helper()
	file, err := os.Open(archivePath)
	require.NoError(t, err)
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	require.NoError(t, err)
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	var records []DeliveryRecord
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if !strings.HasPrefix(header.Name, "delivery/") {
			continue
		}
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), int(defaultDecodedEnvelopeMaxBytes))
		for scanner.Scan() {
			var record DeliveryRecord
			require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
			records = append(records, record)
		}
		require.NoError(t, scanner.Err())
	}
	return records
}
