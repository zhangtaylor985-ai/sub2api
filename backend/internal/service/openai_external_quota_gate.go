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

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

const (
	openAIExternalQuotaGateEnabledKey = "openai_external_quota_gate_enabled"
	openAIExternalQuotaGateStateKey   = "openai_external_quota_gate_state"

	defaultOpenAIExternalQuotaGateInterval = time.Minute
	defaultOpenAIExternalQuotaGateLease    = time.Hour
	defaultOpenAIExternalQuotaGateQuiet    = 2 * time.Minute
	defaultOpenAIExternalQuotaGateDelta    = 0.01
	defaultOpenAIExternalQuotaGateWorkers  = 4
	defaultOpenAIExternalQuotaGateEvents   = 12
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

type openAIExternalQuotaGateEvent struct {
	OccurredAt           time.Time  `json:"occurred_at"`
	Decision             string     `json:"decision"`
	Schedulable          bool       `json:"schedulable"`
	UsedPercent          *float64   `json:"used_percent,omitempty"`
	ExternalDeltaPercent float64    `json:"external_delta_percent,omitempty"`
	LocalRequests        int64      `json:"local_requests,omitempty"`
	LeaseUntil           *time.Time `json:"lease_until,omitempty"`
}

type openAIExternalQuotaGateState struct {
	ObservedAt           *time.Time                     `json:"observed_at,omitempty"`
	LastAttemptAt        time.Time                      `json:"last_attempt_at"`
	Allowed              bool                           `json:"allowed"`
	LimitReached         bool                           `json:"limit_reached"`
	Primary              *openAIExternalQuotaWindow     `json:"primary_window,omitempty"`
	Secondary            *openAIExternalQuotaWindow     `json:"secondary_window,omitempty"`
	BaselineObservedAt   *time.Time                     `json:"baseline_observed_at,omitempty"`
	BaselinePrimary      *openAIExternalQuotaWindow     `json:"baseline_primary_window,omitempty"`
	BaselineSecondary    *openAIExternalQuotaWindow     `json:"baseline_secondary_window,omitempty"`
	ObservationReadyAt   *time.Time                     `json:"observation_ready_at,omitempty"`
	ExternalDetectedAt   *time.Time                     `json:"external_detected_at,omitempty"`
	LeaseUntil           *time.Time                     `json:"lease_until,omitempty"`
	LastLeaseUntil       *time.Time                     `json:"last_lease_until,omitempty"`
	Decision             string                         `json:"decision"`
	ExternalDeltaPercent float64                        `json:"external_delta_percent,omitempty"`
	LocalRequests        int64                          `json:"local_requests,omitempty"`
	RecentEvents         []openAIExternalQuotaGateEvent `json:"recent_events,omitempty"`
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
	quiet    time.Duration
	minDelta float64
	now      func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
	evalLocks sync.Map
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
		quiet:       defaultOpenAIExternalQuotaGateQuiet,
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
		accountID := accounts[index].ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			workerSlots <- struct{}{}
			defer func() { <-workerSlots }()
			if _, err := s.RefreshAccount(ctx, accountID); err != nil {
				slog.Warn("openai_external_quota_gate_refresh_failed", "account_id", accountID, "error", err)
			}
		}()
	}
	wg.Wait()
}

// ConfigureAccount enables or disables external quota gate management for one OpenAI OAuth account.
// Both transitions fail closed. Enabling also performs an immediate baseline observation.
func (s *OpenAIExternalQuotaGateService) ConfigureAccount(ctx context.Context, accountID int64, enabled bool) (*Account, error) {
	if s == nil || s.accountRepo == nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_EXTERNAL_QUOTA_GATE_UNAVAILABLE", "external quota gate is unavailable")
	}

	lock := s.evaluationLock(accountID)
	lock.Lock()
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	if !account.IsOpenAIOAuth() {
		lock.Unlock()
		return nil, infraerrors.BadRequest("OPENAI_EXTERNAL_QUOTA_GATE_UNSUPPORTED_ACCOUNT", "external quota gate only supports OpenAI OAuth accounts")
	}

	// Close scheduling before changing gate ownership so partial failures remain safe.
	if err := s.accountRepo.SetSchedulable(ctx, accountID, false); err != nil {
		lock.Unlock()
		return nil, err
	}
	if err := s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		openAIExternalQuotaGateEnabledKey: enabled,
		openAIExternalQuotaGateStateKey:   nil,
	}); err != nil {
		lock.Unlock()
		return nil, err
	}
	lock.Unlock()

	if enabled {
		return s.RefreshAccount(ctx, accountID)
	}
	return s.accountRepo.GetByID(ctx, accountID)
}

// RefreshAccount immediately evaluates one gate-enabled account and returns its persisted state.
func (s *OpenAIExternalQuotaGateService) RefreshAccount(ctx context.Context, accountID int64) (*Account, error) {
	if s == nil || s.accountRepo == nil || s.usageRepo == nil || s.reader == nil {
		return nil, infraerrors.ServiceUnavailable("OPENAI_EXTERNAL_QUOTA_GATE_UNAVAILABLE", "external quota gate is unavailable")
	}

	lock := s.evaluationLock(accountID)
	lock.Lock()
	defer lock.Unlock()

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !account.IsOpenAIOAuth() {
		return nil, infraerrors.BadRequest("OPENAI_EXTERNAL_QUOTA_GATE_UNSUPPORTED_ACCOUNT", "external quota gate only supports OpenAI OAuth accounts")
	}
	if !IsOpenAIExternalQuotaGateEnabled(account) {
		return nil, infraerrors.Conflict("OPENAI_EXTERNAL_QUOTA_GATE_DISABLED", "external quota gate is disabled for this account")
	}

	s.evaluateAccountUnlocked(ctx, account)
	return s.accountRepo.GetByID(ctx, accountID)
}

func (s *OpenAIExternalQuotaGateService) evaluationLock(accountID int64) *sync.Mutex {
	lock, _ := s.evalLocks.LoadOrStore(accountID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (s *OpenAIExternalQuotaGateService) evaluateAccount(ctx context.Context, account *Account) {
	if account == nil {
		return
	}
	lock := s.evaluationLock(account.ID)
	lock.Lock()
	defer lock.Unlock()
	s.evaluateAccountUnlocked(ctx, account)
}

func (s *OpenAIExternalQuotaGateService) evaluateAccountUnlocked(ctx context.Context, account *Account) {
	now := s.now().UTC()
	previous := readOpenAIExternalQuotaGateState(account)

	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.Status != StatusActive {
		s.persistDecision(ctx, account, previous.failureState(now, "invalid_account"), false)
		return
	}

	snapshot, err := s.reader.Fetch(ctx, account)
	if err != nil {
		slog.Warn("openai_external_quota_gate_fetch_failed", "account_id", account.ID, "error", err)
		if previous.LeaseUntil != nil && now.Before(*previous.LeaseUntil) && account.Schedulable {
			previous.LastAttemptAt = now
			previous.Decision = "lease_active_upstream_error"
			s.persistDecision(ctx, account, previous, true)
			return
		}
		s.persistDecision(ctx, account, previous.failureState(now, "upstream_error"), false)
		return
	}
	current := stateFromOpenAIExternalQuotaSnapshot(snapshot, now)
	if !snapshot.Allowed || snapshot.LimitReached {
		carryOpenAIExternalQuotaSignal(previous, &current)
		if current.LastLeaseUntil == nil {
			current.LastLeaseUntil = previous.LeaseUntil
		}
		current.Decision = "upstream_unavailable"
		s.persistDecision(ctx, account, current, false)
		return
	}

	if previous.ObservedAt == nil || previous.Primary == nil {
		current = s.beginObservation(previous, current, now, "baseline_created", false)
		s.persistDecision(ctx, account, current, false)
		return
	}
	if !externalQuotaWindowsStable(previous, current) {
		current = s.beginObservation(previous, current, now, "window_changed", false)
		s.persistDecision(ctx, account, current, false)
		return
	}

	if previous.LeaseUntil != nil {
		if now.Before(*previous.LeaseUntil) {
			if !account.Schedulable {
				current = s.beginObservation(previous, current, now, "inactive_lease_discarded", true)
				s.persistDecision(ctx, account, current, false)
				return
			}
			carryOpenAIExternalQuotaSignal(previous, &current)
			current.LeaseUntil = previous.LeaseUntil
			if current.LastLeaseUntil == nil {
				current.LastLeaseUntil = previous.LeaseUntil
			}
			current.Decision = "lease_active"
			s.persistDecision(ctx, account, current, true)
			return
		}
		current = s.beginObservation(previous, current, now, "lease_expired", true)
		s.persistDecision(ctx, account, current, false)
		return
	}

	if account.Schedulable {
		current = s.beginObservation(previous, current, now, "schedulable_without_lease_closed", true)
		s.persistDecision(ctx, account, current, false)
		return
	}

	if previous.BaselineObservedAt == nil || previous.BaselinePrimary == nil {
		current = s.beginObservation(previous, current, now, "baseline_created", previous.ExternalDetectedAt != nil)
		s.persistDecision(ctx, account, current, false)
		return
	}
	carryOpenAIExternalQuotaObservation(previous, &current)
	carryOpenAIExternalQuotaSignal(previous, &current)

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
		current = s.beginObservation(previous, current, now, "local_traffic_detected", true)
		current.LocalRequests = stats.Requests
		s.persistDecision(ctx, account, current, false)
		return
	}

	if previous.ObservationReadyAt != nil && now.Before(*previous.ObservationReadyAt) {
		current.Decision = "observation_cooldown"
		s.persistDecision(ctx, account, current, false)
		return
	}

	delta, comparable := externalQuotaDeltaFromObservation(current)
	if !comparable {
		current = s.beginObservation(previous, current, now, "window_changed", false)
		s.persistDecision(ctx, account, current, false)
		return
	}
	if delta < s.minDelta {
		current.Decision = "observing_external_usage"
		s.persistDecision(ctx, account, current, false)
		return
	}

	detectedAt := now
	leaseUntil := now.Add(s.lease)
	current.ExternalDetectedAt = &detectedAt
	current.LeaseUntil = &leaseUntil
	current.LastLeaseUntil = &leaseUntil
	current.ExternalDeltaPercent = delta
	current.Decision = "external_decrease_detected"
	s.persistDecision(ctx, account, current, true)
}

func (s *OpenAIExternalQuotaGateService) beginObservation(
	previous openAIExternalQuotaGateState,
	current openAIExternalQuotaGateState,
	now time.Time,
	decision string,
	preserveSignal bool,
) openAIExternalQuotaGateState {
	current.Decision = decision
	current.BaselineObservedAt = timePointer(now)
	current.BaselinePrimary = current.Primary
	current.BaselineSecondary = current.Secondary
	readyAt := now.Add(s.quiet)
	current.ObservationReadyAt = &readyAt
	current.LeaseUntil = nil
	if preserveSignal {
		carryOpenAIExternalQuotaSignal(previous, &current)
		if current.LastLeaseUntil == nil {
			current.LastLeaseUntil = previous.LeaseUntil
		}
	}
	return current
}

func carryOpenAIExternalQuotaObservation(previous openAIExternalQuotaGateState, current *openAIExternalQuotaGateState) {
	if current == nil {
		return
	}
	current.BaselineObservedAt = previous.BaselineObservedAt
	current.BaselinePrimary = previous.BaselinePrimary
	current.BaselineSecondary = previous.BaselineSecondary
	current.ObservationReadyAt = previous.ObservationReadyAt
}

func carryOpenAIExternalQuotaSignal(previous openAIExternalQuotaGateState, current *openAIExternalQuotaGateState) {
	if current == nil {
		return
	}
	current.ExternalDetectedAt = previous.ExternalDetectedAt
	current.LastLeaseUntil = previous.LastLeaseUntil
	current.ExternalDeltaPercent = previous.ExternalDeltaPercent
}

// IsOpenAIExternalQuotaGateEnabled reports whether account schedulability is owned by the gate.
func IsOpenAIExternalQuotaGateEnabled(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[openAIExternalQuotaGateEnabledKey].(bool)
	return ok && enabled
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
	previous := readOpenAIExternalQuotaGateState(account)
	state.RecentEvents = append([]openAIExternalQuotaGateEvent(nil), previous.RecentEvents...)
	if previous.Decision != state.Decision || account.Schedulable != schedulable {
		state.RecentEvents = appendOpenAIExternalQuotaGateEvent(state.RecentEvents, state, schedulable)
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

func appendOpenAIExternalQuotaGateEvent(
	events []openAIExternalQuotaGateEvent,
	state openAIExternalQuotaGateState,
	schedulable bool,
) []openAIExternalQuotaGateEvent {
	event := openAIExternalQuotaGateEvent{
		OccurredAt:    state.LastAttemptAt,
		Decision:      state.Decision,
		Schedulable:   schedulable,
		LocalRequests: state.LocalRequests,
	}
	if state.Primary != nil {
		usedPercent := state.Primary.UsedPercent
		event.UsedPercent = &usedPercent
	}
	if state.Decision == "external_decrease_detected" || state.Decision == "lease_active" || state.Decision == "lease_active_upstream_error" || state.Decision == "lease_expired" {
		event.ExternalDeltaPercent = state.ExternalDeltaPercent
		event.LeaseUntil = state.LeaseUntil
		if event.LeaseUntil == nil {
			event.LeaseUntil = state.LastLeaseUntil
		}
	}
	events = append(events, event)
	if len(events) > defaultOpenAIExternalQuotaGateEvents {
		events = events[len(events)-defaultOpenAIExternalQuotaGateEvents:]
	}
	return events
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
	if s.LastLeaseUntil == nil {
		s.LastLeaseUntil = s.LeaseUntil
	}
	s.LeaseUntil = nil
	s.BaselineObservedAt = nil
	s.BaselinePrimary = nil
	s.BaselineSecondary = nil
	s.ObservationReadyAt = nil
	s.Decision = decision
	s.LocalRequests = 0
	return s
}

func externalQuotaDeltaFromObservation(current openAIExternalQuotaGateState) (float64, bool) {
	maxDelta := 0.0
	comparable := false
	for _, pair := range [][2]*openAIExternalQuotaWindow{
		{current.BaselinePrimary, current.Primary},
		{current.BaselineSecondary, current.Secondary},
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
