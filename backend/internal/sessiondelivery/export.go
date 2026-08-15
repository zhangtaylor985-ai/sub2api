package sessiondelivery

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"go.uber.org/zap"
)

const deliveryFormatVersion = "anthropic-messages-jsonl-v1"

type ManifestFile struct {
	Path    string `json:"path"`
	Records int64  `json:"records"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type ExportManifest struct {
	FormatVersion string                `json:"format_version"`
	SchemaVersion int                   `json:"schema_version"`
	PublicModel   string                `json:"public_model"`
	ExportDay     string                `json:"export_day"`
	ExportHour    string                `json:"export_hour"`
	RangeStart    string                `json:"range_start"`
	RangeEnd      string                `json:"range_end"`
	RecordCount   int64                 `json:"record_count"`
	DeliveryCount int64                 `json:"delivery_count"`
	ExcludedCount int64                 `json:"excluded_count"`
	TokenUsage    *DeliveryTokenMetrics `json:"token_usage,omitempty"`
	Specification string                `json:"specification"`
	Files         []ManifestFile        `json:"files"`
}

type ExporterConfig struct {
	PublicModel      string
	TempDir          string
	AllowCurrentHour bool
}

type Exporter struct {
	store            *Store
	backend          ArchiveBackend
	publicModel      string
	tempDir          string
	allowCurrentHour bool
}

type ExportResult struct {
	Hour     time.Time      `json:"hour"`
	Stats    HourStats      `json:"stats"`
	Manifest ExportManifest `json:"manifest"`
	Archive  ArchiveObject  `json:"archive"`
	Durable  bool           `json:"durable"`
}

type ArchiveValidation struct {
	Manifest   ExportManifest       `json:"manifest"`
	Files      []ManifestFile       `json:"files"`
	TokenUsage DeliveryTokenMetrics `json:"token_usage"`
}

func NewExporter(store *Store, backend ArchiveBackend, config ExporterConfig) (*Exporter, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("Session exporter store is required")
	}
	if backend == nil {
		return nil, errors.New("Session archive backend is required")
	}
	publicModel := strings.TrimSpace(config.PublicModel)
	if publicModel == "" {
		publicModel = DefaultPublicModel
	}
	tempDir := strings.TrimSpace(config.TempDir)
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "sub2api-session-export")
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Session export temp directory: %w", err)
	}
	return &Exporter{
		store:            store,
		backend:          backend,
		publicModel:      publicModel,
		tempDir:          tempDir,
		allowCurrentHour: config.AllowCurrentHour,
	}, nil
}

func (e *Exporter) ExportHour(ctx context.Context, hour time.Time) (_ *ExportResult, returnErr error) {
	hour = hourUTC(hour)
	if !e.allowCurrentHour && !hour.Before(hourUTC(time.Now())) {
		return nil, errors.New("refusing to export the current or a future UTC hour")
	}
	unlockProjection, err := e.store.LockProjectionExport(ctx)
	if err != nil {
		return nil, err
	}
	defer unlockProjection()
	if existing, err := e.store.GetExportBatch(ctx, hour); err == nil {
		if existing.Status == "verified" {
			return nil, errors.New("Session hour already has a verified durable archive")
		}
		if existing.Status == "purged" {
			return nil, ErrExportHourPurged
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	attemptID := uuid.NewString()
	if err := e.store.StartExport(ctx, hour, attemptID); err != nil {
		return nil, err
	}
	exportCtx, cancelExport := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	var heartbeatErr error
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-exportCtx.Done():
				return
			case <-ticker.C:
				if exportCtx.Err() != nil {
					return
				}
				if err := e.store.HeartbeatExport(exportCtx, hour, attemptID); err != nil {
					if exportCtx.Err() != nil {
						return
					}
					heartbeatErr = err
					cancelExport()
					return
				}
			}
		}
	}()
	stopHeartbeat := func() error {
		cancelExport()
		<-heartbeatDone
		return heartbeatErr
	}
	defer func() {
		if err := stopHeartbeat(); err != nil && (returnErr == nil || errors.Is(returnErr, context.Canceled)) {
			returnErr = fmt.Errorf("Session export heartbeat failed: %w", err)
		}
		if returnErr != nil {
			failureCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = e.store.MarkExportFailed(failureCtx, hour, attemptID, returnErr)
		}
	}()

	workDir, err := os.MkdirTemp(e.tempDir, ".export-"+hour.Format("20060102-15")+"-*")
	if err != nil {
		return nil, fmt.Errorf("create Session export work directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	stagedArchiveName := "session-delivery-" + hour.Format("20060102-15") + ".tar.zst"
	archivePath := filepath.Join(workDir, stagedArchiveName)

	manifest, stats, checkpoints, err := e.buildArchive(exportCtx, hour, workDir, archivePath)
	if err != nil {
		return nil, err
	}
	validation, err := ValidateArchive(archivePath, e.publicModel)
	if err != nil {
		return nil, fmt.Errorf("validate staged Session archive: %w", err)
	}
	if validation.Manifest.RecordCount != stats.Deliverable ||
		validation.Manifest.DeliveryCount != stats.Deliverable ||
		validation.Manifest.ExcludedCount != stats.Rejected {
		return nil, errors.New("staged Session archive counts do not match database snapshot")
	}
	stagedSHA, _, err := fileSHA256(archivePath)
	if err != nil {
		return nil, fmt.Errorf("hash staged Session archive: %w", err)
	}
	archiveName := fmt.Sprintf("session-delivery-%s-%s.tar.zst", hour.Format("20060102-15"), stagedSHA[:16])

	object, err := e.backend.Put(exportCtx, archiveName, archivePath)
	if err != nil {
		return nil, fmt.Errorf("write Session archive backend: %w", err)
	}
	if object.SHA256 != stagedSHA {
		return nil, errors.New("archive backend returned a checksum different from the staged object")
	}
	if err := e.backend.Verify(exportCtx, object); err != nil {
		return nil, fmt.Errorf("verify Session archive read-back: %w", err)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err := stopHeartbeat(); err != nil {
		return nil, fmt.Errorf("Session export heartbeat failed: %w", err)
	}
	if err := e.store.MarkExportArchived(
		ctx, hour, attemptID, stats, e.backend.Name(), object.Name, object.SHA256, object.Size, manifestJSON, e.backend.Durable(),
		checkpoints,
	); err != nil {
		return nil, err
	}
	return &ExportResult{Hour: hour, Stats: stats, Manifest: manifest, Archive: object, Durable: e.backend.Durable()}, nil
}

func (e *Exporter) buildArchive(
	ctx context.Context,
	hour time.Time,
	workDir string,
	archivePath string,
) (ExportManifest, HourStats, map[string]projectionCheckpoint, error) {
	archiveFile, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ExportManifest{}, HourStats{}, nil, fmt.Errorf("create staged Session archive: %w", err)
	}
	encoder, err := zstd.NewWriter(archiveFile, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		_ = archiveFile.Close()
		return ExportManifest{}, HourStats{}, nil, fmt.Errorf("create archive zstd writer: %w", err)
	}
	tarWriter := tar.NewWriter(encoder)
	closeArchive := func() error {
		if err := tarWriter.Close(); err != nil {
			return err
		}
		if err := encoder.Close(); err != nil {
			return err
		}
		if err := archiveFile.Sync(); err != nil {
			return err
		}
		return archiveFile.Close()
	}

	hour = hourUTC(hour)
	hourEnd := hour.Add(time.Hour)
	manifest := ExportManifest{
		FormatVersion: deliveryFormatVersion,
		SchemaVersion: SchemaVersion,
		PublicModel:   e.publicModel,
		ExportDay:     hour.Format("2006-01-02"),
		ExportHour:    hour.Format(time.RFC3339),
		RangeStart:    hour.Format(time.RFC3339),
		RangeEnd:      hourEnd.Format(time.RFC3339),
		Specification: "vendor-delivery-spec-claude-20260811",
	}
	var stats HourStats
	sessionWriter := newSessionEntryWriter(workDir, tarWriter, hourEnd)
	echo := &echoRepair{}
	usage := &usageProjector{}
	checkpoints := make(map[string]projectionCheckpoint)
	activeSessionID := ""
	checkpointActiveSession := func() {
		if activeSessionID != "" {
			checkpoints[activeSessionID] = checkpointFromProjectors(echo, usage)
		}
	}
	activateSession := func(sessionID string) error {
		if sessionID == activeSessionID {
			return nil
		}
		checkpointActiveSession()
		checkpoint, found, err := e.store.LoadProjectionCheckpoint(ctx, sessionID, hour)
		if err != nil {
			return err
		}
		activeSessionID = sessionID
		if !found {
			checkpoint = projectionCheckpoint{Version: projectionCheckpointVersion}
		}
		echo.restore(sessionID, checkpoint.Echo)
		if err := usage.restore(sessionID, checkpoint.Usage); err != nil {
			return err
		}
		return nil
	}

	iterateErr := e.store.ForEachHourProjection(ctx, hour, func(
		recordID string,
		delivery *DeliveryRecord,
		rejection *Rejection,
	) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		stats.Records++
		if delivery == nil {
			if rejection == nil {
				return fmt.Errorf("record %s has neither delivery nor rejection", recordID)
			}
			stats.Rejected++
			return nil
		}
		if err := activateSession(delivery.SessionID); err != nil {
			return fmt.Errorf("load projection state for record %s: %w", recordID, err)
		}
		// Records may have entered the database before the current capture
		// normalizer was deployed. Apply the same deterministic delivery-only
		// normalization at export time so queued records cannot retain Codex /
		// OpenAI wire artifacts in a newly produced archive.
		request, err := decodeJSONObject(delivery.Request, "request")
		if err != nil {
			return fmt.Errorf("decode delivery request for record %s: %w", recordID, err)
		}
		normalizedRequest, normalizedResponse, fidelityStats, err := normalizeProjectionFidelity(
			delivery.Request,
			delivery.Response.ResponseData,
			fidelityNormalizationOptions{
				CodexProjection:          isLegacyCodexDeliveryRequest(request),
				RemoveSignedWhenDisabled: true,
			},
		)
		if err != nil {
			// The capture path quarantines a record it cannot normalize and
			// carries on. Doing the same here keeps one unconvertible record —
			// a queued record predating this normalizer, say — from costing the
			// whole hour its delivery. The count is reported separately so a
			// systematic conversion failure shows up instead of passing for
			// ordinary rejections.
			logger.FromContext(ctx).Warn(
				"Session delivery record held back by fidelity normalization",
				zap.String("record_id", recordID),
				zap.Error(err),
			)
			stats.Rejected++
			stats.NormalizationFailed++
			return nil
		}
		// The tool surface was converted, but a system prompt naming the tools
		// that conversion removed still instructs the model to call them. The
		// record contradicts itself and no further normalization can reconcile
		// it, so it is held back rather than delivered.
		if fidelityStats.ForeignSystemPromptTools > 0 {
			stats.Rejected++
			stats.ForeignSystemPromptExcluded++
			return nil
		}
		// Assistant prose naming a different model cannot be restated without
		// fabricating what the model said, so it is held back as well.
		if fidelityStats.ForeignModelSelfClaims > 0 {
			stats.Rejected++
			stats.ForeignModelSelfClaimExcluded++
			return nil
		}
		delivery.Request = normalizedRequest
		delivery.Response.ResponseData = normalizedResponse
		// The offline rebuild path also upgrades legacy thinking request shapes
		// and completes delivery-only signatures. Reuse that deterministic step
		// here so a pre-upgrade record cannot block the normal hourly drain.
		if _, _, _, err := normalizeHistoricalDelivery(delivery); err != nil {
			return fmt.Errorf("upgrade historical delivery for record %s: %w", recordID, err)
		}
		// Re-insert thinking-block echoes into later requests of the session
		// before validation, so delivered conversations match real Claude
		// Code multi-turn shape.
		if err := echo.process(delivery); err != nil {
			return fmt.Errorf("echo repair record %s: %w", recordID, err)
		}
		// A capture can begin in the middle of a client conversation. In that
		// case an unsigned historical thinking block has no earlier response in
		// our checkpoint to match. Complete only those still-unmatched blocks
		// with the same delivery-only fallback used by offline rebuilds.
		if _, err := ensureRequestHistoryThinkingSignatures(delivery); err != nil {
			return fmt.Errorf("complete request history thinking for record %s: %w", recordID, err)
		}
		// Project Anthropic-style prompt-cache usage (real CC traffic always
		// shows per-turn cache creation; GPT upstreams never report it).
		if err := usage.process(delivery); err != nil {
			return fmt.Errorf("usage projection record %s: %w", recordID, err)
		}
		// Must be the last mutation: every stage above re-encodes from Go maps,
		// which would re-alphabetize the members again.
		if err := finalizeDeliveryRecord(delivery); err != nil {
			return fmt.Errorf("finalize record %s: %w", recordID, err)
		}
		if err := ValidateDeliveryFidelity(delivery, e.publicModel); err != nil {
			return fmt.Errorf("validate delivery record %s: %w", recordID, err)
		}
		tokens, err := ExtractDeliveryTokenMetrics(delivery)
		if err != nil {
			return fmt.Errorf("extract delivery token metrics for record %s: %w", recordID, err)
		}
		if err := stats.TokenUsage.Add(tokens); err != nil {
			return fmt.Errorf("aggregate delivery token metrics for record %s: %w", recordID, err)
		}
		stats.Deliverable++
		return sessionWriter.write(delivery)
	})
	if iterateErr != nil {
		_ = closeArchive()
		return ExportManifest{}, HourStats{}, nil, iterateErr
	}
	checkpointActiveSession()
	entries, err := sessionWriter.close()
	if err != nil {
		_ = closeArchive()
		return ExportManifest{}, HourStats{}, nil, err
	}
	manifest.Files = append(manifest.Files, entries...)
	manifest.RecordCount = stats.Deliverable
	manifest.DeliveryCount = stats.Deliverable
	manifest.ExcludedCount = stats.Rejected
	manifest.TokenUsage = &stats.TokenUsage

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		_ = closeArchive()
		return ExportManifest{}, HourStats{}, nil, err
	}
	if err := writeTarBytes(tarWriter, "manifest.json", manifestJSON, hourEnd); err != nil {
		_ = closeArchive()
		return ExportManifest{}, HourStats{}, nil, err
	}
	if err := closeArchive(); err != nil {
		return ExportManifest{}, HourStats{}, nil, fmt.Errorf("finalize Session archive: %w", err)
	}
	return manifest, stats, checkpoints, nil
}

type sessionEntryWriter struct {
	workDir   string
	tarWriter *tar.Writer
	modTime   time.Time
	currentID string
	current   *jsonlTempFile
	entries   []ManifestFile
}

func newSessionEntryWriter(workDir string, tarWriter *tar.Writer, modTime time.Time) *sessionEntryWriter {
	return &sessionEntryWriter{workDir: workDir, tarWriter: tarWriter, modTime: modTime}
}

func (w *sessionEntryWriter) write(record *DeliveryRecord) error {
	if w.currentID != record.SessionID {
		if err := w.flush(); err != nil {
			return err
		}
		current, err := newJSONLTempFile(w.workDir, "session")
		if err != nil {
			return err
		}
		w.currentID = record.SessionID
		w.current = current
	}
	return w.current.writeJSON(record)
}

func (w *sessionEntryWriter) close() ([]ManifestFile, error) {
	if err := w.flush(); err != nil {
		return nil, err
	}
	return w.entries, nil
}

func (w *sessionEntryWriter) flush() error {
	if w.current == nil {
		return nil
	}
	defer w.current.cleanup()
	path := "delivery/" + safeArchiveComponent(w.currentID) + ".jsonl"
	entry, err := w.current.appendToTar(w.tarWriter, path, w.modTime)
	if err != nil {
		return err
	}
	w.entries = append(w.entries, entry)
	w.current = nil
	w.currentID = ""
	return nil
}

type jsonlTempFile struct {
	file    *os.File
	path    string
	hash    hash.Hash
	bytes   int64
	records int64
}

func newJSONLTempFile(workDir, prefix string) (*jsonlTempFile, error) {
	file, err := os.CreateTemp(workDir, "."+prefix+"-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("create JSONL temp file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return &jsonlTempFile{file: file, path: file.Name(), hash: sha256.New()}, nil
}

func (f *jsonlTempFile) writeJSON(value any) error {
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// Every record and manifest entry funnels through here, which makes it the
	// one place where the Go serializer's HTML escaping can be undone for the
	// whole archive at once.
	line = unescapeJSONHTML(line)
	if bytes.Contains(line, []byte{'\n'}) {
		return errors.New("encoded JSONL record contains a literal newline")
	}
	line = append(line, '\n')
	written, err := f.file.Write(line)
	if err != nil {
		return err
	}
	if written != len(line) {
		return io.ErrShortWrite
	}
	_, _ = f.hash.Write(line)
	f.bytes += int64(written)
	f.records++
	return nil
}

func (f *jsonlTempFile) appendToTar(writer *tar.Writer, path string, modTime time.Time) (ManifestFile, error) {
	if err := f.file.Sync(); err != nil {
		return ManifestFile{}, err
	}
	if _, err := f.file.Seek(0, io.SeekStart); err != nil {
		return ManifestFile{}, err
	}
	if err := writer.WriteHeader(&tar.Header{
		Name:     path,
		Mode:     0o600,
		Size:     f.bytes,
		ModTime:  modTime,
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}); err != nil {
		return ManifestFile{}, err
	}
	if _, err := io.CopyN(writer, f.file, f.bytes); err != nil {
		return ManifestFile{}, err
	}
	return ManifestFile{Path: path, Records: f.records, Bytes: f.bytes, SHA256: hex.EncodeToString(f.hash.Sum(nil))}, nil
}

func (f *jsonlTempFile) cleanup() {
	if f == nil {
		return
	}
	if f.file != nil {
		_ = f.file.Close()
	}
	if f.path != "" {
		_ = os.Remove(f.path)
	}
}

func writeTarBytes(writer *tar.Writer, path string, content []byte, modTime time.Time) error {
	if err := writer.WriteHeader(&tar.Header{
		Name:     path,
		Mode:     0o600,
		Size:     int64(len(content)),
		ModTime:  modTime,
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}); err != nil {
		return err
	}
	_, err := writer.Write(content)
	return err
}

func safeArchiveComponent(value string) string {
	if value != "" {
		valid := true
		for _, r := range value {
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.') {
				valid = false
				break
			}
		}
		if valid && value != "." && value != ".." {
			return value
		}
	}
	digest := sha256.Sum256([]byte(value))
	return "session_" + hex.EncodeToString(digest[:16])
}

func ValidateArchive(path, publicModel string) (*ArchiveValidation, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Session archive: %w", err)
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open Session archive zstd stream: %w", err)
	}
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	var manifest ExportManifest
	manifestFound := false
	var files []ManifestFile
	var deliveryCount int64
	var tokenUsage DeliveryTokenMetrics
	seenPaths := make(map[string]struct{})
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read Session archive entry: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unsupported archive entry type for %s", header.Name)
		}
		if !safeTarPath(header.Name) {
			return nil, fmt.Errorf("unsafe archive entry path %q", header.Name)
		}
		if _, exists := seenPaths[header.Name]; exists {
			return nil, fmt.Errorf("archive contains duplicate entry %q", header.Name)
		}
		seenPaths[header.Name] = struct{}{}
		if header.Name == "manifest.json" {
			if manifestFound {
				return nil, errors.New("archive contains multiple manifests")
			}
			if header.Size < 0 || header.Size > 256<<20 {
				return nil, errors.New("Session archive manifest exceeds validation limit")
			}
			manifestBytes, err := io.ReadAll(io.LimitReader(reader, header.Size))
			if err != nil {
				return nil, fmt.Errorf("read Session archive manifest: %w", err)
			}
			if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
				return nil, fmt.Errorf("decode Session archive manifest: %w", err)
			}
			manifestFound = true
			continue
		}
		entry, delivered, entryTokens, err := validateJSONLEntry(reader, header, publicModel)
		if err != nil {
			return nil, fmt.Errorf("validate %s: %w", header.Name, err)
		}
		files = append(files, entry)
		deliveryCount += delivered
		if err := tokenUsage.Add(entryTokens); err != nil {
			return nil, fmt.Errorf("aggregate archive token metrics: %w", err)
		}
	}
	if !manifestFound {
		return nil, errors.New("Session archive is missing manifest.json")
	}
	if manifest.FormatVersion != deliveryFormatVersion || manifest.SchemaVersion != SchemaVersion {
		return nil, errors.New("Session archive manifest version mismatch")
	}
	if manifest.PublicModel != publicModel {
		return nil, fmt.Errorf("Session archive public model mismatch: %s", manifest.PublicModel)
	}
	manifestHour, err := time.Parse(time.RFC3339, manifest.ExportHour)
	if err != nil || manifest.ExportDay != manifestHour.UTC().Format("2006-01-02") || manifest.RangeStart != manifestHour.UTC().Format(time.RFC3339) || manifest.RangeEnd != manifestHour.UTC().Add(time.Hour).Format(time.RFC3339) {
		return nil, errors.New("Session archive manifest UTC range is invalid")
	}
	if manifest.Specification != "vendor-delivery-spec-claude-20260811" {
		return nil, errors.New("Session archive manifest specification mismatch")
	}
	if manifest.DeliveryCount != deliveryCount || manifest.RecordCount != deliveryCount || manifest.ExcludedCount < 0 {
		return nil, errors.New("Session archive manifest counts do not match JSONL entries")
	}
	if err := compareManifestFiles(manifest.Files, files); err != nil {
		return nil, err
	}
	if manifest.TokenUsage != nil {
		if err := manifest.TokenUsage.Validate(); err != nil {
			return nil, fmt.Errorf("Session archive manifest token metrics: %w", err)
		}
		if manifest.TokenUsage.CountedDeliveries != deliveryCount || *manifest.TokenUsage != tokenUsage {
			return nil, errors.New("Session archive manifest token metrics do not match JSONL entries")
		}
	}
	return &ArchiveValidation{Manifest: manifest, Files: files, TokenUsage: tokenUsage}, nil
}

func validateJSONLEntry(reader io.Reader, header *tar.Header, publicModel string) (ManifestFile, int64, DeliveryTokenMetrics, error) {
	if !strings.HasPrefix(header.Name, "delivery/") || !strings.HasSuffix(header.Name, ".jsonl") {
		return ManifestFile{}, 0, DeliveryTokenMetrics{}, errors.New("unknown JSONL archive path")
	}
	hash := sha256.New()
	limited := io.LimitReader(reader, header.Size)
	scanner := bufio.NewScanner(io.TeeReader(limited, hash))
	scanner.Buffer(make([]byte, 64<<10), int(defaultDecodedEnvelopeMaxBytes))
	var records int64
	var tokenUsage DeliveryTokenMetrics
	expectedSessionComponent := strings.TrimSuffix(strings.TrimPrefix(header.Name, "delivery/"), ".jsonl")
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 {
			return ManifestFile{}, 0, DeliveryTokenMetrics{}, errors.New("JSONL contains an empty line")
		}
		records++
		if err := ValidateDeliveryJSON(line, publicModel); err != nil {
			return ManifestFile{}, 0, DeliveryTokenMetrics{}, err
		}
		var record DeliveryRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return ManifestFile{}, 0, DeliveryTokenMetrics{}, err
		}
		if safeArchiveComponent(record.SessionID) != expectedSessionComponent {
			return ManifestFile{}, 0, DeliveryTokenMetrics{}, errors.New("delivery JSONL path does not match record session_id")
		}
		tokens, err := ExtractDeliveryTokenMetrics(&record)
		if err != nil {
			return ManifestFile{}, 0, DeliveryTokenMetrics{}, err
		}
		if err := tokenUsage.Add(tokens); err != nil {
			return ManifestFile{}, 0, DeliveryTokenMetrics{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return ManifestFile{}, 0, DeliveryTokenMetrics{}, err
	}
	if records == 0 {
		return ManifestFile{}, 0, DeliveryTokenMetrics{}, errors.New("delivery JSONL file is empty")
	}
	return ManifestFile{
		Path:    header.Name,
		Records: records,
		Bytes:   header.Size,
		SHA256:  hex.EncodeToString(hash.Sum(nil)),
	}, records, tokenUsage, nil
}

func compareManifestFiles(expected, actual []ManifestFile) error {
	expectedCopy := append([]ManifestFile(nil), expected...)
	actualCopy := append([]ManifestFile(nil), actual...)
	sort.Slice(expectedCopy, func(i, j int) bool { return expectedCopy[i].Path < expectedCopy[j].Path })
	sort.Slice(actualCopy, func(i, j int) bool { return actualCopy[i].Path < actualCopy[j].Path })
	if len(expectedCopy) != len(actualCopy) {
		return errors.New("Session archive manifest file count mismatch")
	}
	for index := range expectedCopy {
		if expectedCopy[index] != actualCopy[index] {
			return fmt.Errorf("Session archive file manifest mismatch for %s", expectedCopy[index].Path)
		}
	}
	return nil
}

func safeTarPath(path string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	return path != "" && cleaned == path && cleaned != "." && !strings.HasPrefix(cleaned, "../") && !strings.HasPrefix(cleaned, "/")
}
