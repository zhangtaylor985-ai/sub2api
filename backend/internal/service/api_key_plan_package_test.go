//go:build unit

package service

import (
	"testing"
	"time"
)

func TestAddCalendarMonthsClamped(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	tests := []struct {
		name   string
		value  time.Time
		months int
		want   time.Time
	}{
		{
			name:   "ordinary renewal preserves wall clock",
			value:  time.Date(2026, 8, 15, 10, 30, 45, 123, location),
			months: 1,
			want:   time.Date(2026, 9, 15, 10, 30, 45, 123, location),
		},
		{
			name:   "end of month clamps in non leap year",
			value:  time.Date(2025, 1, 31, 8, 0, 0, 0, location),
			months: 1,
			want:   time.Date(2025, 2, 28, 8, 0, 0, 0, location),
		},
		{
			name:   "end of month clamps in leap year",
			value:  time.Date(2024, 1, 31, 8, 0, 0, 0, location),
			months: 1,
			want:   time.Date(2024, 2, 29, 8, 0, 0, 0, location),
		},
		{
			name:   "negative month supports legacy baseline",
			value:  time.Date(2026, 3, 31, 8, 0, 0, 0, location),
			months: -1,
			want:   time.Date(2026, 2, 28, 8, 0, 0, 0, location),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AddCalendarMonthsClamped(tt.value, tt.months); !got.Equal(tt.want) {
				t.Fatalf("AddCalendarMonthsClamped() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAPIKeyManagedPlanConcurrency(t *testing.T) {
	key := &APIKey{
		Concurrency: 99,
		User:        &User{Concurrency: 88},
		Group:       &Group{Concurrency: 77},
		PlanPackageSummary: &APIKeyPlanPackageSummary{
			Managed:     true,
			ActiveCount: 2,
			Concurrency: 7,
		},
	}
	if got := key.EffectiveConcurrency(); got != 7 {
		t.Fatalf("EffectiveConcurrency() = %d, want 7", got)
	}

	key.PlanPackageSummary.ActiveCount = 0
	key.PlanPackageSummary.Concurrency = 0
	if got := key.EffectiveConcurrency(); got != 0 {
		t.Fatalf("expired managed packages should contribute zero concurrency, got %d", got)
	}
}
