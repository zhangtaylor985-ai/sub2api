package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBusinessDefaultInitializationUsesConfirmedCommercialBaseline(t *testing.T) {
	initBusinessTestTimezone(t)
	month := businessTestTime(2026, time.August, 1, 0, 0)
	excludedNames := []string{"TW - Lily", "Larry", "TW - jane", "TW - cloud", "TW - Dow", "TW"}
	paidAccounts := []string{"hoangthihang05041997@gmail.com", "anhduc250391@gmail.com"}
	references := &BusinessReferenceData{
		Groups: []BusinessGroupReference{
			{ID: 1, Name: "独享车", IsExclusive: true},
			{ID: 2, Name: "2人车"},
			{ID: 3, Name: "3人车"},
			{ID: 4, Name: "4人车"},
		},
	}
	for index, name := range excludedNames {
		references.APIKeys = append(references.APIKeys, BusinessAPIKeyReference{ID: int64(index + 10), Name: name})
	}
	for index, name := range paidAccounts {
		references.Accounts = append(references.Accounts, BusinessAccountReference{ID: int64(index + 20), Name: name})
	}

	defaults, gaps := buildBusinessDefaultInitialization(month, references)

	require.Equal(t, int64(6_750_000), defaults.USDRateScaled)
	require.Empty(t, gaps.MissingPricingTiers)
	require.Empty(t, gaps.MissingExcludedNames)
	require.Empty(t, gaps.MissingAccountNames)
	require.Len(t, defaults.Pricing, 4)
	require.Equal(t, map[string]int64{
		"dedicated": 146_000,
		"double":    73_000,
		"triple":    48_500,
		"quad":      36_500,
	}, businessDefaultPricesByTier(defaults.Pricing))
	require.Len(t, defaults.ExcludedKeys, 6)
	require.Len(t, defaults.Costs, 3)

	costs := make(map[string]BusinessDefaultCost, len(defaults.Costs))
	for _, cost := range defaults.Costs {
		costs[cost.AccountIdentifier] = cost
	}
	for _, name := range paidAccounts {
		require.Equal(t, int64(15_000), costs[name].AmountMinor, name)
		require.Equal(t, "USD", costs[name].Currency, name)
		require.False(t, costs[name].IsFree, name)
	}
	domain := costs["claudepool.com"]
	require.Equal(t, int64(3_000), domain.AmountMinor)
	require.Equal(t, BusinessCostClassOperating, domain.CostClass)
	require.Equal(t, BusinessBillingCycleYearly, domain.BillingCycle)
}

func TestBusinessServiceCostAndReferenceValidation(t *testing.T) {
	initBusinessTestTimezone(t)
	repo := newBusinessManagementRepositoryStub()
	svc := NewBusinessService(repo)
	accountID := int64(11)
	repo.accountIDs[accountID] = true

	base := CreateBusinessCostInput{
		Name:         "Philippines subscription",
		CostClass:    BusinessCostClassDirect,
		Category:     "subscription_account",
		AmountMinor:  15_000,
		Currency:     "usd",
		BillingCycle: BusinessBillingCycleMonthly,
		StartsOn:     "2026-08-01",
		AccountID:    &accountID,
		Active:       true,
	}

	invalid := base
	invalid.AmountMinor = -1
	_, err := svc.CreateCost(context.Background(), invalid)
	require.ErrorIs(t, err, ErrBusinessCostInvalid)
	require.Zero(t, repo.createCostCalls)

	invalid = base
	invalid.Currency = "XYZ"
	_, err = svc.CreateCost(context.Background(), invalid)
	require.ErrorIs(t, err, ErrBusinessCostInvalid)

	badEnd := "2026-07-31"
	invalid = base
	invalid.EndsOn = &badEnd
	_, err = svc.CreateCost(context.Background(), invalid)
	require.ErrorIs(t, err, ErrBusinessCostInvalid)

	missingAccountID := int64(99)
	invalid = base
	invalid.AccountID = &missingAccountID
	_, err = svc.CreateCost(context.Background(), invalid)
	require.ErrorIs(t, err, ErrBusinessCostInvalid)

	cost, err := svc.CreateCost(context.Background(), base)
	require.NoError(t, err)
	require.Equal(t, 1, repo.createCostCalls)
	require.Equal(t, int64(501), cost.ID)
	require.Equal(t, "USD", cost.Currency)
	require.Equal(t, accountID, *cost.AccountID)
}

func TestBusinessServiceRejectsUnknownPricingTierAndConflictingKeyStrategies(t *testing.T) {
	initBusinessTestTimezone(t)
	repo := newBusinessManagementRepositoryStub()
	repo.groupIDs[7] = true
	repo.apiKeyIDs[42] = true
	repo.subscriptionIDs[9] = true
	svc := NewBusinessService(repo)

	_, err := svc.UpsertPricingRule(context.Background(), UpsertBusinessPricingRuleInput{
		GroupID: 7, Tier: "custom", MonthlyPriceCents: 1_000, Active: true,
	})
	require.ErrorIs(t, err, ErrBusinessPricingRuleInvalid)

	override := int64(36_500)
	subscriptionID := int64(9)
	_, err = svc.UpsertAPIKeyConfig(context.Background(), 42, UpsertBusinessAPIKeyConfigInput{
		OverrideAmountCents: &override, PrivateSubscriptionID: &subscriptionID,
	})
	require.ErrorIs(t, err, ErrBusinessAPIKeyConfigInvalid)

	_, err = svc.UpsertAPIKeyConfig(context.Background(), 42, UpsertBusinessAPIKeyConfigInput{
		RevenueExcluded: true, PrivateSubscriptionID: &subscriptionID,
	})
	require.ErrorIs(t, err, ErrBusinessAPIKeyConfigInvalid)
}

func TestBusinessServiceInitializationAndReconciliation(t *testing.T) {
	initBusinessTestTimezone(t)
	now := businessTestTime(2026, time.August, 8, 12, 0)
	repo := newBusinessManagementRepositoryStub()
	repo.references = &BusinessReferenceData{
		Groups:  []BusinessGroupReference{{ID: 1, Name: "独享车", IsExclusive: true}},
		APIKeys: []BusinessAPIKeyReference{{ID: 10, Name: "TW - Lily"}},
	}
	repo.bundle = &BusinessSourceBundle{APIKeys: []BusinessAPIKeySource{{
		ID: 10, Name: "No expiry", Status: "active", GroupStatus: "active", UserStatus: "active",
	}}}
	repo.history = []BusinessHistoryPoint{{Month: businessTestTime(2026, time.June, 1, 0, 0)}}
	svc := NewBusinessService(repo)
	svc.now = func() time.Time { return now }

	result, err := svc.InitializeDefaults(context.Background())
	require.NoError(t, err)
	require.True(t, repo.initializeCalled)
	require.Equal(t, int64(6_750_000), repo.initialized.USDRateScaled)
	require.Contains(t, result.MissingPricingTiers, "double")
	require.Contains(t, result.MissingExcludedNames, "Larry")
	require.Contains(t, result.MissingAccountNames, "anhduc250391@gmail.com")

	reconciliation, err := svc.Reconciliation(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, reconciliation.ErrorCount, 1)
	require.GreaterOrEqual(t, reconciliation.InfoCount, 1)
	requireBusinessIssueTypes(t, reconciliation.Issues, BusinessIssueMissingExpiry, BusinessIssueHistoryGap)
}

func TestBusinessSnapshotSchedulerClosesPreviousMonthOnlyOnDayOne(t *testing.T) {
	initBusinessTestTimezone(t)
	repo := &businessRepositoryStub{bundle: &BusinessSourceBundle{}}
	svc := NewBusinessService(repo)
	scheduler := NewBusinessSnapshotScheduler(svc, time.Hour)
	scheduler.now = func() time.Time { return businessTestTime(2026, time.August, 2, 0, 1) }
	svc.now = scheduler.now

	result, err := scheduler.runOnce(context.Background())
	require.NoError(t, err)
	require.True(t, result.Skipped)
	require.Zero(t, repo.closeCalls)

	scheduler.now = func() time.Time { return businessTestTime(2026, time.August, 1, 0, 1) }
	svc.now = scheduler.now
	result, err = scheduler.runOnce(context.Background())
	require.NoError(t, err)
	require.False(t, result.Skipped)
	require.True(t, result.Created)
	require.Equal(t, "2026-07", result.Month)

	result, err = scheduler.runOnce(context.Background())
	require.NoError(t, err)
	require.False(t, result.Skipped)
	require.False(t, result.Created)
	require.Equal(t, 2, repo.closeCalls)
}

func businessDefaultPricesByTier(items []BusinessDefaultPricing) map[string]int64 {
	result := make(map[string]int64, len(items))
	for _, item := range items {
		result[item.Tier] = item.MonthlyPriceCents
	}
	return result
}

type businessManagementRepositoryStub struct {
	*businessRepositoryStub
	references       *BusinessReferenceData
	accountIDs       map[int64]bool
	groupIDs         map[int64]bool
	apiKeyIDs        map[int64]bool
	subscriptionIDs  map[int64]bool
	createCostCalls  int
	initializeCalled bool
	initialized      BusinessDefaultInitialization
	exchangeRates    []BusinessExchangeRate
	upsertedRate     *BusinessExchangeRate
}

func newBusinessManagementRepositoryStub() *businessManagementRepositoryStub {
	return &businessManagementRepositoryStub{
		businessRepositoryStub: &businessRepositoryStub{bundle: &BusinessSourceBundle{}},
		references:             &BusinessReferenceData{},
		accountIDs:             make(map[int64]bool),
		groupIDs:               make(map[int64]bool),
		apiKeyIDs:              make(map[int64]bool),
		subscriptionIDs:        make(map[int64]bool),
	}
}

func (r *businessManagementRepositoryStub) ListCosts(context.Context) ([]BusinessCostItem, error) {
	return nil, nil
}

func (r *businessManagementRepositoryStub) CreateCost(_ context.Context, cost *BusinessCostItem) error {
	r.createCostCalls++
	cost.ID = 500 + int64(r.createCostCalls)
	return nil
}

func (r *businessManagementRepositoryStub) UpdateCost(context.Context, *BusinessCostItem) error {
	return nil
}

func (r *businessManagementRepositoryStub) DeleteCost(context.Context, int64) error { return nil }

func (r *businessManagementRepositoryStub) ListPricingRules(context.Context) ([]BusinessPricingRule, error) {
	return nil, nil
}

func (r *businessManagementRepositoryStub) UpsertPricingRule(context.Context, *BusinessPricingRule) error {
	return nil
}

func (r *businessManagementRepositoryStub) ListExchangeRates(context.Context, time.Time) ([]BusinessExchangeRate, error) {
	return append([]BusinessExchangeRate(nil), r.exchangeRates...), nil
}

func (r *businessManagementRepositoryStub) UpsertExchangeRate(_ context.Context, rate *BusinessExchangeRate) error {
	copy := *rate
	r.upsertedRate = &copy
	copy.ID = 1
	copy.CreatedAt = businessTestTime(2026, time.August, 9, 0, 0)
	copy.UpdatedAt = copy.CreatedAt
	r.exchangeRates = []BusinessExchangeRate{copy}
	*rate = copy
	return nil
}

func (r *businessManagementRepositoryStub) UpsertAPIKeyConfig(context.Context, *BusinessAPIKeyConfig) error {
	return nil
}

func (r *businessManagementRepositoryStub) ListReferences(context.Context) (*BusinessReferenceData, error) {
	return r.references, nil
}

func (r *businessManagementRepositoryStub) AccountExists(_ context.Context, id int64) (bool, error) {
	return r.accountIDs[id], nil
}

func (r *businessManagementRepositoryStub) GroupExists(_ context.Context, id int64) (bool, error) {
	return r.groupIDs[id], nil
}

func (r *businessManagementRepositoryStub) APIKeyExists(_ context.Context, id int64) (bool, error) {
	return r.apiKeyIDs[id], nil
}

func (r *businessManagementRepositoryStub) PrivateSubscriptionExists(_ context.Context, id int64) (bool, error) {
	return r.subscriptionIDs[id], nil
}

func (r *businessManagementRepositoryStub) InitializeDefaults(
	_ context.Context,
	defaults BusinessDefaultInitialization,
) (*BusinessInitializationResult, error) {
	r.initializeCalled = true
	r.initialized = defaults
	return &BusinessInitializationResult{}, nil
}
