package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

const (
	openAIExternalQuotaGateEnabledKey = "openai_external_quota_gate_enabled"
	openAIExternalQuotaGateStateKey   = "openai_external_quota_gate_state"

	defaultOpenAIExternalQuotaGateInterval = time.Minute
	defaultOpenAIExternalQuotaGateLease    = 10 * time.Minute
	defaultOpenAIExternalQuotaGateDelta    = 1.0
	defaultOpenAIExternalQuotaGateWorkers  = 4
	openAIExternalQuotaUsageBodyLimit      = int64(1 << 20)
)

var openAIExternalQuotaUsageURL = "https://chatgpt.com/backend-api/wham/usage"

type openAIExternalQuotaWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type openAIExternalQuotaSnapshot struct {
	Allowed      bool                       `json:"allowed"`
	LimitReached bool                       `json:"limit_reached"`
	Primary      *openAIExternalQuotaWindow `json:"primary_window,omitempty"`
	Secondary    *openAIExternalQuotaWindow `json:"secondary_window,omitempty"`
}

type openAIExternalQuotaGateState struct {
	ObservedAt           *time.Time                 `json:"observed_at,omitempty"`
	LastAttemptAt        time.Time                  `json:"last_attempt_at"`
	Allowed              bool                       `json:"allowed"`
	LimitReached         bool                       `json:"limit_reached"`
	Primary              *openAIExternalQuotaWindow `json:"primary_window,omitempty"`
	Secondary            *openAIExternalQuotaWindow `json:"secondary_window,omitempty"`
	LeaseUntil           *time.Time                 `json:"lease_until,omitempty"`
	Decision             string                     `json:"decision"`
	ExternalDeltaPercent float64                    `json:"external_delta_percent,omitempty"`
	LocalRequests        int64                      `json:"local_requests,omitempty"`
}

type openAIExternalQuotaUsageReader interface {
	Fetch(ctx context.Context, account *Account) (*openAIExternalQuotaSnapshot, error)
}

type openAIExternalQuotaTokenProvider interface {
	GetAccessToken(ctx context.Context, account *Account) (string, error)
}

type openAIExternalQuotaWindowStatsReader interface {
	GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error)
}

type openAIExternalQuotaHTTPReader struct {
	tokenProvider openAIExternalQuotaTokenProvider
	endpoint      string
}

func newOpenAIExternalQuotaHTTPReader(tokenProvider openAIExternalQuotaTokenProvider) *openAIExternalQuotaHTTPReader {
	return &openAIExternalQuotaHTTPReader{
		tokenProvider: tokenProvider,
		endpoint:      openAIExternalQuotaUsageURL,
	}
}

type openAIExternalQuotaRawWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
	ResetAt            *int64   `json:"reset_at"`
}

type openAIExternalQuotaRawLimit struct {
	Allowed      *bool                         `json:"allowed"`
	LimitReached *bool                         `json:"limit_reached"`
	Primary      *openAIExternalQuotaRawWindow `json:"primary_window"`
	Secondary    *openAIExternalQuotaRawWindow `json:"secondary_window"`
}

type openAIExternalQuotaRawResponse struct {
	RateLimit *openAIExternalQuotaRawLimit `json:"rate_limit"`
	openAIExternalQuotaRawLimit
}

func (r *openAIExternalQuotaHTTPReader) Fetch(ctx context.Context, account *Account) (*openAIExternalQuotaSnapshot, error) {
	if account == nil || !account.IsOpenAIOAuth() {
		return nil, errors.New("OpenAI OAuth account is required")
	}

	accessToken := strings.TrimSpace(account.GetOpenAIAccessToken())
	if r.tokenProvider != nil {
		var err error
		accessToken, err = r.tokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return nil, errors.New("OpenAI OAuth token is unavailable")
		}
		accessToken = strings.TrimSpace(accessToken)
	}
	if accessToken == "" {
		return nil, errors.New("OpenAI OAuth token is unavailable")
	}
	chatGPTAccountID := strings.TrimSpace(account.GetChatGPTAccountID())
	if chatGPTAccountID == "" {
		return nil, errors.New("ChatGPT account ID is unavailable")
	}

	endpoint := strings.TrimSpace(r.endpoint)
	if endpoint == "" {
		endpoint = openAIExternalQuotaUsageURL
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("build OpenAI quota request")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("Version", openAICodexProbeVersion)
	req.Header.Set("User-Agent", codexCLIUserAgent)
	req.Header.Set("chatgpt-account-id", chatGPTAccountID)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               15 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, errors.New("OpenAI quota proxy configuration is invalid")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("OpenAI quota request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OpenAI quota endpoint returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, openAIExternalQuotaUsageBodyLimit+1))
	if err != nil {
		return nil, errors.New("read OpenAI quota response")
	}
	if int64(len(body)) > openAIExternalQuotaUsageBodyLimit {
		return nil, errors.New("OpenAI quota response is too large")
	}
	var raw openAIExternalQuotaRawResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("decode OpenAI quota response")
	}
	limit := &raw.openAIExternalQuotaRawLimit
	if raw.RateLimit != nil {
		limit = raw.RateLimit
	}
	if limit.Allowed == nil || limit.LimitReached == nil {
		return nil, errors.New("OpenAI quota response is missing availability fields")
	}
	primary, err := convertOpenAIExternalQuotaWindow(limit.Primary)
	if err != nil {
		return nil, fmt.Errorf("invalid primary quota window: %w", err)
	}
	secondary, err := convertOptionalOpenAIExternalQuotaWindow(limit.Secondary)
	if err != nil {
		return nil, fmt.Errorf("invalid secondary quota window: %w", err)
	}
	return &openAIExternalQuotaSnapshot{
		Allowed:      *limit.Allowed,
		LimitReached: *limit.LimitReached,
		Primary:      primary,
		Secondary:    secondary,
	}, nil
}

func convertOpenAIExternalQuotaWindow(raw *openAIExternalQuotaRawWindow) (*openAIExternalQuotaWindow, error) {
	if raw == nil || raw.UsedPercent == nil || raw.LimitWindowSeconds == nil || raw.ResetAt == nil {
		return nil, errors.New("required fields are missing")
	}
	if *raw.UsedPercent < 0 || *raw.UsedPercent > 100 || *raw.LimitWindowSeconds <= 0 || *raw.ResetAt <= 0 {
		return nil, errors.New("field values are outside the valid range")
	}
	return &openAIExternalQuotaWindow{
		UsedPercent:        *raw.UsedPercent,
		LimitWindowSeconds: *raw.LimitWindowSeconds,
		ResetAt:            *raw.ResetAt,
	}, nil
}

func convertOptionalOpenAIExternalQuotaWindow(raw *openAIExternalQuotaRawWindow) (*openAIExternalQuotaWindow, error) {
	if raw == nil {
		return nil, nil
	}
	return convertOpenAIExternalQuotaWindow(raw)
}

type OpenAIExternalQuotaGateService struct {
	accountRepo AccountRepository
	usageRepo   openAIExternalQuotaWindowStatsReader
	reader      openAIExternalQuotaUsageReader

	interval time.Duration
	lease    time.Duration
	minDelta float64
	now      func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func NewOpenAIExternalQuotaGateService(
	accountRepo AccountRepository,
	usageRepo openAIExternalQuotaWindowStatsReader,
	reader openAIExternalQuotaUsageReader,
	interval time.Duration,
	lease time.Duration,
	minDelta float64,
) *OpenAIExternalQuotaGateService {
	if interval <= 0 {
		interval = defaultOpenAIExternalQuotaGateInterval
	}
	if lease <= 0 {
		lease = defaultOpenAIExternalQuotaGateLease
	}
	if minDelta <= 0 {
		minDelta = defaultOpenAIExternalQuotaGateDelta
	}
	return &OpenAIExternalQuotaGateService{
		accountRepo: accountRepo,
		usageRepo:   usageRepo,
		reader:      reader,
		interval:    interval,
		lease:       lease,
		minDelta:    minDelta,
		now:         time.Now,
		stopCh:      make(chan struct{}),
	}
}

func ProvideOpenAIExternalQuotaGateService(
	accountRepo AccountRepository,
	usageRepo UsageLogRepository,
	tokenProvider *OpenAITokenProvider,
) *OpenAIExternalQuotaGateService {
	service := NewOpenAIExternalQuotaGateService(
		accountRepo,
		usageRepo,
		newOpenAIExternalQuotaHTTPReader(tokenProvider),
		defaultOpenAIExternalQuotaGateInterval,
		defaultOpenAIExternalQuotaGateLease,
		defaultOpenAIExternalQuotaGateDelta,
	)
	service.Start()
	return service
}

func (s *OpenAIExternalQuotaGateService) Start() {
	if s == nil || s.accountRepo == nil || s.usageRepo == nil || s.reader == nil {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.run()
	})
}

func (s *OpenAIExternalQuotaGateService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *OpenAIExternalQuotaGateService) run() {
	defer s.wg.Done()
	s.runOnce(context.Background())
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.runOnce(context.Background())
		case <-s.stopCh:
			return
		}
	}
}

func (s *OpenAIExternalQuotaGateService) runOnce(ctx context.Context) {
	accounts, err := s.accountRepo.FindByExtraField(ctx, openAIExternalQuotaGateEnabledKey, true)
	if err != nil {
		slog.Error("openai_external_quota_gate_list_failed", "error", err)
		return
	}
	var wg sync.WaitGroup
	workerSlots := make(chan struct{}, defaultOpenAIExternalQuotaGateWorkers)
	for index := range accounts {
		account := accounts[index]
		wg.Add(1)
		go func() {
			defer wg.Done()
			workerSlots <- struct{}{}
			defer func() { <-workerSlots }()
			s.evaluateAccount(ctx, &account)
		}()
	}
	wg.Wait()
}

func (s *OpenAIExternalQuotaGateService) evaluateAccount(ctx context.Context, account *Account) {
	now := s.now().UTC()
	previous := readOpenAIExternalQuotaGateState(account)

	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.Status != StatusActive {
		s.persistDecision(ctx, account, previous.failureState(now, "invalid_account"), false)
		return
	}

	snapshot, err := s.reader.Fetch(ctx, account)
	if err != nil {
		slog.Warn("openai_external_quota_gate_fetch_failed", "account_id", account.ID, "error", err)
		s.persistDecision(ctx, account, previous.failureState(now, "upstream_error"), false)
		return
	}
	current := stateFromOpenAIExternalQuotaSnapshot(snapshot, now)
	if !snapshot.Allowed || snapshot.LimitReached {
		current.Decision = "upstream_unavailable"
		s.persistDecision(ctx, account, current, false)
		return
	}

	if previous.ObservedAt == nil || previous.Primary == nil {
		current.Decision = "baseline_created"
		s.persistDecision(ctx, account, current, false)
		return
	}
	if !externalQuotaWindowsStable(previous, current) {
		current.Decision = "window_changed"
		s.persistDecision(ctx, account, current, false)
		return
	}

	if previous.LeaseUntil != nil {
		if now.Before(*previous.LeaseUntil) {
			if !account.Schedulable {
				current.Decision = "inactive_lease_discarded"
				s.persistDecision(ctx, account, current, false)
				return
			}
			current.LeaseUntil = previous.LeaseUntil
			current.Decision = "lease_active"
			s.persistDecision(ctx, account, current, true)
			return
		}
		current.Decision = "lease_expired"
		s.persistDecision(ctx, account, current, false)
		return
	}

	stats, err := s.usageRepo.GetAccountWindowStats(ctx, account.ID, *previous.ObservedAt)
	if err != nil {
		slog.Warn("openai_external_quota_gate_local_usage_failed", "account_id", account.ID, "error", err)
		s.persistDecision(ctx, account, previous.failureState(now, "local_usage_error"), false)
		return
	}
	if stats == nil {
		stats = &usagestats.AccountStats{}
	}
	current.LocalRequests = stats.Requests
	if stats.Requests > 0 {
		current.Decision = "local_traffic_detected"
		s.persistDecision(ctx, account, current, false)
		return
	}

	delta, comparable := externalQuotaDelta(previous, current)
	current.ExternalDeltaPercent = delta
	if !comparable {
		current.Decision = "window_changed"
		s.persistDecision(ctx, account, current, false)
		return
	}
	if delta < s.minDelta {
		current.Decision = "no_external_decrease"
		s.persistDecision(ctx, account, current, false)
		return
	}

	leaseUntil := now.Add(s.lease)
	current.LeaseUntil = &leaseUntil
	current.Decision = "external_decrease_detected"
	s.persistDecision(ctx, account, current, true)
}

func externalQuotaWindowsStable(previous, current openAIExternalQuotaGateState) bool {
	if !sameOpenAIExternalQuotaWindow(previous.Primary, current.Primary) {
		return false
	}
	if previous.Secondary == nil || current.Secondary == nil {
		return previous.Secondary == nil && current.Secondary == nil
	}
	return sameOpenAIExternalQuotaWindow(previous.Secondary, current.Secondary)
}

func sameOpenAIExternalQuotaWindow(before, after *openAIExternalQuotaWindow) bool {
	return before != nil && after != nil &&
		before.LimitWindowSeconds == after.LimitWindowSeconds &&
		before.ResetAt == after.ResetAt
}

func (s *OpenAIExternalQuotaGateService) persistDecision(
	ctx context.Context,
	account *Account,
	state openAIExternalQuotaGateState,
	schedulable bool,
) {
	if account == nil {
		return
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		openAIExternalQuotaGateStateKey: state,
	}); err != nil {
		slog.Error("openai_external_quota_gate_state_update_failed", "account_id", account.ID, "error", err)
		if schedulable {
			return
		}
	}
	if account.Schedulable == schedulable {
		return
	}
	if err := s.accountRepo.SetSchedulable(ctx, account.ID, schedulable); err != nil {
		slog.Error("openai_external_quota_gate_schedulable_update_failed", "account_id", account.ID, "schedulable", schedulable, "error", err)
		return
	}
	account.Schedulable = schedulable
}

func readOpenAIExternalQuotaGateState(account *Account) openAIExternalQuotaGateState {
	if account == nil || account.Extra == nil {
		return openAIExternalQuotaGateState{}
	}
	raw, ok := account.Extra[openAIExternalQuotaGateStateKey]
	if !ok || raw == nil {
		return openAIExternalQuotaGateState{}
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return openAIExternalQuotaGateState{}
	}
	var state openAIExternalQuotaGateState
	if err := json.Unmarshal(body, &state); err != nil {
		return openAIExternalQuotaGateState{}
	}
	return state
}

func stateFromOpenAIExternalQuotaSnapshot(snapshot *openAIExternalQuotaSnapshot, now time.Time) openAIExternalQuotaGateState {
	state := openAIExternalQuotaGateState{LastAttemptAt: now}
	if snapshot == nil {
		return state
	}
	state.ObservedAt = timePointer(now)
	state.Allowed = snapshot.Allowed
	state.LimitReached = snapshot.LimitReached
	state.Primary = snapshot.Primary
	state.Secondary = snapshot.Secondary
	return state
}

func (s openAIExternalQuotaGateState) failureState(now time.Time, decision string) openAIExternalQuotaGateState {
	s.LastAttemptAt = now
	s.LeaseUntil = nil
	s.Decision = decision
	s.ExternalDeltaPercent = 0
	s.LocalRequests = 0
	return s
}

func externalQuotaDelta(previous, current openAIExternalQuotaGateState) (float64, bool) {
	maxDelta := 0.0
	comparable := false
	for _, pair := range [][2]*openAIExternalQuotaWindow{
		{previous.Primary, current.Primary},
		{previous.Secondary, current.Secondary},
	} {
		before, after := pair[0], pair[1]
		if before == nil || after == nil ||
			before.LimitWindowSeconds != after.LimitWindowSeconds ||
			before.ResetAt != after.ResetAt {
			continue
		}
		comparable = true
		if delta := after.UsedPercent - before.UsedPercent; delta > maxDelta {
			maxDelta = delta
		}
	}
	return maxDelta, comparable
}

func timePointer(value time.Time) *time.Time {
	return &value
}
