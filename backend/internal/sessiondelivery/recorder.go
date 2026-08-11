package sessiondelivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSpoolMaxBytes   int64 = 4 << 30
	defaultCaptureMaxBytes int64 = 256 << 20
)

type Recorder struct {
	enabled         bool
	captureMaxBytes int64
	spool           *Spool
	canonicalizer   *Canonicalizer
}

func NewRecorder(config RecorderConfig) (*Recorder, error) {
	if !config.Enabled {
		return &Recorder{}, nil
	}
	if strings.TrimSpace(config.HMACSecret) == "" {
		return nil, errors.New("session delivery HMAC secret is required when capture is enabled")
	}
	if config.SpoolMaxBytes <= 0 {
		config.SpoolMaxBytes = defaultSpoolMaxBytes
	}
	if config.CaptureMaxBytes <= 0 {
		config.CaptureMaxBytes = defaultCaptureMaxBytes
	}
	spool, err := NewSpool(config.SpoolDir, config.SpoolMaxBytes)
	if err != nil {
		return nil, err
	}
	aliases, err := NewFileAliasStore(filepath.Join(spool.Dir(), "aliases"), config.HMACSecret)
	if err != nil {
		return nil, err
	}
	ids, err := NewIDGenerator(config.HMACSecret, aliases)
	if err != nil {
		return nil, err
	}
	canonicalizer, err := NewCanonicalizer(config.PublicModel, ids)
	if err != nil {
		return nil, err
	}
	return &Recorder{
		enabled:         true,
		captureMaxBytes: config.CaptureMaxBytes,
		spool:           spool,
		canonicalizer:   canonicalizer,
	}, nil
}

func (r *Recorder) Enabled() bool {
	return r != nil && r.enabled
}

func (r *Recorder) CaptureMaxBytes() int64 {
	if r == nil {
		return 0
	}
	return r.captureMaxBytes
}

func (r *Recorder) NewCaptureFile(kind string) (*os.File, error) {
	if !r.Enabled() {
		return nil, errors.New("session delivery recorder is disabled")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "payload"
	}
	file, err := os.CreateTemp(r.spool.TempDir(), ".capture-"+kind+"-*")
	if err != nil {
		return nil, fmt.Errorf("create capture temp file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("secure capture temp file: %w", err)
	}
	return file, nil
}

func (r *Recorder) Record(input CaptureInput) (string, *Envelope, error) {
	if !r.Enabled() {
		return "", nil, nil
	}
	if int64(len(input.RequestBody)) > r.captureMaxBytes {
		return "", nil, fmt.Errorf("request capture size %d exceeds limit %d", len(input.RequestBody), r.captureMaxBytes)
	}
	if int64(len(input.ResponseBody)) > r.captureMaxBytes {
		return "", nil, fmt.Errorf("response capture size %d exceeds limit %d", len(input.ResponseBody), r.captureMaxBytes)
	}
	input.MaxEventBytes = int(r.captureMaxBytes)
	envelope, err := r.canonicalizer.Build(input)
	if err != nil {
		return "", nil, err
	}
	path, err := r.spool.Write(envelope)
	if err != nil {
		return "", envelope, err
	}
	return path, envelope, nil
}

func (r *Recorder) RecordFiles(input CaptureInput, requestPath, responsePath string) (string, *Envelope, error) {
	defer func() {
		if requestPath != "" {
			_ = os.Remove(requestPath)
		}
		if responsePath != "" {
			_ = os.Remove(responsePath)
		}
	}()
	requestBody, err := readFileAtMost(requestPath, r.captureMaxBytes)
	if err != nil {
		return "", nil, fmt.Errorf("read captured request: %w", err)
	}
	responseBody, err := readFileAtMost(responsePath, r.captureMaxBytes)
	if err != nil {
		return "", nil, fmt.Errorf("read captured response: %w", err)
	}
	input.RequestBody = requestBody
	input.ResponseBody = responseBody
	return r.Record(input)
}

func (r *Recorder) Spool() *Spool {
	if r == nil {
		return nil
	}
	return r.spool
}

func readFileAtMost(path string, limit int64) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("capture file path is empty")
	}
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if stat.Size() > limit {
		return nil, fmt.Errorf("capture file size %d exceeds limit %d", stat.Size(), limit)
	}
	return os.ReadFile(path)
}
