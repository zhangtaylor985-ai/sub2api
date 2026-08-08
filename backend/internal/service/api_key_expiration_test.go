package service

import (
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestResolveAPIKeyExpirationDefaultsToThirtyDays(t *testing.T) {
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)

	expiresAt, err := resolveAPIKeyExpiration(nil, now)

	require.NoError(t, err)
	require.Equal(t, now.AddDate(0, 0, 30), *expiresAt)
}

func TestResolveAPIKeyExpirationRejectsPastTime(t *testing.T) {
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Second)

	_, err := resolveAPIKeyExpiration(&past, now)

	require.Error(t, err)
	require.Equal(t, "API_KEY_EXPIRATION_INVALID", infraerrors.Reason(err))
}

func TestResolveAPIKeyExpirationDaysUsesDefaultAndQuickPresets(t *testing.T) {
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)

	defaultExpiration, err := resolveAPIKeyExpirationDays(nil, now)
	require.NoError(t, err)
	require.Equal(t, now.AddDate(0, 0, 30), *defaultExpiration)

	sevenDays := 7
	quickExpiration, err := resolveAPIKeyExpirationDays(&sevenDays, now)
	require.NoError(t, err)
	require.Equal(t, now.AddDate(0, 0, 7), *quickExpiration)
}

func TestResolveAPIKeyExpirationDaysRejectsInvalidRange(t *testing.T) {
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)

	for _, days := range []int{0, -1, MaxAPIKeyExpirationDays + 1} {
		_, err := resolveAPIKeyExpirationDays(&days, now)
		require.Error(t, err)
		require.Equal(t, "API_KEY_EXPIRATION_DAYS_INVALID", infraerrors.Reason(err))
	}
}
