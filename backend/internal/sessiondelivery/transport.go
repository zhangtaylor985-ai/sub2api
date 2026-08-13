package sessiondelivery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	ingestTimestampHeader = "X-Session-Timestamp"
	ingestSHAHeader       = "X-Session-Content-SHA256"
	ingestSignatureHeader = "X-Session-Signature"
	ingestMediaType       = "application/vnd.sub2api.session-envelope+zstd"
	statusSignatureDomain = "v1/status"
)

type ForwarderConfig struct {
	Endpoint    string
	Endpoints   []string
	Secret      string
	BatchLimit  int
	Concurrency int
	Timeout     time.Duration
	Client      *http.Client
}

type ForwardStats struct {
	Attempted   int `json:"attempted"`
	Inserted    int `json:"inserted"`
	Duplicates  int `json:"duplicates"`
	Quarantined int `json:"quarantined"`
	Pending     int `json:"pending"`
}

type Forwarder struct {
	spool       *Spool
	endpoints   []string
	secret      []byte
	batchLimit  int
	concurrency int
	client      *http.Client
}

func NewForwarder(spool *Spool, config ForwarderConfig) (*Forwarder, error) {
	if spool == nil {
		return nil, errors.New("Session forwarder spool is required")
	}
	if len(config.Secret) < minimumHMACSecretBytes {
		return nil, fmt.Errorf("Session ingest secret must be at least %d bytes", minimumHMACSecretBytes)
	}
	rawEndpoints := config.Endpoints
	if len(rawEndpoints) == 0 {
		rawEndpoints = []string{config.Endpoint}
	}
	endpoints := make([]string, 0, len(rawEndpoints))
	seenEndpoints := make(map[string]struct{}, len(rawEndpoints))
	for _, rawEndpoint := range rawEndpoints {
		endpoint, err := normalizeIngestEndpoint(rawEndpoint)
		if err != nil {
			return nil, err
		}
		if _, exists := seenEndpoints[endpoint]; exists {
			continue
		}
		seenEndpoints[endpoint] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	if config.BatchLimit <= 0 {
		config.BatchLimit = 100
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 1
	}
	if config.Timeout <= 0 {
		config.Timeout = 20 * time.Minute
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: config.Timeout}
	}
	return &Forwarder{
		spool:       spool,
		endpoints:   endpoints,
		secret:      []byte(config.Secret),
		batchLimit:  config.BatchLimit,
		concurrency: config.Concurrency,
		client:      client,
	}, nil
}

func (f *Forwarder) ForwardOnce(ctx context.Context) (ForwardStats, error) {
	paths, err := f.spool.ListPending()
	if err != nil {
		return ForwardStats{}, err
	}
	stats := ForwardStats{Pending: len(paths)}
	if len(paths) > f.batchLimit {
		paths = paths[:f.batchLimit]
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	type forwardResult struct {
		status string
		err    error
	}
	jobs := make(chan string, len(paths))
	results := make(chan forwardResult, len(paths))
	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	workerCount := f.concurrency
	if workerCount > len(paths) {
		workerCount = len(paths)
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for workerIndex := range workerCount {
		endpoint := f.endpoints[workerIndex%len(f.endpoints)]
		go func() {
			defer workers.Done()
			for path := range jobs {
				if ctx.Err() != nil {
					return
				}
				status, err := f.forwardOne(ctx, endpoint, path)
				results <- forwardResult{status: status, err: err}
				if err != nil {
					// Stop only this worker. Other in-flight uploads may still
					// complete successfully and must not be canceled because one
					// lane encountered a transient upstream failure.
					return
				}
			}
		}()
	}
	workers.Wait()
	close(results)
	var firstErr error
	for result := range results {
		stats.Attempted++
		if result.err != nil {
			if firstErr == nil || errors.Is(firstErr, context.Canceled) {
				firstErr = result.err
			}
			continue
		}
		switch result.status {
		case "inserted":
			stats.Inserted++
			stats.Pending--
		case "duplicate":
			stats.Duplicates++
			stats.Pending--
		case "quarantined":
			stats.Quarantined++
			stats.Pending--
		}
	}
	if firstErr != nil {
		return stats, firstErr
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	return stats, nil
}

func (f *Forwarder) forwardOne(ctx context.Context, endpoint, path string) (string, error) {
	sha, size, err := fileSHA256(path)
	if err != nil {
		return "", err
	}
	file, err := f.spool.OpenCompressed(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	timestamp := time.Now().UTC().Format(time.RFC3339)
	signature := signIngest(f.secret, timestamp, sha)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, file)
	if err != nil {
		return "", err
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", ingestMediaType)
	request.Header.Set(ingestTimestampHeader, timestamp)
	request.Header.Set(ingestSHAHeader, sha)
	request.Header.Set(ingestSignatureHeader, signature)
	response, err := f.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("send Session spool record: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	var result struct {
		Status string `json:"status"`
		Code   string `json:"code"`
	}
	_ = json.Unmarshal(body, &result)
	switch response.StatusCode {
	case http.StatusCreated:
		if err := f.spool.Acknowledge(path); err != nil {
			return "", err
		}
		return "inserted", nil
	case http.StatusOK:
		if err := f.spool.Acknowledge(path); err != nil {
			return "", err
		}
		return "duplicate", nil
	case http.StatusGone, http.StatusUnprocessableEntity:
		if _, err := f.spool.Quarantine(path, result.Code); err != nil {
			return "", err
		}
		return "quarantined", nil
	case http.StatusConflict:
		return "", fmt.Errorf("Session ingest hour is temporarily frozen: code=%s", result.Code)
	case http.StatusInsufficientStorage, http.StatusServiceUnavailable:
		return "", fmt.Errorf("Session ingest is temporarily unavailable: code=%s", result.Code)
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", errors.New("Session ingest authentication failed")
	default:
		return "", fmt.Errorf("Session ingest returned HTTP %d code=%s", response.StatusCode, sanitizeTransportText(result.Code))
	}
}

func normalizeIngestEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse Session ingest endpoint: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("Session ingest endpoint must use https (or loopback http for local testing)")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return "", errors.New("plaintext Session ingest is allowed only on loopback")
	}
	if parsed.Host == "" {
		return "", errors.New("Session ingest endpoint host is required")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path != "/v1/records" {
		parsed.Path += "/v1/records"
	}
	return parsed.String(), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type IngestHandlerConfig struct {
	Secret              string
	TempDir             string
	MaxBodyBytes        int64
	MaxDecodedBytes     int64
	MaxConcurrent       int
	MaxDecodeConcurrent int
	AllowedSkew         time.Duration
	DiskPath            string
	RejectDiskUsage     int
}

type IngestHandler struct {
	store           *Store
	secret          []byte
	tempDir         string
	maxBodyBytes    int64
	maxDecodedBytes int64
	semaphore       chan struct{}
	decodeSemaphore chan struct{}
	allowedSkew     time.Duration
	diskPath        string
	rejectDiskUsage int
	hostCollector   HostStatusCollector
}

func NewIngestHandler(store *Store, config IngestHandlerConfig) (*IngestHandler, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("Session ingest store is required")
	}
	if len(config.Secret) < minimumHMACSecretBytes {
		return nil, fmt.Errorf("Session ingest secret must be at least %d bytes", minimumHMACSecretBytes)
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 256 << 20
	}
	if config.MaxDecodedBytes <= 0 {
		config.MaxDecodedBytes = defaultDecodedEnvelopeMaxBytes
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 2
	}
	if config.MaxDecodeConcurrent <= 0 {
		config.MaxDecodeConcurrent = 1
	}
	if config.MaxDecodeConcurrent > config.MaxConcurrent {
		config.MaxDecodeConcurrent = config.MaxConcurrent
	}
	if config.AllowedSkew <= 0 {
		config.AllowedSkew = 5 * time.Minute
	}
	if strings.TrimSpace(config.TempDir) == "" {
		config.TempDir = filepath.Join(os.TempDir(), "sub2api-session-ingest")
	}
	if strings.TrimSpace(config.DiskPath) == "" {
		config.DiskPath = config.TempDir
	}
	if config.RejectDiskUsage <= 0 || config.RejectDiskUsage > 100 {
		config.RejectDiskUsage = 75
	}
	if err := os.MkdirAll(config.TempDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Session ingest temp directory: %w", err)
	}
	return &IngestHandler{
		store:           store,
		secret:          []byte(config.Secret),
		tempDir:         config.TempDir,
		maxBodyBytes:    config.MaxBodyBytes,
		maxDecodedBytes: config.MaxDecodedBytes,
		semaphore:       make(chan struct{}, config.MaxConcurrent),
		decodeSemaphore: make(chan struct{}, config.MaxDecodeConcurrent),
		allowedSkew:     config.AllowedSkew,
		diskPath:        config.DiskPath,
		rejectDiskUsage: config.RejectDiskUsage,
		hostCollector:   &LinuxHostStatusCollector{},
	}, nil
}

func (h *IngestHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/health":
		ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
		defer cancel()
		if err := h.store.Ping(ctx); err != nil {
			writeIngestJSON(writer, http.StatusServiceUnavailable, map[string]any{"status": "unavailable"})
			return
		}
		writeIngestJSON(writer, http.StatusOK, map[string]any{"status": "ok"})
		return
	case request.Method == http.MethodGet && request.URL.Path == "/v1/status":
		if !h.authenticateStatusRequest(request) {
			writeIngestJSON(writer, http.StatusUnauthorized, map[string]any{"status": "error", "code": "invalid_signature"})
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
		defer cancel()
		snapshot, err := BuildStatusSnapshot(ctx, h.store, h.hostCollector, h.diskPath, h.rejectDiskUsage)
		if err != nil {
			writeIngestJSON(writer, http.StatusServiceUnavailable, map[string]any{"status": "error", "code": "status_unavailable"})
			return
		}
		writeIngestJSON(writer, http.StatusOK, snapshot)
		return
	case request.Method != http.MethodPost || request.URL.Path != "/v1/records":
		writeIngestJSON(writer, http.StatusNotFound, map[string]any{"status": "error", "code": "not_found"})
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != ingestMediaType {
		writeIngestJSON(writer, http.StatusUnsupportedMediaType, map[string]any{"status": "error", "code": "unsupported_media_type"})
		return
	}
	if usedPercent, err := DiskUsagePercent(h.diskPath); err != nil {
		writeIngestJSON(writer, http.StatusServiceUnavailable, map[string]any{"status": "error", "code": "disk_check_failed"})
		return
	} else if usedPercent >= h.rejectDiskUsage {
		writeIngestJSON(writer, http.StatusInsufficientStorage, map[string]any{"status": "error", "code": "disk_guard"})
		return
	}
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	default:
		writeIngestJSON(writer, http.StatusServiceUnavailable, map[string]any{"status": "error", "code": "ingest_busy"})
		return
	}
	timestamp := request.Header.Get(ingestTimestampHeader)
	contentSHA := strings.ToLower(strings.TrimSpace(request.Header.Get(ingestSHAHeader)))
	signature := strings.TrimSpace(request.Header.Get(ingestSignatureHeader))
	requestTime, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || time.Since(requestTime).Abs() > h.allowedSkew {
		writeIngestJSON(writer, http.StatusUnauthorized, map[string]any{"status": "error", "code": "invalid_timestamp"})
		return
	}
	if len(contentSHA) != sha256.Size*2 {
		writeIngestJSON(writer, http.StatusUnauthorized, map[string]any{"status": "error", "code": "invalid_content_hash"})
		return
	}

	tmp, err := os.CreateTemp(h.tempDir, ".ingest-*.json.zst")
	if err != nil {
		writeIngestJSON(writer, http.StatusInternalServerError, map[string]any{"status": "error", "code": "temp_file_failed"})
		return
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		writeIngestJSON(writer, http.StatusInternalServerError, map[string]any{"status": "error", "code": "temp_file_security_failed"})
		return
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(request.Body, h.maxBodyBytes+1))
	if copyErr != nil {
		_ = tmp.Close()
		writeIngestJSON(writer, http.StatusBadRequest, map[string]any{"status": "error", "code": "body_read_failed"})
		return
	}
	if written > h.maxBodyBytes {
		_ = tmp.Close()
		writeIngestJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{"status": "error", "code": "body_too_large"})
		return
	}
	actualSHA := hex.EncodeToString(hash.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(actualSHA), []byte(contentSHA)) != 1 ||
		subtle.ConstantTimeCompare([]byte(signIngest(h.secret, timestamp, actualSHA)), []byte(signature)) != 1 {
		_ = tmp.Close()
		writeIngestJSON(writer, http.StatusUnauthorized, map[string]any{"status": "error", "code": "invalid_signature"})
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		writeIngestJSON(writer, http.StatusInternalServerError, map[string]any{"status": "error", "code": "temp_seek_failed"})
		return
	}
	select {
	case h.decodeSemaphore <- struct{}{}:
		defer func() { <-h.decodeSemaphore }()
	case <-request.Context().Done():
		return
	}
	envelope, err := DecodeCompressedEnvelopeAtMost(tmp, h.maxDecodedBytes)
	_ = tmp.Close()
	if err != nil {
		writeIngestJSON(writer, http.StatusUnprocessableEntity, map[string]any{"status": "error", "code": "invalid_envelope"})
		return
	}
	inserted, err := h.store.Insert(request.Context(), envelope)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidEnvelope):
			writeIngestJSON(writer, http.StatusUnprocessableEntity, map[string]any{"status": "error", "code": "invalid_envelope"})
		case errors.Is(err, ErrExportHourPurged):
			writeIngestJSON(writer, http.StatusGone, map[string]any{"status": "error", "code": "hour_purged"})
		case errors.Is(err, ErrExportHourFrozen):
			writeIngestJSON(writer, http.StatusConflict, map[string]any{"status": "error", "code": "hour_frozen"})
		default:
			writeIngestJSON(writer, http.StatusInternalServerError, map[string]any{"status": "error", "code": "store_failed"})
		}
		return
	}
	if inserted {
		writeIngestJSON(writer, http.StatusCreated, map[string]any{"status": "inserted", "record_id": envelope.RecordID})
		return
	}
	writeIngestJSON(writer, http.StatusOK, map[string]any{"status": "duplicate", "record_id": envelope.RecordID})
}

func (h *IngestHandler) authenticateStatusRequest(request *http.Request) bool {
	timestamp := strings.TrimSpace(request.Header.Get(ingestTimestampHeader))
	signature := strings.TrimSpace(request.Header.Get(ingestSignatureHeader))
	requestTime, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || time.Since(requestTime).Abs() > h.allowedSkew {
		return false
	}
	expected := signStatus(h.secret, timestamp)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

func DiskUsagePercent(path string) (int, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(strings.TrimSpace(path), &stat); err != nil {
		return 0, fmt.Errorf("stat Session disk usage: %w", err)
	}
	if stat.Blocks == 0 {
		return 0, errors.New("Session disk reports zero blocks")
	}
	usedBlocks := stat.Blocks - stat.Bavail
	return int((usedBlocks*100 + stat.Blocks - 1) / stat.Blocks), nil
}

func signIngest(secret []byte, timestamp, contentSHA string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("v1\n"))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(contentSHA))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func signStatus(secret []byte, timestamp string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(statusSignatureDomain))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(timestamp))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func writeIngestJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func sanitizeTransportText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func parsePositiveInt64(raw string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
