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
	"time"
)

const (
	ingestTimestampHeader = "X-Session-Timestamp"
	ingestSHAHeader       = "X-Session-Content-SHA256"
	ingestSignatureHeader = "X-Session-Signature"
	ingestMediaType       = "application/vnd.sub2api.session-envelope+zstd"
)

type ForwarderConfig struct {
	Endpoint   string
	Secret     string
	BatchLimit int
	Timeout    time.Duration
	Client     *http.Client
}

type ForwardStats struct {
	Attempted   int `json:"attempted"`
	Inserted    int `json:"inserted"`
	Duplicates  int `json:"duplicates"`
	Quarantined int `json:"quarantined"`
	Pending     int `json:"pending"`
}

type Forwarder struct {
	spool      *Spool
	endpoint   string
	secret     []byte
	batchLimit int
	client     *http.Client
}

func NewForwarder(spool *Spool, config ForwarderConfig) (*Forwarder, error) {
	if spool == nil {
		return nil, errors.New("Session forwarder spool is required")
	}
	if len(config.Secret) < minimumHMACSecretBytes {
		return nil, fmt.Errorf("Session ingest secret must be at least %d bytes", minimumHMACSecretBytes)
	}
	endpoint, err := normalizeIngestEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.BatchLimit <= 0 {
		config.BatchLimit = 100
	}
	if config.Timeout <= 0 {
		config.Timeout = 20 * time.Minute
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: config.Timeout}
	}
	return &Forwarder{
		spool:      spool,
		endpoint:   endpoint,
		secret:     []byte(config.Secret),
		batchLimit: config.BatchLimit,
		client:     client,
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
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		stats.Attempted++
		result, err := f.forwardOne(ctx, path)
		if err != nil {
			return stats, err
		}
		switch result {
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
	return stats, nil
}

func (f *Forwarder) forwardOne(ctx context.Context, path string) (string, error) {
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, file)
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
		return "", fmt.Errorf("Session ingest day is temporarily frozen: code=%s", result.Code)
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
	Secret          string
	TempDir         string
	MaxBodyBytes    int64
	MaxDecodedBytes int64
	MaxConcurrent   int
	AllowedSkew     time.Duration
}

type IngestHandler struct {
	store           *Store
	secret          []byte
	tempDir         string
	maxBodyBytes    int64
	maxDecodedBytes int64
	semaphore       chan struct{}
	allowedSkew     time.Duration
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
	if config.AllowedSkew <= 0 {
		config.AllowedSkew = 5 * time.Minute
	}
	if strings.TrimSpace(config.TempDir) == "" {
		config.TempDir = filepath.Join(os.TempDir(), "sub2api-session-ingest")
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
		allowedSkew:     config.AllowedSkew,
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
	case request.Method != http.MethodPost || request.URL.Path != "/v1/records":
		writeIngestJSON(writer, http.StatusNotFound, map[string]any{"status": "error", "code": "not_found"})
		return
	}
	if mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]); mediaType != ingestMediaType {
		writeIngestJSON(writer, http.StatusUnsupportedMediaType, map[string]any{"status": "error", "code": "unsupported_media_type"})
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
		case errors.Is(err, ErrExportDayPurged):
			writeIngestJSON(writer, http.StatusGone, map[string]any{"status": "error", "code": "day_purged"})
		case errors.Is(err, ErrExportDayFrozen):
			writeIngestJSON(writer, http.StatusConflict, map[string]any{"status": "error", "code": "day_frozen"})
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

func signIngest(secret []byte, timestamp, contentSHA string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("v1\n"))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(contentSHA))
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
