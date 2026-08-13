package sessiondelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type StatusClientConfig struct {
	Endpoint string
	Secret   string
	Timeout  time.Duration
	Client   *http.Client
}

type StatusClient struct {
	endpoint string
	secret   []byte
	client   *http.Client
	now      func() time.Time
}

func NewStatusClient(config StatusClientConfig) (*StatusClient, error) {
	if len(config.Secret) < minimumHMACSecretBytes {
		return nil, fmt.Errorf("Session status secret must be at least %d bytes", minimumHMACSecretBytes)
	}
	endpoint, err := normalizeStatusEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Second
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: config.Timeout}
	}
	return &StatusClient{
		endpoint: endpoint,
		secret:   []byte(config.Secret),
		client:   client,
		now:      time.Now,
	}, nil
}

func (c *StatusClient) Fetch(ctx context.Context) (StatusSnapshot, error) {
	if c == nil || c.client == nil {
		return StatusSnapshot{}, errors.New("Session status client is not initialized")
	}
	timestamp := c.now().UTC().Format(time.RFC3339)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return StatusSnapshot{}, fmt.Errorf("create Session status request: %w", err)
	}
	request.Header.Set(ingestTimestampHeader, timestamp)
	request.Header.Set(ingestSignatureHeader, signStatus(c.secret, timestamp))
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return StatusSnapshot{}, fmt.Errorf("fetch Session status: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return StatusSnapshot{}, fmt.Errorf("read Session status: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return StatusSnapshot{}, fmt.Errorf("Session status returned HTTP %d", response.StatusCode)
	}
	var snapshot StatusSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return StatusSnapshot{}, fmt.Errorf("decode Session status: %w", err)
	}
	if snapshot.ObservedAt.IsZero() || strings.TrimSpace(snapshot.Status) == "" {
		return StatusSnapshot{}, errors.New("Session status response is incomplete")
	}
	return snapshot, nil
}

func normalizeStatusEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse Session status endpoint: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("Session status endpoint must use https (or loopback http for local testing)")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return "", errors.New("plaintext Session status is allowed only on loopback")
	}
	if parsed.Host == "" {
		return "", errors.New("Session status endpoint host is required")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "/v1/records" {
		parsed.Path = ""
	}
	if parsed.Path != "/v1/status" {
		parsed.Path += "/v1/status"
	}
	return parsed.String(), nil
}
