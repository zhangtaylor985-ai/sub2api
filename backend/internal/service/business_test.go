package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestCalculateBusinessReportKeepsPrivateSubscriptionsIndependent(t *testing.T) {
	initBusinessTestTimezone(t)
	asOf := businessTestTime(2026, time.August, 8, 12, 0)
	futureExpiry := businessTestTime(2026, time.September, 8, 0, 0)
	expiredAt := businessTestTime(2026, time.August, 7, 23, 59)
	groupID := int64(10)
	linkedSubscriptionID := int64(100)
	override := int64(73_000)
	exclusionReason := "internal key"

	report := CalculateBusinessReport(asOf, &BusinessSourceBundle{
		APIKeys: []BusinessAPIKeySource{
			businessTestKey(1, "Dedicated", groupID, futureExpiry),
			businessTestKey(2, "Override", groupID, futureExpiry),
			businessTestKey(3, "Excluded", groupID, futureExpiry),
			{
				ID:          4,
				Name:        "No expiry",
				Status:      "active",
				GroupID:     &groupID,
				GroupName:   "Dedicated",
				GroupStatus: "active",
				UserID:      1,
				UserStatus:  "active",
			},
			businessTestKey(5, "Expired", groupID, expiredAt),
			businessTestKey(6, "Linked", groupID, futureExpiry),
			businessTestKey(7, "Same Customer", groupID, futureExpiry),
		},
		PrivateSubscriptions: []BusinessPrivateSubscriptionSource{
			{
				ID:               linkedSubscriptionID,
				Name:             "Linked customer",
				SubscriptionType: "2-person",
				AmountCents:      60_000,
				ExpiresOn:        businessTestTime(2026, time.October, 1, 0, 0),
			},
			{
				ID:               101,
				Name:             "Same Customer",
				SubscriptionType: "private",
				AmountCents:      36_500,
				ExpiresOn:        futureExpiry,
			},
		},
		PricingRules: []BusinessPricingRule{
			{GroupID: groupID, Tier: "dedicated", MonthlyPriceCents: 146_000, Active: true},
		},
		APIKeyConfigs: []BusinessAPIKeyConfig{
			{APIKeyID: 2, OverrideAmountCents: &override},
			{APIKeyID: 3, RevenueExcluded: true, Reason: &exclusionReason},
			{APIKeyID: 6, PrivateSubscriptionID: &linkedSubscriptionID},
		},
		Costs: []BusinessCostItem{
			businessTestCost(1, "Direct", BusinessCostClassDirect, 1_000, BusinessCurrencyCNY),
			businessTestCost(2, "Operating", BusinessCostClassOperating, 1_000, BusinessCurrencyCNY),
		},
	})

	require.Equal(t, 4, report.Summary.APIKeyCount)
	require.Equal(t, 2, report.Summary.PrivateSubscriptionCount)
	require.Equal(t, 6, report.Summary.CustomerCount)
	require.Equal(t, 1, report.Summary.ExcludedAPIKeyCount)
	require.Equal(t, int64(511_000), report.Summary.APIKeyRevenueCents)
	require.Equal(t, int64(96_500), report.Summary.PrivateSubscriptionRevenueCents)
	require.Equal(t, int64(607_500), report.Summary.TotalRevenueCents)
	require.True(t, report.Summary.CostsComplete)
	requireBusinessIssueTypes(t, report.Issues, BusinessIssueMissingExpiry)
	for _, issue := range report.Issues {
		require.NotEqual(t, BusinessIssueExpiredActive, issue.Type)
		require.NotEqual(t, BusinessIssuePossibleDuplicate, issue.Type)
		require.NotEqual(t, BusinessIssueLinkedExpiryMismatch, issue.Type)
	}

	linkedItems := businessItemsByType(report.Items, BusinessItemRevenuePrivateSubscription)
	require.Len(t, linkedItems, 2)
	require.NotNil(t, linkedItems[0].SourceID)
	require.NotNil(t, linkedItems[1].SourceID)
	for _, item := range linkedItems {
		require.Nil(t, item.LinkedAPIKeyID)
	}
}

func TestCalculateBusinessReportRecognizesTokenPackageSalesInCNY(t *testing.T) {
	initBusinessTestTimezone(t)
	asOf := businessTestTime(2026, time.July, 31, 23, 59)
	futureExpiry := businessTestTime(2026, time.December, 31, 23, 59)
	groupID := int64(19)
	activePackageKey := businessTestKey(147, "Mr 椰子", groupID, futureExpiry)
	activePackageKey.TokenPackageRequired = true
	activePackageKey.TokenPackageRemainingUSDMinor = 92_443
	depletedPackageKey := businessTestKey(505, "东手工", groupID, futureExpiry)
	depletedPackageKey.TokenPackageRequired = true

	report := CalculateBusinessReport(asOf, &BusinessSourceBundle{
		APIKeys: []BusinessAPIKeySource{activePackageKey, depletedPackageKey},
		TokenPackages: []BusinessTokenPackageSource{
			{ID: 14, APIKeyID: 147, APIKeyName: "Mr 椰子", GroupName: "Codex Token Package Pool", AmountUSDMinor: 200_000, CreatedAt: asOf},
			{ID: 15, APIKeyID: 505, APIKeyName: "东手工", GroupName: "Codex Token Package Pool", AmountUSDMinor: 10_000, CreatedAt: asOf},
		},
		Costs: []BusinessCostItem{
			businessTestCost(1, "Direct", BusinessCostClassDirect, 1_000, BusinessCurrencyCNY),
			businessTestCost(2, "Operating", BusinessCostClassOperating, 1_000, BusinessCurrencyCNY),
		},
	})

	require.Equal(t, 1, report.Summary.APIKeyCount)
	require.Equal(t, int64(126_000), report.Summary.APIKeyRevenueCents)
	items := businessItemsByType(report.Items, BusinessItemRevenueTokenPackage)
	require.Len(t, items, 2)
	require.Equal(t, int64(120_000), items[0].AmountCNYCents)
	require.Equal(t, int64(6_000), items[1].AmountCNYCents)
	require.Empty(t, report.Issues)
}

func TestCalculateBusinessReportConfirmedAugustBaseline(t *testing.T) {
	initBusinessTestTimezone(t)
	asOf := businessTestTime(2026, time.August, 8, 19, 36)
	futureExpiry := businessTestTime(2026, time.December, 8, 23, 59)
	rules := []BusinessPricingRule{
		{GroupID: 13, Tier: "dedicated", MonthlyPriceCents: 146_000, Active: true},
		{GroupID: 14, Tier: "double", MonthlyPriceCents: 73_000, Active: true},
		{GroupID: 15, Tier: "triple", MonthlyPriceCents: 48_500, Active: true},
		{GroupID: 16, Tier: "quad", MonthlyPriceCents: 36_500, Active: true},
	}
	bundle := &BusinessSourceBundle{PricingRules: rules}
	keyID := int64(1)
	for _, group := range []struct {
		id    int64
		name  string
		count int
	}{{13, "CP Legacy dedicated", 4}, {14, "CP Legacy double", 16}, {15, "CP Legacy triple", 3}, {16, "CP Legacy quad", 4}} {
		for index := 0; index < group.count; index++ {
			bundle.APIKeys = append(bundle.APIKeys, BusinessAPIKeySource{
				ID: keyID, Name: group.name, Status: "active", ExpiresAt: &futureExpiry,
				GroupID: &group.id, GroupName: group.name, GroupStatus: "active",
				UserID: keyID, UserEmail: "customer@example.com", UserStatus: "active",
			})
			keyID++
		}
	}
	for _, name := range []string{"TW - Lily", "Larry", "TW - jane", "TW - cloud", "TW - Dow", "TW"} {
		groupID := int64(14)
		bundle.APIKeys = append(bundle.APIKeys, businessTestKey(keyID, name, groupID, futureExpiry))
		bundle.APIKeyConfigs = append(bundle.APIKeyConfigs, BusinessAPIKeyConfig{APIKeyID: keyID, RevenueExcluded: true})
		keyID++
	}
	for index, name := range []string{"沛沛", "三风与我", "东木青", "七七", "Mr 椰子"} {
		bundle.PrivateSubscriptions = append(bundle.PrivateSubscriptions, BusinessPrivateSubscriptionSource{
			ID: int64(index + 1), Name: name, SubscriptionType: "private",
			AmountCents: 73_000, ExpiresOn: futureExpiry,
		})
	}
	bundle.Costs = []BusinessCostItem{
		businessTestCost(1, "hoangthihang05041997@gmail.com subscription", BusinessCostClassDirect, 15_000, "USD"),
		businessTestCost(2, "anhduc250391@gmail.com subscription", BusinessCostClassDirect, 15_000, "USD"),
	}
	bundle.ExchangeRates = []BusinessExchangeRate{{
		Month: businessMonthStart(asOf), Currency: "USD", RateScaled: 6_750_000,
	}}

	report := CalculateBusinessReport(asOf, bundle)

	require.Equal(t, 27, report.Summary.APIKeyCount)
	require.Equal(t, 5, report.Summary.PrivateSubscriptionCount)
	require.Equal(t, 32, report.Summary.CustomerCount)
	require.Equal(t, 6, report.Summary.ExcludedAPIKeyCount)
	require.Equal(t, int64(2_043_500), report.Summary.APIKeyRevenueCents)
	require.Equal(t, int64(365_000), report.Summary.PrivateSubscriptionRevenueCents)
	require.Equal(t, int64(2_408_500), report.Summary.TotalRevenueCents)
	require.Equal(t, int64(202_500), report.Summary.DirectCostCents)
	require.Equal(t, int64(2_206_000), report.Summary.GrossProfitCents)
	require.Equal(t, int64(9_159), report.Summary.GrossMarginBPS)
	require.False(t, report.Summary.CostsComplete)
}

func TestCalculateBusinessReportCostCyclesAndExchangeRates(t *testing.T) {
	initBusinessTestTimezone(t)
	asOf := businessTestTime(2026, time.August, 8, 12, 0)
	startsAugust := businessTestTime(2025, time.August, 15, 0, 0)
	oneTimeAugust := businessTestTime(2026, time.August, 2, 0, 0)
	startsSeptember := businessTestTime(2025, time.September, 1, 0, 0)

	report := CalculateBusinessReport(asOf, &BusinessSourceBundle{
		Costs: []BusinessCostItem{
			{
				ID: 1, Name: "USD monthly", CostClass: BusinessCostClassDirect,
				Category: "subscription", AmountMinor: 15_000, Currency: "USD",
				BillingCycle: BusinessBillingCycleMonthly, StartsOn: startsAugust, Active: true,
			},
			{
				ID: 2, Name: "Annual domain", CostClass: BusinessCostClassOperating,
				Category: "domain", AmountMinor: 12_000, Currency: "CNY",
				BillingCycle: BusinessBillingCycleYearly, StartsOn: startsAugust, Active: true,
			},
			{
				ID: 3, Name: "September annual", CostClass: BusinessCostClassOperating,
				Category: "domain", AmountMinor: 99_000, Currency: "CNY",
				BillingCycle: BusinessBillingCycleYearly, StartsOn: startsSeptember, Active: true,
			},
			{
				ID: 4, Name: "One-time setup", CostClass: BusinessCostClassOperating,
				Category: "setup", AmountMinor: 5_000, Currency: "CNY",
				BillingCycle: BusinessBillingCycleOneTime, StartsOn: oneTimeAugust, Active: true,
			},
		},
		ExchangeRates: []BusinessExchangeRate{
			{Month: businessMonthStart(asOf), Currency: "USD", RateScaled: 6_750_000},
		},
	})

	require.Equal(t, int64(101_250), report.Summary.DirectCostCents)
	require.Equal(t, int64(14_250), report.Summary.OperatingCostCents)
	require.True(t, report.Summary.CostsComplete)
	require.Len(t, businessItemsByType(report.Items, BusinessItemCostOperating), 3)
}

func TestCalculateBusinessReportMissingRateAndZeroRevenue(t *testing.T) {
	initBusinessTestTimezone(t)
	asOf := businessTestTime(2026, time.August, 8, 12, 0)
	report := CalculateBusinessReport(asOf, &BusinessSourceBundle{
		Costs: []BusinessCostItem{
			businessTestCost(1, "Paid USD", BusinessCostClassDirect, 15_000, "USD"),
			businessTestCost(2, "Operating", BusinessCostClassOperating, 1_000, "CNY"),
			{
				ID: 3, Name: "Free USD", CostClass: BusinessCostClassDirect,
				Category: "subscription", Currency: "USD", AmountMinor: 0,
				BillingCycle: BusinessBillingCycleMonthly, StartsOn: asOf, Active: true, IsFree: true,
			},
		},
	})

	require.False(t, report.Summary.CostsComplete)
	require.Equal(t, int64(0), report.Summary.GrossMarginBPS)
	require.Equal(t, int64(0), report.Summary.NetMarginBPS)
	requireBusinessIssueTypes(t, report.Issues, BusinessIssueMissingExchangeRate)
	require.Empty(t, businessItemsByName(report.Items, "Free USD"))
}

func TestBusinessServiceHistoryAppendsCurrentWithoutFuturePoints(t *testing.T) {
	initBusinessTestTimezone(t)
	now := businessTestTime(2026, time.August, 8, 12, 0)
	repo := &businessRepositoryStub{
		bundle: &BusinessSourceBundle{},
		history: []BusinessHistoryPoint{
			{Month: businessTestTime(2026, time.June, 1, 0, 0), Summary: BusinessSummary{CustomerCount: 3}},
			{Month: businessTestTime(2026, time.July, 1, 0, 0), Summary: BusinessSummary{CustomerCount: 5}},
		},
	}
	svc := NewBusinessService(repo)
	svc.now = func() time.Time { return now }

	points, err := svc.History(context.Background())
	require.NoError(t, err)
	require.Len(t, points, 3)
	require.Equal(t, "2026-08", points[2].Month.Format(businessMonthLayout))
	require.True(t, points[2].IsCurrent)
	require.NotNil(t, points[1].CustomerDelta)
	require.Equal(t, 2, *points[1].CustomerDelta)
	for _, point := range points {
		require.False(t, point.Month.After(businessMonthStart(now)))
	}
}

func TestBusinessServiceCloseMonthGuardsCurrentActualAndMissingRate(t *testing.T) {
	initBusinessTestTimezone(t)
	now := businessTestTime(2026, time.August, 8, 12, 0)
	repo := &businessRepositoryStub{bundle: &BusinessSourceBundle{}}
	svc := NewBusinessService(repo)
	svc.now = func() time.Time { return now }

	_, _, err := svc.CloseMonth(context.Background(), CloseBusinessMonthInput{
		Month:       "2026-08",
		DataQuality: BusinessDataQualityActual,
	})
	require.ErrorIs(t, err, ErrBusinessCurrentMonthClose)
	require.Equal(t, 0, repo.closeCalls)

	_, _, err = svc.CloseMonth(context.Background(), CloseBusinessMonthInput{
		Month:       "2026-07",
		DataQuality: BusinessDataQualityEstimated,
	})
	require.ErrorIs(t, err, ErrBusinessSnapshotNotesRequired)
	require.Equal(t, 0, repo.closeCalls)

	repo.bundle = &BusinessSourceBundle{Costs: []BusinessCostItem{
		businessTestCost(1, "USD", BusinessCostClassDirect, 15_000, "USD"),
	}}
	_, _, err = svc.CloseMonth(context.Background(), CloseBusinessMonthInput{
		Month:       "2026-07",
		DataQuality: BusinessDataQualityActual,
	})
	require.ErrorIs(t, err, ErrBusinessExchangeRateMissing)
	require.Equal(t, 0, repo.closeCalls)
}

type businessRepositoryStub struct {
	bundle     *BusinessSourceBundle
	history    []BusinessHistoryPoint
	snapshot   *BusinessReport
	closeCalls int
	closeErr   error
}

func (r *businessRepositoryStub) LoadSources(context.Context, time.Time, time.Time) (*BusinessSourceBundle, error) {
	return r.bundle, nil
}

func (r *businessRepositoryStub) ListSnapshots(context.Context, time.Time) ([]BusinessHistoryPoint, error) {
	return append([]BusinessHistoryPoint(nil), r.history...), nil
}

func (r *businessRepositoryStub) GetSnapshot(context.Context, time.Time) (*BusinessReport, error) {
	if r.snapshot == nil {
		return nil, ErrBusinessSnapshotNotFound
	}
	return r.snapshot, nil
}

func (r *businessRepositoryStub) CloseSnapshot(_ context.Context, input BusinessSnapshotWrite) (*BusinessReport, bool, error) {
	r.closeCalls++
	if r.closeErr != nil {
		return nil, false, r.closeErr
	}
	if input.Report == nil {
		return nil, false, errors.New("missing report")
	}
	if r.snapshot != nil && sameBusinessMonth(r.snapshot.Month, input.Report.Month) {
		return r.snapshot, false, nil
	}
	r.snapshot = input.Report
	return r.snapshot, true, nil
}

func businessTestKey(id int64, name string, groupID int64, expiresAt time.Time) BusinessAPIKeySource {
	return BusinessAPIKeySource{
		ID:          id,
		Name:        name,
		Status:      "active",
		ExpiresAt:   &expiresAt,
		GroupID:     &groupID,
		GroupName:   "Dedicated",
		GroupStatus: "active",
		UserID:      1,
		UserEmail:   "customer@example.com",
		UserStatus:  "active",
	}
}

func businessTestCost(id int64, name, costClass string, amount int64, currency string) BusinessCostItem {
	return BusinessCostItem{
		ID:           id,
		Name:         name,
		CostClass:    costClass,
		Category:     "test",
		AmountMinor:  amount,
		Currency:     currency,
		BillingCycle: BusinessBillingCycleMonthly,
		StartsOn:     businessTestTime(2026, time.January, 1, 0, 0),
		Active:       true,
	}
}

func businessItemsByType(items []BusinessLineItem, itemType string) []BusinessLineItem {
	result := make([]BusinessLineItem, 0)
	for _, item := range items {
		if item.ItemType == itemType {
			result = append(result, item)
		}
	}
	return result
}

func businessItemsByName(items []BusinessLineItem, name string) []BusinessLineItem {
	result := make([]BusinessLineItem, 0)
	for _, item := range items {
		if item.Name == name {
			result = append(result, item)
		}
	}
	return result
}

func requireBusinessIssueTypes(t *testing.T, issues []BusinessIssue, expected ...string) {
	t.Helper()
	types := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		types[issue.Type] = struct{}{}
	}
	for _, issueType := range expected {
		_, ok := types[issueType]
		require.Truef(t, ok, "missing issue type %s in %#v", issueType, issues)
	}
}

func initBusinessTestTimezone(t *testing.T) {
	t.Helper()
	require.NoError(t, timezone.Init("Asia/Shanghai"))
}

func businessTestTime(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, timezone.Location())
}
