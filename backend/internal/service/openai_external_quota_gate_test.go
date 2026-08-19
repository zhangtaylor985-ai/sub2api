package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type openAIExternalQuotaGateAccountRepoStub struct {
	AccountRepository

	mu           sync.Mutex
	account      *Account
	getErr       error
	extraUpdates []openAIExternalQuotaGateState
	setValues    []bool
	updateErr    error
	setErr       error
}

func (s *openAIExternalQuotaGateAccountRepoStub) GetByID(_ context.Context, _ int64) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account, s.getErr
}

func (s *openAIExternalQuotaGateAccountRepoStub) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state, ok := updates[openAIExternalQuotaGateStateKey].(openAIExternalQuotaGateState); ok {
		s.extraUpdates = append(s.extraUpdates, state)
	}
	if s.account != nil {
		if s.account.Extra == nil {
			s.account.Extra = map[string]any{}
		}
		for key, value := range updates {
			s.account.Extra[key] = value
		}
	}
	return s.updateErr
}

func (s *openAIExternalQuotaGateAccountRepoStub) SetSchedulable(_ context.Context, _ int64, value bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setValues = append(s.setValues, value)
	if s.account != nil {
		s.account.Schedulable = value
	}
	return s.setErr
}

func (s *openAIExternalQuotaGateAccountRepoStub) lastState(t *testing.T) openAIExternalQuotaGateState {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.extraUpdates)
	return s.extraUpdates[len(s.extraUpdates)-1]
}

type openAIExternalQuotaGateUsageRepoStub struct {
	stats     *usagestats.AccountStats
	err       error
	startTime time.Time
}

func (s *openAIExternalQuotaGateUsageRepoStub) GetAccountWindowStats(_ context.Context, _ int64, startTime time.Time) (*usagestats.AccountStats, error) {
	s.startTime = startTime
	return s.stats, s.err
}

type openAIExternalQuotaGateReaderStub struct {
	snapshot *openAIExternalQuotaSnapshot
	err      error
	calls    int
}

func (s *openAIExternalQuotaGateReaderStub) Fetch(_ context.Context, _ *Account) (*openAIExternalQuotaSnapshot, error) {
	s.calls++
	return s.snapshot, s.err
}

func TestOpenAIExternalQuotaHTTPReaderFetch(t *testing.T) {
	var authorization, accountID, originator, method string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		accountID = r.Header.Get("chatgpt-account-id")
		originator = r.Header.Get("Originator")
		method = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_type":"pro",
			"rate_limit":{
				"allowed":true,
				"limit_reached":false,
				"primary_window":{"used_percent":17.5,"limit_window_seconds":604800,"reset_at":1787204531},
				"secondary_window":null
			}
		}`))
	}))
	defer server.Close()

	reader := newOpenAIExternalQuotaHTTPReader(nil)
	reader.endpoint = server.URL
	account := newOpenAIExternalQuotaGateTestAccount(false)
	snapshot, err := reader.Fetch(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, http.MethodGet, method)
	require.Equal(t, "Bearer test-access-token", authorization)
	require.Equal(t, "chatgpt-account", accountID)
	require.Equal(t, "codex_cli_rs", originator)
	require.True(t, snapshot.Allowed)
	require.False(t, snapshot.LimitReached)
	require.Equal(t, 17.5, snapshot.Primary.UsedPercent)
	require.Nil(t, snapshot.Secondary)
}

func TestOpenAIExternalQuotaGateFirstObservationFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	repo := &openAIExternalQuotaGateAccountRepoStub{}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(10, 100)}
	service := newOpenAIExternalQuotaGateTestService(repo, &openAIExternalQuotaGateUsageRepoStub{}, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(true)

	service.evaluateAccount(context.Background(), account)

	require.Equal(t, []bool{false}, repo.setValues)
	require.Equal(t, "baseline_created", repo.lastState(t).Decision)
}

func TestOpenAIExternalQuotaGateExternalDecreaseGrantsLease(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	repo := &openAIExternalQuotaGateAccountRepoStub{}
	usageRepo := &openAIExternalQuotaGateUsageRepoStub{stats: &usagestats.AccountStats{}}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(11.25, 100)}
	service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(false)
	account.Extra[openAIExternalQuotaGateStateKey] = quotaState(observedAt, 10, 100)

	service.evaluateAccount(context.Background(), account)

	state := repo.lastState(t)
	require.Equal(t, []bool{true}, repo.setValues)
	require.Equal(t, observedAt, usageRepo.startTime)
	require.Equal(t, "external_decrease_detected", state.Decision)
	require.Equal(t, 1.25, state.ExternalDeltaPercent)
	require.NotNil(t, state.LeaseUntil)
	require.Equal(t, now.Add(10*time.Minute), *state.LeaseUntil)
}

func TestOpenAIExternalQuotaGateLocalTrafficBlocksExternalSignal(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	repo := &openAIExternalQuotaGateAccountRepoStub{}
	usageRepo := &openAIExternalQuotaGateUsageRepoStub{stats: &usagestats.AccountStats{Requests: 1}}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(15, 100)}
	service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(true)
	account.Extra[openAIExternalQuotaGateStateKey] = quotaState(observedAt, 10, 100)

	service.evaluateAccount(context.Background(), account)

	state := repo.lastState(t)
	require.Equal(t, []bool{false}, repo.setValues)
	require.Equal(t, "local_traffic_detected", state.Decision)
	require.Equal(t, int64(1), state.LocalRequests)
}

func TestOpenAIExternalQuotaGateActiveLeaseDoesNotRenew(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	leaseUntil := now.Add(3 * time.Minute)
	repo := &openAIExternalQuotaGateAccountRepoStub{}
	usageRepo := &openAIExternalQuotaGateUsageRepoStub{stats: &usagestats.AccountStats{Requests: 99}}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(30, 100)}
	service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(true)
	state := quotaState(observedAt, 10, 100)
	state.LeaseUntil = &leaseUntil
	account.Extra[openAIExternalQuotaGateStateKey] = state

	service.evaluateAccount(context.Background(), account)

	updated := repo.lastState(t)
	require.Empty(t, repo.setValues)
	require.Equal(t, "lease_active", updated.Decision)
	require.NotNil(t, updated.LeaseUntil)
	require.Equal(t, leaseUntil, *updated.LeaseUntil)
	require.True(t, usageRepo.startTime.IsZero(), "active leases must not use local traffic to renew")
}

func TestOpenAIExternalQuotaGateInactiveAccountCannotReusePersistedLease(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	leaseUntil := now.Add(3 * time.Minute)
	repo := &openAIExternalQuotaGateAccountRepoStub{}
	usageRepo := &openAIExternalQuotaGateUsageRepoStub{stats: &usagestats.AccountStats{}}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(30, 100)}
	service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(false)
	state := quotaState(observedAt, 10, 100)
	state.LeaseUntil = &leaseUntil
	account.Extra[openAIExternalQuotaGateStateKey] = state

	service.evaluateAccount(context.Background(), account)

	updated := repo.lastState(t)
	require.Empty(t, repo.setValues)
	require.Equal(t, "inactive_lease_discarded", updated.Decision)
	require.Nil(t, updated.LeaseUntil)
	require.True(t, usageRepo.startTime.IsZero())
}

func TestOpenAIExternalQuotaGateExpiredLeaseFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	leaseUntil := now
	repo := &openAIExternalQuotaGateAccountRepoStub{}
	usageRepo := &openAIExternalQuotaGateUsageRepoStub{stats: &usagestats.AccountStats{}}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(30, 100)}
	service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(true)
	state := quotaState(observedAt, 10, 100)
	state.LeaseUntil = &leaseUntil
	account.Extra[openAIExternalQuotaGateStateKey] = state

	service.evaluateAccount(context.Background(), account)

	require.Equal(t, []bool{false}, repo.setValues)
	require.Equal(t, "lease_expired", repo.lastState(t).Decision)
	require.True(t, usageRepo.startTime.IsZero())
}

func TestOpenAIExternalQuotaGateActiveLeaseClosesOnWindowReset(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	leaseUntil := now.Add(3 * time.Minute)
	repo := &openAIExternalQuotaGateAccountRepoStub{}
	usageRepo := &openAIExternalQuotaGateUsageRepoStub{stats: &usagestats.AccountStats{}}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(1, 200)}
	service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(true)
	state := quotaState(observedAt, 10, 100)
	state.LeaseUntil = &leaseUntil
	account.Extra[openAIExternalQuotaGateStateKey] = state

	service.evaluateAccount(context.Background(), account)

	require.Equal(t, []bool{false}, repo.setValues)
	require.Equal(t, "window_changed", repo.lastState(t).Decision)
	require.True(t, usageRepo.startTime.IsZero())
}

func TestOpenAIExternalQuotaGateWindowChangeFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	repo := &openAIExternalQuotaGateAccountRepoStub{}
	usageRepo := &openAIExternalQuotaGateUsageRepoStub{stats: &usagestats.AccountStats{}}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(20, 200)}
	service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(true)
	account.Extra[openAIExternalQuotaGateStateKey] = quotaState(observedAt, 10, 100)

	service.evaluateAccount(context.Background(), account)

	require.Equal(t, []bool{false}, repo.setValues)
	require.Equal(t, "window_changed", repo.lastState(t).Decision)
}

func TestOpenAIExternalQuotaGateUpstreamAndDatabaseFailuresFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)

	t.Run("upstream", func(t *testing.T) {
		repo := &openAIExternalQuotaGateAccountRepoStub{}
		reader := &openAIExternalQuotaGateReaderStub{err: errors.New("secret upstream detail")}
		service := newOpenAIExternalQuotaGateTestService(repo, &openAIExternalQuotaGateUsageRepoStub{}, reader, now)
		account := newOpenAIExternalQuotaGateTestAccount(true)
		account.Extra[openAIExternalQuotaGateStateKey] = quotaState(observedAt, 10, 100)

		service.evaluateAccount(context.Background(), account)

		state := repo.lastState(t)
		require.Equal(t, []bool{false}, repo.setValues)
		require.Equal(t, "upstream_error", state.Decision)
		require.Equal(t, observedAt, *state.ObservedAt, "the last successful baseline must be preserved")
	})

	t.Run("database", func(t *testing.T) {
		repo := &openAIExternalQuotaGateAccountRepoStub{}
		usageRepo := &openAIExternalQuotaGateUsageRepoStub{err: errors.New("database unavailable")}
		reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(20, 100)}
		service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
		account := newOpenAIExternalQuotaGateTestAccount(true)
		account.Extra[openAIExternalQuotaGateStateKey] = quotaState(observedAt, 10, 100)

		service.evaluateAccount(context.Background(), account)

		state := repo.lastState(t)
		require.Equal(t, []bool{false}, repo.setValues)
		require.Equal(t, "local_usage_error", state.Decision)
		require.Equal(t, observedAt, *state.ObservedAt, "the last successful baseline must be preserved")
	})
}

func TestOpenAIExternalQuotaGateStateWriteFailureCannotOpenAccount(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	observedAt := now.Add(-time.Minute)
	repo := &openAIExternalQuotaGateAccountRepoStub{updateErr: errors.New("database unavailable")}
	usageRepo := &openAIExternalQuotaGateUsageRepoStub{stats: &usagestats.AccountStats{}}
	reader := &openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(20, 100)}
	service := newOpenAIExternalQuotaGateTestService(repo, usageRepo, reader, now)
	account := newOpenAIExternalQuotaGateTestAccount(false)
	account.Extra[openAIExternalQuotaGateStateKey] = quotaState(observedAt, 10, 100)

	service.evaluateAccount(context.Background(), account)

	require.Empty(t, repo.setValues)
}

func TestOpenAIExternalQuotaGateConfigureAccountEnablesWithImmediateBaseline(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	account := newOpenAIExternalQuotaGateTestAccount(true)
	repo := &openAIExternalQuotaGateAccountRepoStub{account: account}
	service := newOpenAIExternalQuotaGateTestService(
		repo,
		&openAIExternalQuotaGateUsageRepoStub{},
		&openAIExternalQuotaGateReaderStub{snapshot: quotaSnapshot(12.5, 100)},
		now,
	)

	updated, err := service.ConfigureAccount(context.Background(), account.ID, true)

	require.NoError(t, err)
	require.False(t, updated.Schedulable)
	require.True(t, IsOpenAIExternalQuotaGateEnabled(updated))
	require.Equal(t, "baseline_created", readOpenAIExternalQuotaGateState(updated).Decision)
}

func TestOpenAIExternalQuotaGateConfigureAccountDisablesFailClosed(t *testing.T) {
	account := newOpenAIExternalQuotaGateTestAccount(true)
	account.Extra[openAIExternalQuotaGateEnabledKey] = true
	account.Extra[openAIExternalQuotaGateStateKey] = quotaState(time.Now(), 10, 100)
	repo := &openAIExternalQuotaGateAccountRepoStub{account: account}
	service := newOpenAIExternalQuotaGateTestService(
		repo,
		&openAIExternalQuotaGateUsageRepoStub{},
		&openAIExternalQuotaGateReaderStub{},
		time.Now(),
	)

	updated, err := service.ConfigureAccount(context.Background(), account.ID, false)

	require.NoError(t, err)
	require.False(t, updated.Schedulable)
	require.False(t, IsOpenAIExternalQuotaGateEnabled(updated))
	require.Nil(t, updated.Extra[openAIExternalQuotaGateStateKey])
}

func TestOpenAIExternalQuotaGateRefreshRejectsDisabledAccount(t *testing.T) {
	account := newOpenAIExternalQuotaGateTestAccount(false)
	repo := &openAIExternalQuotaGateAccountRepoStub{account: account}
	service := newOpenAIExternalQuotaGateTestService(
		repo,
		&openAIExternalQuotaGateUsageRepoStub{},
		&openAIExternalQuotaGateReaderStub{},
		time.Now(),
	)

	_, err := service.RefreshAccount(context.Background(), account.ID)

	require.Error(t, err)
}

func newOpenAIExternalQuotaGateTestService(
	repo AccountRepository,
	usageRepo openAIExternalQuotaWindowStatsReader,
	reader openAIExternalQuotaUsageReader,
	now time.Time,
) *OpenAIExternalQuotaGateService {
	service := NewOpenAIExternalQuotaGateService(repo, usageRepo, reader, time.Minute, 10*time.Minute, 1)
	service.now = func() time.Time { return now }
	return service
}

func newOpenAIExternalQuotaGateTestAccount(schedulable bool) *Account {
	return &Account{
		ID:          19,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: schedulable,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "chatgpt-account",
		},
		Extra: map[string]any{},
	}
}

func quotaSnapshot(usedPercent float64, resetAt int64) *openAIExternalQuotaSnapshot {
	return &openAIExternalQuotaSnapshot{
		Allowed:      true,
		LimitReached: false,
		Primary: &openAIExternalQuotaWindow{
			UsedPercent:        usedPercent,
			LimitWindowSeconds: 604800,
			ResetAt:            resetAt,
		},
	}
}

func quotaState(observedAt time.Time, usedPercent float64, resetAt int64) openAIExternalQuotaGateState {
	return openAIExternalQuotaGateState{
		ObservedAt:    &observedAt,
		LastAttemptAt: observedAt,
		Allowed:       true,
		Primary: &openAIExternalQuotaWindow{
			UsedPercent:        usedPercent,
			LimitWindowSeconds: 604800,
			ResetAt:            resetAt,
		},
	}
}
