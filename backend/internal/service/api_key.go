package service

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
)

// API Key status constants
const (
	StatusAPIKeyActive         = "active"
	StatusAPIKeyDisabled       = "disabled"
	StatusAPIKeyQuotaExhausted = "quota_exhausted"
	StatusAPIKeyExpired        = "expired"
)

// Rate limit window durations
const (
	RateLimitWindow5h = 5 * time.Hour
	RateLimitWindow1d = 24 * time.Hour
	RateLimitWindow7d = 7 * 24 * time.Hour
)

// IsWindowExpired returns true if the window starting at windowStart has exceeded the given duration.
// A nil windowStart is treated as expired — no initialized window means any accumulated usage is stale.
func IsWindowExpired(windowStart *time.Time, duration time.Duration) bool {
	return windowStart == nil || time.Since(*windowStart) >= duration
}

type APIKey struct {
	ID          int64
	UserID      int64
	Key         string
	Name        string
	GroupID     *int64
	Status      string
	Concurrency int
	// Admin-only billing multiplier. User-facing API key responses should not expose it.
	RateMultiplier float64
	// TokenPackageRequired makes this key usable only while it has remaining token package balance.
	TokenPackageRequired bool
	// Model family policy is evaluated against the user-requested endpoint/model,
	// not the internal upstream routing target.
	AllowClaudeFamily    bool
	AllowGPTFamily       bool
	ModelFamilyPolicySet bool
	// Image generation policy gates dedicated Images endpoints and Responses image tools.
	AllowImageGeneration     bool
	ImageGenerationPolicySet bool
	// Optional API key-level override for Claude /v1/messages dispatch to OpenAI/Codex.
	// Empty config means inherit the group's messages dispatch mapping.
	MessagesDispatchModelConfig OpenAIMessagesDispatchModelConfig
	IPWhitelist                 []string
	IPBlacklist                 []string
	// 预编译的 IP 规则，用于认证热路径避免重复 ParseIP/ParseCIDR。
	CompiledIPWhitelist *ip.CompiledIPRules `json:"-"`
	CompiledIPBlacklist *ip.CompiledIPRules `json:"-"`
	LastUsedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	User                *User
	Group               *Group

	// Quota fields
	Quota     float64    // Quota limit in USD (0 = unlimited)
	QuotaUsed float64    // Used quota amount
	ExpiresAt *time.Time // Expiration time (required for persisted API keys)

	// Rate limit fields
	RateLimit5h   float64    // Rate limit in USD per 5h (0 = unlimited)
	RateLimit1d   float64    // Rate limit in USD per 1d (0 = unlimited)
	RateLimit7d   float64    // Rate limit in USD per 7d (0 = unlimited)
	Usage5h       float64    // Used amount in current 5h window
	Usage1d       float64    // Used amount in current 1d window
	Usage7d       float64    // Used amount in current 7d window
	Window5hStart *time.Time // Start of current 5h window
	Window1dStart *time.Time // Start of current 1d window
	Window7dStart *time.Time // Start of current 7d window

	// PlanPackageSummary is populated on authentication reads after this key starts
	// using independently expiring plan packages. Historical keys without package
	// rows continue to use the legacy key/group fields above.
	PlanPackageSummary *APIKeyPlanPackageSummary
}

type APIKeyPlanPackage struct {
	ID             int64     `json:"id"`
	APIKeyID       int64     `json:"api_key_id"`
	GroupID        int64     `json:"group_id"`
	RequestID      string    `json:"request_id,omitempty"`
	PackageName    string    `json:"package_name"`
	DailyLimitUSD  float64   `json:"daily_limit_usd"`
	WeeklyLimitUSD float64   `json:"weekly_limit_usd"`
	Concurrency    int       `json:"concurrency"`
	Months         int       `json:"months"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Source         string    `json:"source"`
	Note           string    `json:"note,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	IsActive       bool      `json:"is_active"`
	IsUpcoming     bool      `json:"is_upcoming"`
}

type APIKeyPlanPackageSummary struct {
	Managed          bool       `json:"managed"`
	ActiveCount      int        `json:"active_count"`
	DailyLimitUSD    float64    `json:"daily_limit_usd"`
	WeeklyLimitUSD   float64    `json:"weekly_limit_usd"`
	Concurrency      int        `json:"concurrency"`
	LatestExpiresAt  *time.Time `json:"latest_expires_at,omitempty"`
	NextTransitionAt *time.Time `json:"next_transition_at,omitempty"`
}

type AddAPIKeyPlanPackageInput struct {
	APIKeyID  int64
	GroupID   int64
	RequestID string
	Months    int
	Note      string
	CreatedBy string
	Now       time.Time
}

type AddAPIKeyPlanPackageResult struct {
	Package    APIKeyPlanPackage
	Summary    APIKeyPlanPackageSummary
	Key        string
	Idempotent bool
}

type APIKeyTokenPackage struct {
	ID        int64
	APIKeyID  int64
	AmountUSD float64
	UsedUSD   float64
	Note      string
	CreatedBy string
	StartedAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (p *APIKeyTokenPackage) RemainingUSD() float64 {
	if p == nil {
		return 0
	}
	remaining := p.AmountUSD - p.UsedUSD
	if remaining < 0 {
		return 0
	}
	return remaining
}

type APIKeyTokenPackageState struct {
	TotalUSD     float64
	UsedUSD      float64
	RemainingUSD float64
}

type APIKeyTokenPackageUsage struct {
	ID                  int64
	PackageID           int64
	APIKeyID            int64
	RequestID           string
	RequestFingerprint  string
	Model               string
	CostUSD             float64
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	TotalTokens         int64
	RequestedAt         time.Time
	CreatedAt           time.Time
}

func (k *APIKey) IsActive() bool {
	return k.Status == StatusActive
}

// HasRateLimits returns true if any rate limit window is configured
func (k *APIKey) HasRateLimits() bool {
	return k.EffectiveRateLimit5h() > 0 || k.EffectiveRateLimit1d() > 0 || k.EffectiveRateLimit7d() > 0
}

func (k *APIKey) BillingRateMultiplier() float64 {
	if k == nil || k.RateMultiplier <= 0 {
		return 1
	}
	return k.RateMultiplier
}

// EffectiveRateLimit5h resolves the 5h API key rate limit.
func (k *APIKey) EffectiveRateLimit5h() float64 {
	if k == nil {
		return 0
	}
	return positiveLimit(k.RateLimit5h)
}

// EffectiveRateLimit1d resolves the daily API key rate limit from key override, then group default.
func (k *APIKey) EffectiveRateLimit1d() float64 {
	if k == nil {
		return 0
	}
	if k.PlanPackageSummary != nil && k.PlanPackageSummary.Managed {
		return positiveLimit(k.PlanPackageSummary.DailyLimitUSD)
	}
	if limit := positiveLimit(k.RateLimit1d); limit > 0 {
		return limit
	}
	if k.TokenPackageRequired {
		return 0
	}
	if k.Group != nil {
		return positiveLimitPtr(k.Group.DailyLimitUSD)
	}
	return 0
}

// EffectiveRateLimit7d resolves the weekly API key rate limit from key override, then group default.
func (k *APIKey) EffectiveRateLimit7d() float64 {
	if k == nil {
		return 0
	}
	if k.PlanPackageSummary != nil && k.PlanPackageSummary.Managed {
		return positiveLimit(k.PlanPackageSummary.WeeklyLimitUSD)
	}
	if limit := positiveLimit(k.RateLimit7d); limit > 0 {
		return limit
	}
	if k.TokenPackageRequired {
		return 0
	}
	if k.Group != nil {
		return positiveLimitPtr(k.Group.WeeklyLimitUSD)
	}
	return 0
}

func positiveLimit(value float64) float64 {
	if value > 0 {
		return value
	}
	return 0
}

func positiveLimitPtr(value *float64) float64 {
	if value == nil {
		return 0
	}
	return positiveLimit(*value)
}

// EffectiveConcurrency resolves API key concurrency from key override, group default, then user fallback.
func (k *APIKey) EffectiveConcurrency() int {
	if k == nil {
		return 0
	}
	if k.PlanPackageSummary != nil && k.PlanPackageSummary.Managed {
		if k.PlanPackageSummary.Concurrency > 0 {
			return k.PlanPackageSummary.Concurrency
		}
		return 0
	}
	if k.Concurrency > 0 {
		return k.Concurrency
	}
	if k.Group != nil && k.Group.Concurrency > 0 {
		return k.Group.Concurrency
	}
	if k.User != nil {
		return k.User.Concurrency
	}
	return 0
}

func (k *APIKey) HasManagedPlanPackages() bool {
	return k != nil && k.PlanPackageSummary != nil && k.PlanPackageSummary.Managed
}

// AddCalendarMonthsClamped adds calendar months while preserving the local
// wall-clock time and clamps end-of-month dates (Jan 31 + 1 month = Feb 28/29).
func AddCalendarMonthsClamped(value time.Time, months int) time.Time {
	year, month, day := value.Date()
	hour, minute, second := value.Clock()
	targetFirst := time.Date(year, month+time.Month(months), 1, hour, minute, second, value.Nanosecond(), value.Location())
	lastDay := time.Date(targetFirst.Year(), targetFirst.Month()+1, 0, hour, minute, second, value.Nanosecond(), value.Location()).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(targetFirst.Year(), targetFirst.Month(), day, hour, minute, second, value.Nanosecond(), value.Location())
}

// IsExpired checks if the API key has expired
func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// IsQuotaExhausted checks if the API key quota is exhausted
func (k *APIKey) IsQuotaExhausted() bool {
	if k.Quota <= 0 {
		return false // unlimited
	}
	return k.QuotaUsed >= k.Quota
}

// GetQuotaRemaining returns remaining quota (-1 for unlimited)
func (k *APIKey) GetQuotaRemaining() float64 {
	if k.Quota <= 0 {
		return -1 // unlimited
	}
	remaining := k.Quota - k.QuotaUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetDaysUntilExpiry returns days until expiry (-1 for never expires)
func (k *APIKey) GetDaysUntilExpiry() int {
	if k.ExpiresAt == nil {
		return -1 // never expires
	}
	duration := time.Until(*k.ExpiresAt)
	if duration < 0 {
		return 0
	}
	return int(duration.Hours() / 24)
}

// EffectiveUsage5h returns the 5h window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage5h() float64 {
	if IsWindowExpired(k.Window5hStart, RateLimitWindow5h) {
		return 0
	}
	return k.Usage5h
}

// EffectiveUsage1d returns the 1d window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage1d() float64 {
	if IsWindowExpired(k.Window1dStart, RateLimitWindow1d) {
		return 0
	}
	return k.Usage1d
}

// EffectiveUsage7d returns the 7d window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage7d() float64 {
	if IsWindowExpired(k.Window7dStart, RateLimitWindow7d) {
		return 0
	}
	return k.Usage7d
}

// APIKeyListFilters holds optional filtering parameters for listing API keys.
type APIKeyListFilters struct {
	Search  string
	Status  string
	GroupID *int64 // nil=不筛选, 0=无分组, >0=指定分组
}
