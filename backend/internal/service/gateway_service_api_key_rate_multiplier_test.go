package service

import "testing"

func TestCostWithAPIKeyRateMultiplier_BlackBoxesMultiplierInActualCost(t *testing.T) {
	t.Parallel()

	base := &CostBreakdown{
		InputCost:  0.2,
		OutputCost: 0.3,
		TotalCost:  0.5,
		ActualCost: 0.75,
	}

	got := costWithAPIKeyRateMultiplier(base, 2)
	if got == nil {
		t.Fatal("costWithAPIKeyRateMultiplier returned nil")
	}
	if got == base {
		t.Fatal("costWithAPIKeyRateMultiplier should clone multiplied costs")
	}
	if got.TotalCost != base.TotalCost {
		t.Fatalf("TotalCost = %v, want %v", got.TotalCost, base.TotalCost)
	}
	if got.InputCost != base.InputCost || got.OutputCost != base.OutputCost {
		t.Fatalf("raw cost breakdown changed: got input=%v output=%v", got.InputCost, got.OutputCost)
	}
	if got.ActualCost != 1.5 {
		t.Fatalf("ActualCost = %v, want 1.5", got.ActualCost)
	}
	if base.ActualCost != 0.75 {
		t.Fatalf("base ActualCost mutated: %v", base.ActualCost)
	}
}

func TestAPIKeyBillingRateMultiplier_DefaultsToOne(t *testing.T) {
	t.Parallel()

	if got := (&APIKey{}).BillingRateMultiplier(); got != 1 {
		t.Fatalf("zero multiplier = %v, want 1", got)
	}
	if got := (&APIKey{RateMultiplier: -2}).BillingRateMultiplier(); got != 1 {
		t.Fatalf("negative multiplier = %v, want 1", got)
	}
	if got := (&APIKey{RateMultiplier: 2}).BillingRateMultiplier(); got != 2 {
		t.Fatalf("positive multiplier = %v, want 2", got)
	}
}
