package service

import "time"

const (
	DefaultAPIKeyExpirationDays = 30
	MaxAPIKeyExpirationDays     = 3650
)

// DefaultAPIKeyExpiresAt returns the mandatory default expiration for a new API key.
func DefaultAPIKeyExpiresAt(now time.Time) time.Time {
	return now.AddDate(0, 0, DefaultAPIKeyExpirationDays)
}

func resolveAPIKeyExpiration(expiresAt *time.Time, now time.Time) (*time.Time, error) {
	if expiresAt == nil {
		value := DefaultAPIKeyExpiresAt(now)
		return &value, nil
	}
	if !expiresAt.After(now) {
		return nil, ErrAPIKeyExpirationInvalid
	}
	value := *expiresAt
	return &value, nil
}

func resolveAPIKeyExpirationDays(days *int, now time.Time) (*time.Time, error) {
	value := DefaultAPIKeyExpirationDays
	if days != nil {
		value = *days
	}
	if value < 1 || value > MaxAPIKeyExpirationDays {
		return nil, ErrAPIKeyExpirationDaysInvalid
	}
	expiresAt := now.AddDate(0, 0, value)
	return &expiresAt, nil
}
