package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
)

type SessionCaptureMode string

const (
	SessionCaptureModeAll      SessionCaptureMode = "all"
	SessionCaptureModeSelected SessionCaptureMode = "selected"
	SessionCaptureModeDisabled SessionCaptureMode = "disabled"
)

type SessionCaptureKeyPolicy string

const (
	SessionCaptureKeyPolicyInherit SessionCaptureKeyPolicy = "inherit"
	SessionCaptureKeyPolicyInclude SessionCaptureKeyPolicy = "include"
	SessionCaptureKeyPolicyExclude SessionCaptureKeyPolicy = "exclude"
)

type SessionCapturePolicySnapshot struct {
	Mode      SessionCaptureMode
	Policies  map[int64]SessionCaptureKeyPolicy
	UpdatedAt time.Time
	UpdatedBy int64
}

type SessionCaptureAPIKey struct {
	ID              int64                   `json:"id"`
	Name            string                  `json:"name"`
	Status          string                  `json:"status"`
	UserEmail       string                  `json:"user_email"`
	GroupName       string                  `json:"group_name"`
	Policy          SessionCaptureKeyPolicy `json:"policy"`
	Effective       bool                    `json:"effective"`
	PolicyUpdatedAt *time.Time              `json:"policy_updated_at,omitempty"`
}

type SessionCaptureAPIKeyPage struct {
	Items    []SessionCaptureAPIKey `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}

type SessionCapturePolicySummary struct {
	Mode             SessionCaptureMode `json:"mode"`
	TotalAPIKeys     int64              `json:"total_api_keys"`
	EffectiveAPIKeys int64              `json:"effective_api_keys"`
	IncludedAPIKeys  int64              `json:"included_api_keys"`
	ExcludedAPIKeys  int64              `json:"excluded_api_keys"`
	UpdatedAt        time.Time          `json:"updated_at"`
	UpdatedBy        int64              `json:"updated_by,omitempty"`
}

type SessionCapturePolicyRepository interface {
	LoadSessionCapturePolicy(context.Context) (*SessionCapturePolicySnapshot, error)
	ListSessionCaptureAPIKeys(context.Context, string, int, int) ([]SessionCaptureAPIKey, int64, error)
	UpdateSessionCaptureMode(context.Context, SessionCaptureMode, int64) error
	UpdateSessionCaptureAPIKey(context.Context, int64, SessionCaptureKeyPolicy, int64) error
	SetOnlySessionCaptureAPIKey(context.Context, int64, int64) error
}

type SessionDeliveryOverview struct {
	Status      string                              `json:"status"`
	ObservedAt  time.Time                           `json:"observed_at"`
	Enabled     bool                                `json:"enabled"`
	PublicModel string                              `json:"public_model"`
	Warnings    []string                            `json:"warnings"`
	Policy      *SessionCapturePolicySummary        `json:"policy,omitempty"`
	Spool       *sessiondelivery.SpoolDetailedStats `json:"spool,omitempty"`
	Remote      *sessiondelivery.StatusSnapshot     `json:"remote,omitempty"`
}

type SessionDeliveryAdminService struct {
	repo         SessionCapturePolicyRepository
	enabled      bool
	publicModel  string
	spoolDir     string
	spoolMax     int64
	statusClient *sessiondelivery.StatusClient
	snapshot     atomic.Pointer[SessionCapturePolicySnapshot]
}

func NewSessionDeliveryAdminService(repo SessionCapturePolicyRepository, cfg *config.Config) (*SessionDeliveryAdminService, error) {
	if repo == nil {
		return nil, errors.New("Session capture policy repository is required")
	}
	if cfg == nil {
		return nil, errors.New("Session delivery configuration is required")
	}
	service := &SessionDeliveryAdminService{
		repo:        repo,
		enabled:     cfg.SessionDelivery.Enabled,
		publicModel: cfg.SessionDelivery.PublicModel,
		spoolDir:    cfg.SessionDelivery.SpoolDir,
		spoolMax:    cfg.SessionDelivery.SpoolMaxBytes,
	}
	if cfg.SessionDelivery.StatusEndpoint != "" {
		client, err := sessiondelivery.NewStatusClient(sessiondelivery.StatusClientConfig{
			Endpoint: cfg.SessionDelivery.StatusEndpoint,
			Secret:   cfg.SessionDelivery.StatusSecret,
			Timeout:  time.Duration(cfg.SessionDelivery.StatusTimeoutSeconds) * time.Second,
		})
		if err != nil {
			return nil, err
		}
		service.statusClient = client
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.reload(ctx); err != nil {
		return nil, fmt.Errorf("load Session capture policy: %w", err)
	}
	return service, nil
}

// ShouldCapture is lock-free on the request path. Configuration mutations
// replace the immutable snapshot atomically.
func (s *SessionDeliveryAdminService) ShouldCapture(apiKeyID int64) bool {
	if s == nil || !s.enabled || apiKeyID <= 0 {
		return false
	}
	snapshot := s.snapshot.Load()
	if snapshot == nil {
		return false
	}
	return sessionCaptureEffective(snapshot.Mode, snapshot.Policies[apiKeyID])
}

func (s *SessionDeliveryAdminService) GetPolicy(ctx context.Context, query string, page, pageSize int) (*SessionCapturePolicySummary, *SessionCaptureAPIKeyPage, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	snapshot := s.snapshot.Load()
	if snapshot == nil {
		return nil, nil, errors.New("Session capture policy is unavailable")
	}
	items, total, err := s.repo.ListSessionCaptureAPIKeys(ctx, strings.TrimSpace(query), page, pageSize)
	if err != nil {
		return nil, nil, err
	}
	for index := range items {
		items[index].Effective = sessionCaptureEffective(snapshot.Mode, items[index].Policy)
	}
	summary := summarizeSessionCapturePolicy(snapshot, total)
	return &summary, &SessionCaptureAPIKeyPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *SessionDeliveryAdminService) UpdateMode(ctx context.Context, mode SessionCaptureMode, actorUserID int64) error {
	if !validSessionCaptureMode(mode) {
		return errors.New("invalid Session capture mode")
	}
	if err := s.repo.UpdateSessionCaptureMode(ctx, mode, actorUserID); err != nil {
		return err
	}
	return s.reload(ctx)
}

func (s *SessionDeliveryAdminService) UpdateAPIKey(ctx context.Context, apiKeyID int64, policy SessionCaptureKeyPolicy, actorUserID int64) error {
	if apiKeyID <= 0 || !validSessionCaptureKeyPolicy(policy) {
		return errors.New("invalid Session capture API key policy")
	}
	if err := s.repo.UpdateSessionCaptureAPIKey(ctx, apiKeyID, policy, actorUserID); err != nil {
		return err
	}
	return s.reload(ctx)
}

// SetOnlyAPIKey is the explicit, transactional implementation of “only record
// this API key”: it selects allowlist mode and removes all other overrides.
func (s *SessionDeliveryAdminService) SetOnlyAPIKey(ctx context.Context, apiKeyID, actorUserID int64) error {
	if apiKeyID <= 0 {
		return errors.New("invalid Session capture API key")
	}
	if err := s.repo.SetOnlySessionCaptureAPIKey(ctx, apiKeyID, actorUserID); err != nil {
		return err
	}
	return s.reload(ctx)
}

func (s *SessionDeliveryAdminService) Overview(ctx context.Context) (*SessionDeliveryOverview, error) {
	if s == nil {
		return nil, errors.New("Session delivery service is unavailable")
	}
	overview := &SessionDeliveryOverview{
		Status:      "healthy",
		ObservedAt:  time.Now().UTC(),
		Enabled:     s.enabled,
		PublicModel: s.publicModel,
		Warnings:    make([]string, 0, 4),
	}
	snapshot := s.snapshot.Load()
	if snapshot != nil {
		_, total, err := s.repo.ListSessionCaptureAPIKeys(ctx, "", 1, 1)
		if err == nil {
			summary := summarizeSessionCapturePolicy(snapshot, total)
			overview.Policy = &summary
		} else {
			overview.Status = "degraded"
			overview.Warnings = append(overview.Warnings, "policy_summary_unavailable")
		}
	}
	if !s.enabled || (snapshot != nil && snapshot.Mode == SessionCaptureModeDisabled) {
		overview.Status = "paused"
	}
	spool, err := sessiondelivery.InspectSpool(s.spoolDir, s.spoolMax)
	if err != nil {
		overview.Status = elevateSessionDeliveryStatus(overview.Status, "critical")
		overview.Warnings = append(overview.Warnings, "spool_unavailable")
	} else {
		overview.Spool = &spool
		switch {
		case spool.UsedPercent >= 80:
			overview.Status = elevateSessionDeliveryStatus(overview.Status, "critical")
			overview.Warnings = append(overview.Warnings, "spool_usage_critical")
		case spool.UsedPercent >= 60:
			overview.Status = elevateSessionDeliveryStatus(overview.Status, "degraded")
			overview.Warnings = append(overview.Warnings, "spool_usage_high")
		}
		if spool.QuarantinedRecords > 0 {
			overview.Status = elevateSessionDeliveryStatus(overview.Status, "degraded")
			overview.Warnings = append(overview.Warnings, "spool_quarantine_present")
		}
		if spool.OldestPendingAt != nil && time.Since(*spool.OldestPendingAt) > 15*time.Minute {
			overview.Status = elevateSessionDeliveryStatus(overview.Status, "degraded")
			overview.Warnings = append(overview.Warnings, "spool_forwarding_delayed")
		}
	}
	if s.statusClient == nil {
		overview.Status = elevateSessionDeliveryStatus(overview.Status, "critical")
		overview.Warnings = append(overview.Warnings, "remote_status_not_configured")
		return overview, nil
	}
	remote, err := s.statusClient.Fetch(ctx)
	if err != nil {
		overview.Status = elevateSessionDeliveryStatus(overview.Status, "critical")
		overview.Warnings = append(overview.Warnings, "remote_status_unavailable")
		return overview, nil
	}
	overview.Remote = &remote
	overview.Status = elevateSessionDeliveryStatus(overview.Status, remote.Status)
	return overview, nil
}

func (s *SessionDeliveryAdminService) reload(ctx context.Context) error {
	snapshot, err := s.repo.LoadSessionCapturePolicy(ctx)
	if err != nil {
		return err
	}
	if snapshot == nil || !validSessionCaptureMode(snapshot.Mode) {
		return errors.New("Session capture policy snapshot is invalid")
	}
	copySnapshot := &SessionCapturePolicySnapshot{
		Mode:      snapshot.Mode,
		Policies:  make(map[int64]SessionCaptureKeyPolicy, len(snapshot.Policies)),
		UpdatedAt: snapshot.UpdatedAt,
		UpdatedBy: snapshot.UpdatedBy,
	}
	for id, policy := range snapshot.Policies {
		if id > 0 && policy != SessionCaptureKeyPolicyInherit && validSessionCaptureKeyPolicy(policy) {
			copySnapshot.Policies[id] = policy
		}
	}
	s.snapshot.Store(copySnapshot)
	return nil
}

func sessionCaptureEffective(mode SessionCaptureMode, policy SessionCaptureKeyPolicy) bool {
	if mode == SessionCaptureModeDisabled || policy == SessionCaptureKeyPolicyExclude {
		return false
	}
	if policy == SessionCaptureKeyPolicyInclude {
		return true
	}
	return mode == SessionCaptureModeAll
}

func summarizeSessionCapturePolicy(snapshot *SessionCapturePolicySnapshot, total int64) SessionCapturePolicySummary {
	var included, excluded int64
	for _, policy := range snapshot.Policies {
		switch policy {
		case SessionCaptureKeyPolicyInclude:
			included++
		case SessionCaptureKeyPolicyExclude:
			excluded++
		}
	}
	effective := int64(0)
	switch snapshot.Mode {
	case SessionCaptureModeAll:
		effective = total - excluded
	case SessionCaptureModeSelected:
		effective = included
	}
	if effective < 0 {
		effective = 0
	}
	if effective > total {
		effective = total
	}
	return SessionCapturePolicySummary{
		Mode:             snapshot.Mode,
		TotalAPIKeys:     total,
		EffectiveAPIKeys: effective,
		IncludedAPIKeys:  included,
		ExcludedAPIKeys:  excluded,
		UpdatedAt:        snapshot.UpdatedAt,
		UpdatedBy:        snapshot.UpdatedBy,
	}
}

func validSessionCaptureMode(mode SessionCaptureMode) bool {
	return mode == SessionCaptureModeAll || mode == SessionCaptureModeSelected || mode == SessionCaptureModeDisabled
}

func validSessionCaptureKeyPolicy(policy SessionCaptureKeyPolicy) bool {
	return policy == SessionCaptureKeyPolicyInherit || policy == SessionCaptureKeyPolicyInclude || policy == SessionCaptureKeyPolicyExclude
}

func elevateSessionDeliveryStatus(current, candidate string) string {
	rank := map[string]int{"healthy": 0, "paused": 0, "degraded": 1, "critical": 2}
	if rank[candidate] > rank[current] {
		return candidate
	}
	return current
}
