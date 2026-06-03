package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsWindowExpired(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		start    *time.Time
		duration time.Duration
		want     bool
	}{
		{
			name:     "nil window start (treated as expired)",
			start:    nil,
			duration: RateLimitWindow5h,
			want:     true,
		},
		{
			name:     "active window (started 1h ago, 5h window)",
			start:    rateLimitTimePtr(now.Add(-1 * time.Hour)),
			duration: RateLimitWindow5h,
			want:     false,
		},
		{
			name:     "expired window (started 6h ago, 5h window)",
			start:    rateLimitTimePtr(now.Add(-6 * time.Hour)),
			duration: RateLimitWindow5h,
			want:     true,
		},
		{
			name:     "exactly at boundary (started 5h ago, 5h window)",
			start:    rateLimitTimePtr(now.Add(-5 * time.Hour)),
			duration: RateLimitWindow5h,
			want:     true,
		},
		{
			name:     "active 1d window (started 12h ago)",
			start:    rateLimitTimePtr(now.Add(-12 * time.Hour)),
			duration: RateLimitWindow1d,
			want:     false,
		},
		{
			name:     "expired 1d window (started 25h ago)",
			start:    rateLimitTimePtr(now.Add(-25 * time.Hour)),
			duration: RateLimitWindow1d,
			want:     true,
		},
		{
			name:     "active 7d window (started 3d ago)",
			start:    rateLimitTimePtr(now.Add(-3 * 24 * time.Hour)),
			duration: RateLimitWindow7d,
			want:     false,
		},
		{
			name:     "expired 7d window (started 8d ago)",
			start:    rateLimitTimePtr(now.Add(-8 * 24 * time.Hour)),
			duration: RateLimitWindow7d,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsWindowExpired(tt.start, tt.duration)
			if got != tt.want {
				t.Errorf("IsWindowExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIKey_EffectiveUsage(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		key    APIKey
		want5h float64
		want1d float64
		want7d float64
	}{
		{
			name: "all windows active",
			key: APIKey{
				Usage5h:       5.0,
				Usage1d:       10.0,
				Usage7d:       50.0,
				Window5hStart: rateLimitTimePtr(now.Add(-1 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-12 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-3 * 24 * time.Hour)),
			},
			want5h: 5.0,
			want1d: 10.0,
			want7d: 50.0,
		},
		{
			name: "all windows expired",
			key: APIKey{
				Usage5h:       5.0,
				Usage1d:       10.0,
				Usage7d:       50.0,
				Window5hStart: rateLimitTimePtr(now.Add(-6 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-25 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-8 * 24 * time.Hour)),
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
		{
			name: "nil window starts return 0 (stale usage reset)",
			key: APIKey{
				Usage5h:       5.0,
				Usage1d:       10.0,
				Usage7d:       50.0,
				Window5hStart: nil,
				Window1dStart: nil,
				Window7dStart: nil,
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
		{
			name: "mixed: 5h expired, 1d active, 7d nil",
			key: APIKey{
				Usage5h:       5.0,
				Usage1d:       10.0,
				Usage7d:       50.0,
				Window5hStart: rateLimitTimePtr(now.Add(-6 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-12 * time.Hour)),
				Window7dStart: nil,
			},
			want5h: 0,
			want1d: 10.0,
			want7d: 0,
		},
		{
			name: "zero usage with active windows",
			key: APIKey{
				Usage5h:       0,
				Usage1d:       0,
				Usage7d:       0,
				Window5hStart: rateLimitTimePtr(now.Add(-1 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-1 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-1 * time.Hour)),
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.EffectiveUsage5h(); got != tt.want5h {
				t.Errorf("EffectiveUsage5h() = %v, want %v", got, tt.want5h)
			}
			if got := tt.key.EffectiveUsage1d(); got != tt.want1d {
				t.Errorf("EffectiveUsage1d() = %v, want %v", got, tt.want1d)
			}
			if got := tt.key.EffectiveUsage7d(); got != tt.want7d {
				t.Errorf("EffectiveUsage7d() = %v, want %v", got, tt.want7d)
			}
		})
	}
}

func TestAPIKey_EffectiveRateLimits(t *testing.T) {
	groupDaily := 150.0
	groupWeekly := 500.0

	tests := []struct {
		name      string
		key       APIKey
		wantHas   bool
		want5h    float64
		wantDaily float64
		wantWeek  float64
	}{
		{
			name: "inherits daily and weekly limits from group",
			key: APIKey{
				Group: &Group{
					DailyLimitUSD:  &groupDaily,
					WeeklyLimitUSD: &groupWeekly,
				},
			},
			wantHas:   true,
			wantDaily: 150,
			wantWeek:  500,
		},
		{
			name: "key override wins over group limits",
			key: APIKey{
				RateLimit5h: 12,
				RateLimit1d: 100,
				RateLimit7d: 330,
				Group: &Group{
					DailyLimitUSD:  &groupDaily,
					WeeklyLimitUSD: &groupWeekly,
				},
			},
			wantHas:   true,
			want5h:    12,
			wantDaily: 100,
			wantWeek:  330,
		},
		{
			name:    "no key or group limits",
			key:     APIKey{},
			wantHas: false,
		},
		{
			name: "zero group values are unlimited",
			key: APIKey{
				Group: &Group{
					DailyLimitUSD:  rateLimitFloatPtr(0),
					WeeklyLimitUSD: rateLimitFloatPtr(0),
				},
			},
			wantHas: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.HasRateLimits(); got != tt.wantHas {
				t.Errorf("HasRateLimits() = %v, want %v", got, tt.wantHas)
			}
			if got := tt.key.EffectiveRateLimit5h(); got != tt.want5h {
				t.Errorf("EffectiveRateLimit5h() = %v, want %v", got, tt.want5h)
			}
			if got := tt.key.EffectiveRateLimit1d(); got != tt.wantDaily {
				t.Errorf("EffectiveRateLimit1d() = %v, want %v", got, tt.wantDaily)
			}
			if got := tt.key.EffectiveRateLimit7d(); got != tt.wantWeek {
				t.Errorf("EffectiveRateLimit7d() = %v, want %v", got, tt.wantWeek)
			}
		})
	}
}

func TestBillingCacheService_EvaluateRateLimits_UsesGroupLimits(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		key       APIKey
		usage1d   float64
		usage7d   float64
		wantError error
	}{
		{
			name: "daily group limit is enforced",
			key: APIKey{
				ID: 1,
				Group: &Group{
					DailyLimitUSD:  rateLimitFloatPtr(100),
					WeeklyLimitUSD: rateLimitFloatPtr(330),
				},
			},
			usage1d:   100,
			wantError: ErrAPIKeyRateLimit1dExceeded,
		},
		{
			name: "weekly group limit is enforced",
			key: APIKey{
				ID: 2,
				Group: &Group{
					DailyLimitUSD:  rateLimitFloatPtr(100),
					WeeklyLimitUSD: rateLimitFloatPtr(330),
				},
			},
			usage1d:   99,
			usage7d:   330,
			wantError: ErrAPIKeyRateLimit7dExceeded,
		},
		{
			name: "key override wins over higher group limit",
			key: APIKey{
				ID:          3,
				RateLimit1d: 60,
				Group: &Group{
					DailyLimitUSD:  rateLimitFloatPtr(100),
					WeeklyLimitUSD: rateLimitFloatPtr(330),
				},
			},
			usage1d:   60,
			wantError: ErrAPIKeyRateLimit1dExceeded,
		},
		{
			name: "under inherited limits succeeds",
			key: APIKey{
				ID: 4,
				Group: &Group{
					DailyLimitUSD:  rateLimitFloatPtr(100),
					WeeklyLimitUSD: rateLimitFloatPtr(330),
				},
			},
			usage1d: 99,
			usage7d: 329,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &BillingCacheService{}
			err := svc.evaluateRateLimits(
				context.Background(),
				&tt.key,
				0,
				tt.usage1d,
				tt.usage7d,
				rateLimitTimePtr(now.Add(-1*time.Hour)),
				rateLimitTimePtr(now.Add(-1*time.Hour)),
				rateLimitTimePtr(now.Add(-1*time.Hour)),
			)
			if tt.wantError == nil {
				if err != nil {
					t.Fatalf("evaluateRateLimits() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("evaluateRateLimits() error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

type tokenPackageRateLimitLoaderStub struct {
	remaining float64
}

func (s *tokenPackageRateLimitLoaderStub) GetRateLimitData(context.Context, int64) (*APIKeyRateLimitData, error) {
	return nil, nil
}

func (s *tokenPackageRateLimitLoaderStub) GetTokenPackageRemaining(context.Context, int64) (float64, error) {
	return s.remaining, nil
}

func TestBillingCacheService_EvaluateRateLimits_AllowsDailyWeeklyOverageWithTokenPackage(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		key     APIKey
		usage5h float64
		usage1d float64
		usage7d float64
		wantErr error
	}{
		{
			name: "daily group limit can use token package",
			key: APIKey{
				ID: 1,
				Group: &Group{
					DailyLimitUSD:  rateLimitFloatPtr(100),
					WeeklyLimitUSD: rateLimitFloatPtr(330),
				},
			},
			usage1d: 100,
		},
		{
			name: "weekly group limit can use token package",
			key: APIKey{
				ID: 2,
				Group: &Group{
					DailyLimitUSD:  rateLimitFloatPtr(100),
					WeeklyLimitUSD: rateLimitFloatPtr(330),
				},
			},
			usage1d: 99,
			usage7d: 330,
		},
		{
			name: "five hour limit still blocks",
			key: APIKey{
				ID:          3,
				RateLimit5h: 10,
				Group: &Group{
					DailyLimitUSD:  rateLimitFloatPtr(100),
					WeeklyLimitUSD: rateLimitFloatPtr(330),
				},
			},
			usage5h: 10,
			wantErr: ErrAPIKeyRateLimit5hExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &BillingCacheService{
				apiKeyRateLimitLoader: &tokenPackageRateLimitLoaderStub{remaining: 20},
			}
			err := svc.evaluateRateLimits(
				context.Background(),
				&tt.key,
				tt.usage5h,
				tt.usage1d,
				tt.usage7d,
				rateLimitTimePtr(now.Add(-1*time.Hour)),
				rateLimitTimePtr(now.Add(-1*time.Hour)),
				rateLimitTimePtr(now.Add(-1*time.Hour)),
			)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("evaluateRateLimits() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("evaluateRateLimits() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAPIKeyRateLimitData_EffectiveUsage(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		data   APIKeyRateLimitData
		want5h float64
		want1d float64
		want7d float64
	}{
		{
			name: "all windows active",
			data: APIKeyRateLimitData{
				Usage5h:       3.0,
				Usage1d:       8.0,
				Usage7d:       40.0,
				Window5hStart: rateLimitTimePtr(now.Add(-2 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-10 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-2 * 24 * time.Hour)),
			},
			want5h: 3.0,
			want1d: 8.0,
			want7d: 40.0,
		},
		{
			name: "all windows expired",
			data: APIKeyRateLimitData{
				Usage5h:       3.0,
				Usage1d:       8.0,
				Usage7d:       40.0,
				Window5hStart: rateLimitTimePtr(now.Add(-10 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-48 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-10 * 24 * time.Hour)),
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
		{
			name: "nil window starts return 0 (stale usage reset)",
			data: APIKeyRateLimitData{
				Usage5h:       3.0,
				Usage1d:       8.0,
				Usage7d:       40.0,
				Window5hStart: nil,
				Window1dStart: nil,
				Window7dStart: nil,
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.data.EffectiveUsage5h(); got != tt.want5h {
				t.Errorf("EffectiveUsage5h() = %v, want %v", got, tt.want5h)
			}
			if got := tt.data.EffectiveUsage1d(); got != tt.want1d {
				t.Errorf("EffectiveUsage1d() = %v, want %v", got, tt.want1d)
			}
			if got := tt.data.EffectiveUsage7d(); got != tt.want7d {
				t.Errorf("EffectiveUsage7d() = %v, want %v", got, tt.want7d)
			}
		})
	}
}

func rateLimitTimePtr(t time.Time) *time.Time {
	return &t
}

func rateLimitFloatPtr(v float64) *float64 {
	return &v
}
