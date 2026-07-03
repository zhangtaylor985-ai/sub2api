//go:build unit

package service

import (
	"context"
	"testing"
)

// TestBuildUsageBillingCommand_SubscriptionAppliesRateMultiplier locks in the fix
// that subscription-mode billing honours the group (and any user-specific) rate
// multiplier — i.e. cmd.SubscriptionCost tracks ActualCost (= TotalCost *
// RateMultiplier), not raw TotalCost.
func TestBuildUsageBillingCommand_SubscriptionAppliesRateMultiplier(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	subID := int64(42)

	tests := []struct {
		name           string
		totalCost      float64
		actualCost     float64
		isSubscription bool
		wantSub        float64
		wantBalance    float64
	}{
		{
			name:           "subscription with 2x multiplier consumes 2x quota",
			totalCost:      1.0,
			actualCost:     2.0,
			isSubscription: true,
			wantSub:        2.0,
			wantBalance:    0,
		},
		{
			name:           "subscription with 0.5x multiplier consumes 0.5x quota",
			totalCost:      1.0,
			actualCost:     0.5,
			isSubscription: true,
			wantSub:        0.5,
			wantBalance:    0,
		},
		{
			name:           "free subscription (multiplier 0) consumes no quota",
			totalCost:      1.0,
			actualCost:     0,
			isSubscription: true,
			wantSub:        0,
			wantBalance:    0,
		},
		{
			name:           "balance billing keeps using ActualCost (regression)",
			totalCost:      1.0,
			actualCost:     2.0,
			isSubscription: false,
			wantSub:        0,
			wantBalance:    2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &postUsageBillingParams{
				Cost:               &CostBreakdown{TotalCost: tt.totalCost, ActualCost: tt.actualCost},
				User:               &User{ID: 1},
				APIKey:             &APIKey{ID: 2, GroupID: &groupID},
				Account:            &Account{ID: 3},
				Subscription:       &UserSubscription{ID: subID},
				IsSubscriptionBill: tt.isSubscription,
			}

			cmd := buildUsageBillingCommand(context.Background(), "req-1", nil, p)
			if cmd == nil {
				t.Fatal("buildUsageBillingCommand returned nil")
			}
			if cmd.SubscriptionCost != tt.wantSub {
				t.Errorf("SubscriptionCost = %v, want %v", cmd.SubscriptionCost, tt.wantSub)
			}
			if cmd.BalanceCost != tt.wantBalance {
				t.Errorf("BalanceCost = %v, want %v", cmd.BalanceCost, tt.wantBalance)
			}
		})
	}
}

type tokenPackageStateAPIKeyServiceStub struct {
	total float64
}

func (s tokenPackageStateAPIKeyServiceStub) UpdateQuotaUsed(context.Context, int64, float64) error {
	return nil
}

func (s tokenPackageStateAPIKeyServiceStub) UpdateRateLimitUsage(context.Context, int64, float64) error {
	return nil
}

func (s tokenPackageStateAPIKeyServiceStub) GetTokenPackageState(context.Context, int64) (*APIKeyTokenPackageState, error) {
	return &APIKeyTokenPackageState{TotalUSD: s.total, RemainingUSD: s.total}, nil
}

func TestBuildUsageBillingCommand_TokenPackageOnlyUpdatesRateLimitLedger(t *testing.T) {
	t.Parallel()

	p := &postUsageBillingParams{
		Cost:          &CostBreakdown{TotalCost: 2.5, ActualCost: 2.5},
		User:          &User{ID: 1},
		APIKey:        &APIKey{ID: 2},
		Account:       &Account{ID: 3},
		APIKeyService: tokenPackageStateAPIKeyServiceStub{total: 10},
	}

	cmd := buildUsageBillingCommand(context.Background(), "req-token-package-only", nil, p)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.APIKeyRateLimitCost != 2.5 {
		t.Fatalf("APIKeyRateLimitCost = %v, want 2.5", cmd.APIKeyRateLimitCost)
	}
}

func TestBuildUsageBillingCommand_NoRateLimitOrTokenPackageSkipsRateLimitLedger(t *testing.T) {
	t.Parallel()

	p := &postUsageBillingParams{
		Cost:          &CostBreakdown{TotalCost: 2.5, ActualCost: 2.5},
		User:          &User{ID: 1},
		APIKey:        &APIKey{ID: 2},
		Account:       &Account{ID: 3},
		APIKeyService: tokenPackageStateAPIKeyServiceStub{},
	}

	cmd := buildUsageBillingCommand(context.Background(), "req-no-package", nil, p)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.APIKeyRateLimitCost != 0 {
		t.Fatalf("APIKeyRateLimitCost = %v, want 0", cmd.APIKeyRateLimitCost)
	}
}

func TestBuildUsageBillingCommand_DedicatedUnlimitedSkipsInternalBilling(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	p := &postUsageBillingParams{
		Cost: &CostBreakdown{TotalCost: 2.5, ActualCost: 2.5},
		User: &User{ID: 1},
		APIKey: &APIKey{
			ID:          2,
			GroupID:     &groupID,
			Group:       &Group{ID: groupID, DedicatedUnlimited: true},
			Quota:       10,
			RateLimit1d: 5,
		},
		Account: &Account{
			ID:   3,
			Type: AccountTypeAPIKey,
			Extra: map[string]any{
				"quota_limit": 100.0,
			},
		},
		APIKeyService: tokenPackageStateAPIKeyServiceStub{total: 10},
	}

	cmd := buildUsageBillingCommand(context.Background(), "req-dedicated-unlimited", nil, p)
	if cmd == nil {
		t.Fatal("buildUsageBillingCommand returned nil")
	}
	if cmd.BalanceCost != 0 || cmd.SubscriptionCost != 0 || cmd.APIKeyQuotaCost != 0 || cmd.APIKeyRateLimitCost != 0 || cmd.AccountQuotaCost != 0 {
		t.Fatalf("dedicated unlimited costs = balance:%v subscription:%v api_key_quota:%v api_key_rate:%v account_quota:%v, want all zero",
			cmd.BalanceCost, cmd.SubscriptionCost, cmd.APIKeyQuotaCost, cmd.APIKeyRateLimitCost, cmd.AccountQuotaCost)
	}
}
