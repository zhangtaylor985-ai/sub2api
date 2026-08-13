package sessiondelivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
)

var ErrSpoolFull = errors.New("session delivery spool is full")

const defaultDecodedEnvelopeMaxBytes int64 = 544 << 20

type Spool struct {
	dir           string
	pendingDir    string
	quarantineDir string
	tmpDir        string
	maxBytes      int64

	mu        sync.Mutex
	usedBytes int64
}

type SpoolStats struct {
	UsedBytes          int64 `json:"used_bytes"`
	MaxBytes           int64 `json:"max_bytes"`
	PendingRecords     int   `json:"pending_records"`
	QuarantinedRecords int   `json:"quarantined_records"`
}

// SpoolDetailedStats contains file metadata only. Session payloads are never
// opened while collecting observability data.
type SpoolDetailedStats struct {
	UsedBytes          int64      `json:"used_bytes"`
	MaxBytes           int64      `json:"max_bytes"`
	UsedPercent        float64    `json:"used_percent"`
	PendingRecords     int        `json:"pending_records"`
	PendingBytes       int64      `json:"pending_bytes"`
	QuarantinedRecords int        `json:"quarantined_records"`
	QuarantinedBytes   int64      `json:"quarantined_bytes"`
	TemporaryFiles     int        `json:"temporary_files"`
	TemporaryBytes     int64      `json:"temporary_bytes"`
	OldestPendingAt    *time.Time `json:"oldest_pending_at,omitempty"`
}

type QuarantineRepairStats struct {
	Scanned           int  `json:"scanned"`
	Candidates        int  `json:"candidates"`
	Repaired          int  `json:"repaired"`
	Skipped           int  `json:"skipped"`
	PendingScanned    int  `json:"pending_scanned"`
	PendingCandidates int  `json:"pending_candidates"`
	PendingStaged     int  `json:"pending_staged"`
	PendingSkipped    int  `json:"pending_skipped"`
	Applied           bool `json:"applied"`
}

func NewSpool(dir string, maxBytes int64) (*Spool, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("session delivery spool directory is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("session delivery spool max bytes must be positive")
	}
	pendingDir := filepath.Join(dir, "pending")
	quarantineDir := filepath.Join(dir, "quarantine")
	tmpDir := filepath.Join(dir, "tmp")
	for _, path := range []string{dir, pendingDir, quarantineDir, tmpDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("create spool directory %s: %w", path, err)
		}
	}

	spool := &Spool{dir: dir, pendingDir: pendingDir, quarantineDir: quarantineDir, tmpDir: tmpDir, maxBytes: maxBytes}
	if err := spool.recount(); err != nil {
		return nil, err
	}
	return spool, nil
}

func (s *Spool) Dir() string {
	return s.dir
}

func (s *Spool) TempDir() string {
	return s.tmpDir
}

func (s *Spool) Usage() (used, max int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usedBytes, s.maxBytes
}

func (s *Spool) Stats() (SpoolStats, error) {
	pending, err := countSpoolRecords(s.pendingDir)
	if err != nil {
		return SpoolStats{}, err
	}
	quarantined, err := countSpoolRecords(s.quarantineDir)
	if err != nil {
		return SpoolStats{}, err
	}
	used, max := s.Usage()
	return SpoolStats{
		UsedBytes:          used,
		MaxBytes:           max,
		PendingRecords:     pending,
		QuarantinedRecords: quarantined,
	}, nil
}

// InspectSpool scans spool file metadata without decoding any Session record.
// It is safe to call while the recorder and forwarder are active.
func InspectSpool(dir string, maxBytes int64) (SpoolDetailedStats, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return SpoolDetailedStats{}, errors.New("session delivery spool directory is required")
	}
	if maxBytes <= 0 {
		return SpoolDetailedStats{}, errors.New("session delivery spool max bytes must be positive")
	}
	stats := SpoolDetailedStats{MaxBytes: maxBytes}
	oldest, err := inspectSpoolDirectory(filepath.Join(dir, "pending"), &stats.PendingRecords, &stats.PendingBytes, true)
	if err != nil {
		return SpoolDetailedStats{}, err
	}
	_, err = inspectSpoolDirectory(filepath.Join(dir, "quarantine"), &stats.QuarantinedRecords, &stats.QuarantinedBytes, false)
	if err != nil {
		return SpoolDetailedStats{}, err
	}
	_, err = inspectSpoolDirectory(filepath.Join(dir, "tmp"), &stats.TemporaryFiles, &stats.TemporaryBytes, false)
	if err != nil {
		return SpoolDetailedStats{}, err
	}
	stats.UsedBytes = stats.PendingBytes + stats.QuarantinedBytes
	stats.UsedPercent = float64(stats.UsedBytes) * 100 / float64(maxBytes)
	stats.OldestPendingAt = oldest
	return stats, nil
}

func inspectSpoolDirectory(directory string, count *int, size *int64, trackOldest bool) (*time.Time, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list spool directory %s: %w", directory, err)
	}
	var oldest *time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.zst") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat spool record: %w", err)
		}
		(*count)++
		*size += info.Size()
		if trackOldest && (oldest == nil || info.ModTime().Before(*oldest)) {
			value := info.ModTime().UTC()
			oldest = &value
		}
	}
	return oldest, nil
}

func (s *Spool) Write(envelope *Envelope) (string, error) {
	if envelope == nil {
		return "", errors.New("cannot spool a nil envelope")
	}
	if envelope.RecordID == "" {
		return "", errors.New("cannot spool envelope without record_id")
	}

	tmp, err := os.CreateTemp(s.tmpDir, ".record-*.json.zst")
	if err != nil {
		return "", fmt.Errorf("create spool temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("secure spool temp file: %w", err)
	}

	encoder, err := zstd.NewWriter(tmp, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("create spool zstd writer: %w", err)
	}
	encodeErr := json.NewEncoder(encoder).Encode(envelope)
	closeEncoderErr := encoder.Close()
	if encodeErr != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("encode spool envelope: %w", encodeErr)
	}
	if closeEncoderErr != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("close spool zstd writer: %w", closeEncoderErr)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync spool temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close spool temp file: %w", err)
	}
	stat, err := os.Stat(tmpName)
	if err != nil {
		return "", fmt.Errorf("stat spool temp file: %w", err)
	}

	filename := fmt.Sprintf("%020d-%s.json.zst", envelope.CapturedAt.UnixNano(), envelope.RecordID)
	destination := filepath.Join(s.pendingDir, filename)

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := os.Stat(destination); err == nil {
		return destination, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check existing spool record: %w", err)
	} else if existing != nil {
		return destination, nil
	}
	if stat.Size() > s.maxBytes || s.usedBytes > s.maxBytes-stat.Size() {
		return "", fmt.Errorf("%w: used=%d incoming=%d max=%d", ErrSpoolFull, s.usedBytes, stat.Size(), s.maxBytes)
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return "", fmt.Errorf("commit spool record: %w", err)
	}
	s.usedBytes += stat.Size()
	if err := syncDirectory(s.pendingDir); err != nil {
		return "", fmt.Errorf("sync spool directory: %w", err)
	}
	return destination, nil
}

// HasCapacity checks the in-memory spool budget before an expensive request or
// response capture starts. Write remains the authoritative final guard because
// concurrent requests may consume the remaining budget after this snapshot.
func (s *Spool) HasCapacity() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usedBytes < s.maxBytes
}

func (s *Spool) ListPending() ([]string, error) {
	entries, err := os.ReadDir(s.pendingDir)
	if err != nil {
		return nil, fmt.Errorf("list pending spool records: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.zst") {
			continue
		}
		paths = append(paths, filepath.Join(s.pendingDir, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *Spool) ReadEnvelope(path string) (*Envelope, error) {
	file, err := s.openPending(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return DecodeCompressedEnvelope(file)
}

func (s *Spool) Quarantine(path, reason string) (string, error) {
	file, err := s.openPending(path)
	if err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	reason = safeArchiveComponent(reason)
	destination := filepath.Join(s.quarantineDir, reason+"-"+filepath.Base(path))
	if err := os.Rename(path, destination); err != nil {
		return "", fmt.Errorf("quarantine spool record: %w", err)
	}
	if err := syncDirectory(s.pendingDir); err != nil {
		return "", err
	}
	if err := syncDirectory(s.quarantineDir); err != nil {
		return "", err
	}
	return destination, nil
}

// RepairMissingSessionIDSpool stages legacy invalid-JSON records that have not
// reached the ingest service yet, then repairs them together with matching
// quarantine records. Callers must stop the forwarder before apply=true.
func (s *Spool) RepairMissingSessionIDSpool(ids *IDGenerator, apply bool) (QuarantineRepairStats, error) {
	if ids == nil {
		return QuarantineRepairStats{}, errors.New("session delivery ID generator is required")
	}
	paths, err := s.ListPending()
	if err != nil {
		return QuarantineRepairStats{}, err
	}
	pendingScanned := 0
	pendingCandidates := 0
	pendingStaged := 0
	pendingSkipped := 0
	for _, path := range paths {
		pendingScanned++
		envelope, readErr := s.ReadEnvelope(path)
		if readErr != nil {
			pendingSkipped++
			continue
		}
		candidate, resolveErr := repairMissingSessionIDEnvelope(ids, envelope)
		if resolveErr != nil {
			return QuarantineRepairStats{}, resolveErr
		}
		if !candidate {
			continue
		}
		pendingCandidates++
		if !apply {
			continue
		}
		if _, quarantineErr := s.Quarantine(path, "invalid_envelope"); quarantineErr != nil {
			return QuarantineRepairStats{}, fmt.Errorf("stage pending Session repair: %w", quarantineErr)
		}
		pendingStaged++
	}
	stats, err := s.RepairMissingSessionIDQuarantine(ids, apply)
	if err != nil {
		return stats, err
	}
	stats.PendingScanned = pendingScanned
	stats.PendingCandidates = pendingCandidates
	stats.PendingStaged = pendingStaged
	stats.PendingSkipped = pendingSkipped
	if !apply {
		stats.Candidates += pendingCandidates
	}
	return stats, nil
}

// RepairMissingSessionIDQuarantine repairs records created by the legacy
// invalid-JSON early-return bug. It never touches other quarantine reasons or
// records that fail the complete storage validator.
func (s *Spool) RepairMissingSessionIDQuarantine(ids *IDGenerator, apply bool) (stats QuarantineRepairStats, err error) {
	if ids == nil {
		return stats, errors.New("session delivery ID generator is required")
	}
	stats.Applied = apply
	entries, err := os.ReadDir(s.quarantineDir)
	if err != nil {
		return stats, fmt.Errorf("list quarantine directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if apply {
		defer func() {
			if recountErr := s.recount(); err == nil && recountErr != nil {
				err = recountErr
			}
		}()
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "invalid_envelope-") || !strings.HasSuffix(entry.Name(), ".json.zst") {
			continue
		}
		stats.Scanned++
		path := filepath.Join(s.quarantineDir, entry.Name())
		file, openErr := os.Open(path)
		if openErr != nil {
			return stats, fmt.Errorf("open quarantined Session record: %w", openErr)
		}
		sourceInfo, statErr := file.Stat()
		envelope, decodeErr := DecodeCompressedEnvelope(file)
		closeErr := file.Close()
		if statErr != nil || decodeErr != nil || closeErr != nil {
			stats.Skipped++
			continue
		}
		candidate, resolveErr := repairMissingSessionIDEnvelope(ids, envelope)
		if resolveErr != nil {
			return stats, resolveErr
		}
		if !candidate {
			stats.Skipped++
			continue
		}
		stats.Candidates++
		if !apply {
			continue
		}
		repairedPath, writeErr := s.Write(envelope)
		if writeErr != nil {
			return stats, fmt.Errorf("rewrite quarantined Session record: %w", writeErr)
		}
		if ownershipErr := preserveFileOwnership(repairedPath, sourceInfo); ownershipErr != nil {
			return stats, fmt.Errorf("preserve repaired Session record ownership: %w", ownershipErr)
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return stats, fmt.Errorf("remove repaired quarantine record: %w", removeErr)
		}
		if syncErr := syncDirectory(s.quarantineDir); syncErr != nil {
			return stats, fmt.Errorf("sync quarantine directory: %w", syncErr)
		}
		stats.Repaired++
	}
	return stats, nil
}

func preserveFileOwnership(path string, source os.FileInfo) error {
	if os.Geteuid() != 0 || source == nil {
		return nil
	}
	stat, ok := source.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Chown(path, int(stat.Uid), int(stat.Gid))
}

func repairMissingSessionIDEnvelope(ids *IDGenerator, envelope *Envelope) (bool, error) {
	if envelope == nil || envelope.SessionID != "" || envelope.Rejection == nil || envelope.Rejection.Code != "invalid_request_json" {
		return false, nil
	}
	sessionID, err := ids.ResolveSession(
		envelope.Source.Protocol,
		envelope.Source.Scope,
		"",
		nil,
		nil,
		envelope.RequestID,
	)
	if err != nil {
		return false, fmt.Errorf("resolve quarantined Session ID: %w", err)
	}
	envelope.SessionID = sessionID
	if err := validateEnvelopeForStorage(envelope); err != nil {
		return false, nil
	}
	return true, nil
}

func (s *Spool) OpenCompressed(path string) (*os.File, error) {
	return s.openPending(path)
}

func (s *Spool) Acknowledge(path string) error {
	file, err := s.openPending(path)
	if err != nil {
		return err
	}
	stat, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return statErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove acknowledged spool record: %w", err)
	}
	s.mu.Lock()
	s.usedBytes -= stat.Size()
	if s.usedBytes < 0 {
		s.usedBytes = 0
	}
	s.mu.Unlock()
	return syncDirectory(s.pendingDir)
}

func (s *Spool) openPending(path string) (*os.File, error) {
	cleaned := filepath.Clean(path)
	relative, err := filepath.Rel(s.pendingDir, cleaned)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		return nil, errors.New("spool path is outside pending directory")
	}
	file, err := os.Open(cleaned)
	if err != nil {
		return nil, fmt.Errorf("open pending spool record: %w", err)
	}
	return file, nil
}

func (s *Spool) recount() error {
	var total int64
	for _, directory := range []string{s.pendingDir, s.quarantineDir} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("list spool directory %s: %w", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.zst") {
				continue
			}
			stat, err := entry.Info()
			if err != nil {
				return fmt.Errorf("stat spool record: %w", err)
			}
			total += stat.Size()
		}
	}
	s.usedBytes = total
	return nil
}

func countSpoolRecords(directory string) (int, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, fmt.Errorf("list spool directory %s: %w", directory, err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json.zst") {
			count++
		}
	}
	return count, nil
}

func DecodeCompressedEnvelope(reader io.Reader) (*Envelope, error) {
	return DecodeCompressedEnvelopeAtMost(reader, defaultDecodedEnvelopeMaxBytes)
}

func DecodeCompressedEnvelopeAtMost(reader io.Reader, maxDecodedBytes int64) (*Envelope, error) {
	if maxDecodedBytes <= 0 {
		return nil, errors.New("decoded envelope byte limit must be positive")
	}
	decoder, err := zstd.NewReader(reader, zstd.WithDecoderMaxMemory(uint64(maxDecodedBytes)))
	if err != nil {
		return nil, fmt.Errorf("open envelope zstd reader: %w", err)
	}
	defer decoder.Close()
	limited := &io.LimitedReader{R: decoder, N: maxDecodedBytes + 1}
	var envelope Envelope
	jsonDecoder := json.NewDecoder(limited)
	if err := jsonDecoder.Decode(&envelope); err != nil {
		if limited.N == 0 {
			return nil, fmt.Errorf("decoded envelope exceeds %d bytes", maxDecodedBytes)
		}
		return nil, fmt.Errorf("decode compressed envelope: %w", err)
	}
	var trailing any
	if err := jsonDecoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("compressed envelope contains multiple JSON values")
		}
		return nil, fmt.Errorf("validate compressed envelope trailing data: %w", err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("decoded envelope exceeds %d bytes", maxDecodedBytes)
	}
	return &envelope, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
