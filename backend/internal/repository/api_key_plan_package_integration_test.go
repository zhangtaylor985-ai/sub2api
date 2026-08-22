//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyPlanPackages_StackRenewExpireAndIdempotency(t *testing.T) {
	ctx := context.Background()
	repo := NewAPIKeyRepository(integrationEntClient, integrationDB).(*apiKeyRepository)
	suffix := time.Now().UTC().Format("20060102T150405.000000000")

	user, err := integrationEntClient.User.Create().
		SetEmail(fmt.Sprintf("plan-stack-%s@test.local", suffix)).
		SetPasswordHash("test-hash").
		SetStatus(service.StatusActive).
		SetRole(service.RoleUser).
		Save(ctx)
	require.NoError(t, err)

	double, err := integrationEntClient.Group.Create().
		SetName("Double " + suffix).
		SetStatus(service.StatusActive).
		SetDailyLimitUsd(150).
		SetWeeklyLimitUsd(500).
		SetConcurrency(2).
		Save(ctx)
	require.NoError(t, err)

	triple, err := integrationEntClient.Group.Create().
		SetName("Triple " + suffix).
		SetStatus(service.StatusActive).
		SetDailyLimitUsd(100).
		SetWeeklyLimitUsd(330).
		SetConcurrency(3).
		Save(ctx)
	require.NoError(t, err)

	var keyIDs []int64
	t.Cleanup(func() {
		for _, keyID := range keyIDs {
			_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", keyID)
		}
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", double.ID, triple.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})

	createLegacyDoubleKey := func(t *testing.T, name string, legacyExpiry time.Time) *service.APIKey {
		t.Helper()
		key := &service.APIKey{
			UserID:    user.ID,
			GroupID:   &double.ID,
			Key:       fmt.Sprintf("sk-plan-%s-%s", name, suffix),
			Name:      name,
			Status:    service.StatusActive,
			ExpiresAt: &legacyExpiry,
		}
		require.NoError(t, repo.Create(ctx, key))
		keyIDs = append(keyIDs, key.ID)
		return key
	}

	t.Run("different plans stack immediately and expired contribution is removed", func(t *testing.T) {
		legacyExpiry := time.Now().UTC().Add(10 * 24 * time.Hour)
		key := createLegacyDoubleKey(t, "different-plan", legacyExpiry)
		now := time.Now().UTC()

		added, err := repo.AddPlanPackage(ctx, service.AddAPIKeyPlanPackageInput{
			APIKeyID:  key.ID,
			GroupID:   triple.ID,
			RequestID: "different-plan-purchase",
			Months:    1,
			CreatedBy: "integration-test",
			Now:       now,
		})
		require.NoError(t, err)
		require.False(t, added.Idempotent)
		require.Equal(t, now, added.Package.StartsAt)
		require.Equal(t, service.AddCalendarMonthsClamped(now, 1), added.Package.ExpiresAt)
		require.Equal(t, 2, added.Summary.ActiveCount)
		require.InDelta(t, 250, added.Summary.DailyLimitUSD, 0.000001)
		require.InDelta(t, 830, added.Summary.WeeklyLimitUSD, 0.000001)
		require.Equal(t, 5, added.Summary.Concurrency)

		afterDoubleExpiry, err := repo.GetPlanPackageSummary(ctx, key.ID, legacyExpiry.Add(time.Second))
		require.NoError(t, err)
		require.Equal(t, 1, afterDoubleExpiry.ActiveCount)
		require.InDelta(t, 100, afterDoubleExpiry.DailyLimitUSD, 0.000001)
		require.InDelta(t, 330, afterDoubleExpiry.WeeklyLimitUSD, 0.000001)
		require.Equal(t, 3, afterDoubleExpiry.Concurrency)

		storedKey, err := repo.GetByID(ctx, key.ID)
		require.NoError(t, err)
		require.NotNil(t, storedKey.ExpiresAt)
		require.True(t, storedKey.ExpiresAt.Equal(added.Package.ExpiresAt))
	})

	t.Run("same plan renewal is queued and duplicate request is idempotent", func(t *testing.T) {
		legacyExpiry := time.Now().UTC().Add(10 * 24 * time.Hour)
		key := createLegacyDoubleKey(t, "same-plan", legacyExpiry)
		now := time.Now().UTC()
		input := service.AddAPIKeyPlanPackageInput{
			APIKeyID:  key.ID,
			GroupID:   double.ID,
			RequestID: "same-plan-purchase",
			Months:    1,
			CreatedBy: "integration-test",
			Now:       now,
		}

		added, err := repo.AddPlanPackage(ctx, input)
		require.NoError(t, err)
		require.Equal(t, legacyExpiry, added.Package.StartsAt)
		require.Equal(t, service.AddCalendarMonthsClamped(legacyExpiry, 1), added.Package.ExpiresAt)
		require.Equal(t, 1, added.Summary.ActiveCount)
		require.InDelta(t, 150, added.Summary.DailyLimitUSD, 0.000001)
		require.InDelta(t, 500, added.Summary.WeeklyLimitUSD, 0.000001)
		require.Equal(t, 2, added.Summary.Concurrency)

		duplicate, err := repo.AddPlanPackage(ctx, input)
		require.NoError(t, err)
		require.True(t, duplicate.Idempotent)
		require.Equal(t, added.Package.ID, duplicate.Package.ID)

		mismatched := input
		mismatched.Months = 2
		_, err = repo.AddPlanPackage(ctx, mismatched)
		require.ErrorIs(t, err, service.ErrAPIKeyPlanPackageInvalid)

		packages, err := repo.ListPlanPackages(ctx, key.ID, 100, now)
		require.NoError(t, err)
		require.Len(t, packages, 2, "legacy baseline plus one renewal should exist")
		require.True(t, packages[0].IsActive)
		require.True(t, packages[1].IsUpcoming)
	})
}
