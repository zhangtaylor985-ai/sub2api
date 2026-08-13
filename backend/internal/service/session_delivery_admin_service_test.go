package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
	"github.com/stretchr/testify/require"
)

type sessionCapturePolicyRepoStub struct {
	mu       sync.Mutex
	mode     SessionCaptureMode
	policies map[int64]SessionCaptureKeyPolicy
	keys     []SessionCaptureAPIKey
	updated  time.Time
}

func (r *sessionCapturePolicyRepoStub) LoadSessionCapturePolicy(context.Context) (*SessionCapturePolicySnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	policies := make(map[int64]SessionCaptureKeyPolicy, len(r.policies))
	for id, policy := range r.policies {
		policies[id] = policy
	}
	return &SessionCapturePolicySnapshot{Mode: r.mode, Policies: policies, UpdatedAt: r.updated}, nil
}

func (r *sessionCapturePolicyRepoStub) ListSessionCaptureAPIKeys(_ context.Context, _ string, page, pageSize int) ([]SessionCaptureAPIKey, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := (page - 1) * pageSize
	if start >= len(r.keys) {
		return []SessionCaptureAPIKey{}, int64(len(r.keys)), nil
	}
	end := start + pageSize
	if end > len(r.keys) {
		end = len(r.keys)
	}
	items := append([]SessionCaptureAPIKey(nil), r.keys[start:end]...)
	for index := range items {
		items[index].Policy = r.policies[items[index].ID]
		if items[index].Policy == "" {
			items[index].Policy = SessionCaptureKeyPolicyInherit
		}
	}
	return items, int64(len(r.keys)), nil
}

func (r *sessionCapturePolicyRepoStub) UpdateSessionCaptureMode(_ context.Context, mode SessionCaptureMode, _ int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode = mode
	r.updated = time.Now().UTC()
	return nil
}

func (r *sessionCapturePolicyRepoStub) UpdateSessionCaptureAPIKey(_ context.Context, id int64, policy SessionCaptureKeyPolicy, _ int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if policy == SessionCaptureKeyPolicyInherit {
		delete(r.policies, id)
	} else {
		r.policies[id] = policy
	}
	r.updated = time.Now().UTC()
	return nil
}

func (r *sessionCapturePolicyRepoStub) SetOnlySessionCaptureAPIKey(_ context.Context, id, _ int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode = SessionCaptureModeSelected
	r.policies = map[int64]SessionCaptureKeyPolicy{id: SessionCaptureKeyPolicyInclude}
	r.updated = time.Now().UTC()
	return nil
}

func TestSessionCapturePolicyPrecedenceAndExclusiveAction(t *testing.T) {
	repo := &sessionCapturePolicyRepoStub{
		mode:     SessionCaptureModeAll,
		policies: map[int64]SessionCaptureKeyPolicy{2: SessionCaptureKeyPolicyExclude, 3: SessionCaptureKeyPolicyInclude},
		keys: []SessionCaptureAPIKey{
			{ID: 1, Name: "inherit"}, {ID: 2, Name: "excluded"}, {ID: 3, Name: "included"},
		},
		updated: time.Now().UTC(),
	}
	service, err := NewSessionDeliveryAdminService(repo, sessionDeliveryTestConfig(t))
	require.NoError(t, err)
	require.True(t, service.ShouldCapture(1))
	require.False(t, service.ShouldCapture(2))
	require.True(t, service.ShouldCapture(3))

	require.NoError(t, service.UpdateMode(context.Background(), SessionCaptureModeSelected, 9))
	require.False(t, service.ShouldCapture(1))
	require.False(t, service.ShouldCapture(2))
	require.True(t, service.ShouldCapture(3))

	require.NoError(t, service.UpdateMode(context.Background(), SessionCaptureModeDisabled, 9))
	require.False(t, service.ShouldCapture(3), "global disabled must override explicit include")

	require.NoError(t, service.SetOnlyAPIKey(context.Background(), 2, 9))
	require.False(t, service.ShouldCapture(1))
	require.True(t, service.ShouldCapture(2))
	require.False(t, service.ShouldCapture(3))
	summary, page, err := service.GetPolicy(context.Background(), "", 1, 20)
	require.NoError(t, err)
	require.Equal(t, SessionCaptureModeSelected, summary.Mode)
	require.Equal(t, int64(1), summary.EffectiveAPIKeys)
	require.Equal(t, int64(3), page.Total)
}

func TestSessionDeliveryOverviewCombinesSpoolAndSignedRemoteStatus(t *testing.T) {
	secret := strings.Repeat("s", 32)
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/v1/status", request.URL.Path)
		require.NotEmpty(t, request.Header.Get("X-Session-Timestamp"))
		require.NotEmpty(t, request.Header.Get("X-Session-Signature"))
		_ = json.NewEncoder(writer).Encode(sessiondelivery.StatusSnapshot{
			Status:     "healthy",
			ObservedAt: time.Now().UTC(),
			Warnings:   []string{},
			Host:       sessiondelivery.HostStatus{Hostname: "session-db", DiskUsedPercent: 16},
		})
	}))
	t.Cleanup(remote.Close)
	spoolDir := filepath.Join(t.TempDir(), "spool")
	_, err := sessiondelivery.NewSpool(spoolDir, 8<<20)
	require.NoError(t, err)
	repo := &sessionCapturePolicyRepoStub{
		mode: SessionCaptureModeAll, policies: map[int64]SessionCaptureKeyPolicy{},
		keys: []SessionCaptureAPIKey{{ID: 1}}, updated: time.Now().UTC(),
	}
	cfg := sessionDeliveryTestConfig(t)
	cfg.SessionDelivery.SpoolDir = spoolDir
	cfg.SessionDelivery.SpoolMaxBytes = 8 << 20
	cfg.SessionDelivery.StatusEndpoint = remote.URL
	cfg.SessionDelivery.StatusSecret = secret
	service, err := NewSessionDeliveryAdminService(repo, cfg)
	require.NoError(t, err)
	overview, err := service.Overview(context.Background())
	require.NoError(t, err)
	require.Equal(t, "healthy", overview.Status)
	require.Equal(t, "claude-opus-5", overview.PublicModel)
	require.NotNil(t, overview.Spool)
	require.NotNil(t, overview.Remote)
	require.Equal(t, "session-db", overview.Remote.Host.Hostname)
}

func sessionDeliveryTestConfig(t *testing.T) *config.Config {
	t.Helper()
	spoolDir := filepath.Join(t.TempDir(), "spool")
	_, err := sessiondelivery.NewSpool(spoolDir, 8<<20)
	require.NoError(t, err)
	return &config.Config{SessionDelivery: config.SessionDeliveryConfig{
		Enabled: true, PublicModel: "claude-opus-5", HMACSecret: strings.Repeat("h", 32),
		SpoolDir: spoolDir, SpoolMaxBytes: 8 << 20, CaptureMaxBytes: 1 << 20,
		StatusTimeoutSeconds: 5,
	}}
}
