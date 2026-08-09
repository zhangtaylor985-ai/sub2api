//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBusinessRepositoryCloseSnapshotIsIdempotentAndPreservesDetail(t *testing.T) {
	ctx := context.Background()
	month := time.Date(2098, time.January, 1, 0, 0, 0, 0, time.UTC)
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM business_monthly_snapshots WHERE month = $1::date", month)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM business_monthly_snapshots WHERE month = $1::date", month)
	})

	repo := NewBusinessRepository(integrationDB).(*businessRepository)
	linkedAPIKeyID := int64(42)
	sourceID := int64(84)
	reason := "Explicit customer subscription link is authoritative."
	closedAt := time.Date(2098, time.February, 1, 0, 5, 0, 0, time.UTC)
	report := &service.BusinessReport{
		Month: month,
		AsOf:  month.AddDate(0, 1, 0).Add(-time.Nanosecond),
		Summary: service.BusinessSummary{
			APIKeyCount:                     2,
			PrivateSubscriptionCount:        1,
			CustomerCount:                   3,
			ExcludedAPIKeyCount:             1,
			APIKeyRevenueCents:              219_000,
			PrivateSubscriptionRevenueCents: 36_500,
			TotalRevenueCents:               255_500,
			DirectCostCents:                 101_250,
			OperatingCostCents:              10_000,
			GrossProfitCents:                154_250,
			NetProfitCents:                  144_250,
			GrossMarginBPS:                  6_037,
			NetMarginBPS:                    5_645,
			CostsComplete:                   true,
			AnomalyCount:                    2,
		},
		Items: []service.BusinessLineItem{{
			ItemType:            service.BusinessItemRevenuePrivateSubscription,
			SourceType:          service.BusinessSourcePrivateSubscription,
			SourceID:            &sourceID,
			Name:                "Customer A",
			OriginalAmountMinor: 36_500,
			Currency:            service.BusinessCurrencyCNY,
			RateScaled:          service.BusinessRateScale,
			AmountCNYCents:      36_500,
			Reason:              &reason,
			Included:            true,
			LinkedAPIKeyID:      &linkedAPIKeyID,
			GroupName:           "4人车",
			UserEmail:           "customer@example.com",
		}},
	}

	first, created, err := repo.CloseSnapshot(ctx, service.BusinessSnapshotWrite{
		Report: report, DataQuality: service.BusinessDataQualityEstimated, ClosedAt: closedAt,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 1, first.Summary.ExcludedAPIKeyCount)
	require.Equal(t, 2, first.Summary.AnomalyCount)
	require.Len(t, first.Items, 1)
	require.True(t, first.Items[0].Included)
	require.Equal(t, linkedAPIKeyID, *first.Items[0].LinkedAPIKeyID)
	require.Equal(t, "4人车", first.Items[0].GroupName)
	require.Equal(t, "customer@example.com", first.Items[0].UserEmail)

	second, created, err := repo.CloseSnapshot(ctx, service.BusinessSnapshotWrite{
		Report: report, DataQuality: service.BusinessDataQualityManual, ClosedAt: closedAt.Add(time.Hour),
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, service.BusinessDataQualityEstimated, second.DataQuality)

	var snapshotCount, itemCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM business_monthly_snapshots WHERE month = $1::date", month,
	).Scan(&snapshotCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM business_monthly_snapshot_items WHERE snapshot_id = $1", first.ID,
	).Scan(&itemCount))
	require.Equal(t, 1, snapshotCount)
	require.Equal(t, 1, itemCount)
}

func TestBusinessRepositoryInitializeDefaultsUsesTypedAccountIdentifier(t *testing.T) {
	ctx := context.Background()
	month := time.Date(2098, time.March, 1, 0, 0, 0, 0, time.UTC)
	identifier := "business-init-regression@example.com"
	cleanup := func() {
		_, _ = integrationDB.ExecContext(ctx,
			"DELETE FROM business_cost_items WHERE account_identifier = $1", identifier,
		)
		_, _ = integrationDB.ExecContext(ctx,
			"DELETE FROM business_exchange_rates WHERE month = $1::date AND currency = 'USD'", month,
		)
	}
	cleanup()
	t.Cleanup(cleanup)

	repo := NewBusinessRepository(integrationDB).(*businessRepository)
	defaults := service.BusinessDefaultInitialization{
		Month: month,
		Costs: []service.BusinessDefaultCost{{
			Name:              "Business initialization regression subscription",
			AccountIdentifier: identifier,
			AmountMinor:       15_000,
			Currency:          "USD",
		}},
		USDRateScaled: 6_750_000,
	}

	first, err := repo.InitializeDefaults(ctx, defaults)
	require.NoError(t, err)
	require.Equal(t, 1, first.CostsCreated)
	require.True(t, first.ExchangeRateCreated)

	second, err := repo.InitializeDefaults(ctx, defaults)
	require.NoError(t, err)
	require.Equal(t, 0, second.CostsCreated)
	require.Equal(t, 1, second.CostsExisting)
	require.False(t, second.ExchangeRateCreated)

	var costCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM business_cost_items WHERE account_identifier = $1", identifier,
	).Scan(&costCount))
	require.Equal(t, 1, costCount)
}
