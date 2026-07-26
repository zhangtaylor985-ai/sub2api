package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	PrivateSubscriptionStatusActive  = "active"
	PrivateSubscriptionStatusDueSoon = "due_soon"
	PrivateSubscriptionStatusExpired = "expired"

	privateSubscriptionDateLayout     = "2006-01-02"
	privateSubscriptionDueSoonDays    = 7
	privateSubscriptionMaxAmountCents = int64(99_999_999_999)
)

var (
	ErrPrivateSubscriptionNotFound = infraerrors.NotFound(
		"PRIVATE_SUBSCRIPTION_NOT_FOUND",
		"private subscription not found",
	)
	ErrPrivateSubscriptionInputRequired = infraerrors.BadRequest(
		"PRIVATE_SUBSCRIPTION_INPUT_REQUIRED",
		"private subscription input is required",
	)
	ErrPrivateSubscriptionNameInvalid = infraerrors.BadRequest(
		"PRIVATE_SUBSCRIPTION_NAME_INVALID",
		"name is required and must not exceed 120 characters",
	)
	ErrPrivateSubscriptionTypeInvalid = infraerrors.BadRequest(
		"PRIVATE_SUBSCRIPTION_TYPE_INVALID",
		"subscription_type is required and must not exceed 50 characters",
	)
	ErrPrivateSubscriptionAmountInvalid = infraerrors.BadRequest(
		"PRIVATE_SUBSCRIPTION_AMOUNT_INVALID",
		"amount_cents must be between 0 and 99999999999",
	)
	ErrPrivateSubscriptionExpiryInvalid = infraerrors.BadRequest(
		"PRIVATE_SUBSCRIPTION_EXPIRY_INVALID",
		"expires_on must be a valid date in YYYY-MM-DD format",
	)
	ErrPrivateSubscriptionStatusInvalid = infraerrors.BadRequest(
		"PRIVATE_SUBSCRIPTION_STATUS_INVALID",
		"status must be active, due_soon, or expired",
	)
)

// PrivateSubscription is an operator-maintained customer record that has no
// relation to Sub2API's billing subscription domain.
type PrivateSubscription struct {
	ID                    int64
	Name                  string
	SubscriptionType      string
	AmountCents           int64
	ExpiresOn             time.Time
	ReminderSentForExpiry *time.Time
	ReminderSentAt        *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (s *PrivateSubscription) DaysRemainingAt(today time.Time) int {
	if s == nil {
		return 0
	}
	return calendarDayDifference(today, s.ExpiresOn)
}

func (s *PrivateSubscription) StatusAt(today time.Time) string {
	days := s.DaysRemainingAt(today)
	switch {
	case days < 0:
		return PrivateSubscriptionStatusExpired
	case days <= privateSubscriptionDueSoonDays:
		return PrivateSubscriptionStatusDueSoon
	default:
		return PrivateSubscriptionStatusActive
	}
}

func (s *PrivateSubscription) ReminderSentForCurrentExpiry() bool {
	return s != nil &&
		s.ReminderSentForExpiry != nil &&
		sameCalendarDate(*s.ReminderSentForExpiry, s.ExpiresOn)
}

type PrivateSubscriptionListFilters struct {
	Search           string
	Status           string
	SubscriptionType string
}

type PrivateSubscriptionSummary struct {
	Total            int64 `json:"total"`
	Active           int64 `json:"active"`
	DueSoon          int64 `json:"due_soon"`
	Expired          int64 `json:"expired"`
	TotalAmountCents int64 `json:"total_amount_cents"`
}

type PrivateSubscriptionRepository interface {
	Create(ctx context.Context, subscription *PrivateSubscription) error
	GetByID(ctx context.Context, id int64) (*PrivateSubscription, error)
	Update(ctx context.Context, subscription *PrivateSubscription) error
	Delete(ctx context.Context, id int64) error
	List(
		ctx context.Context,
		params pagination.PaginationParams,
		filters PrivateSubscriptionListFilters,
		today time.Time,
	) ([]PrivateSubscription, *pagination.PaginationResult, error)
	Summary(ctx context.Context, today time.Time) (*PrivateSubscriptionSummary, error)
	ListDueForReminder(ctx context.Context, expiresOn time.Time, limit int) ([]PrivateSubscription, error)
	MarkReminderSent(ctx context.Context, id int64, expiresOn, sentAt time.Time) (bool, error)
}

type CreatePrivateSubscriptionInput struct {
	Name             string
	SubscriptionType string
	AmountCents      int64
	ExpiresOn        string
}

type UpdatePrivateSubscriptionInput struct {
	Name             *string
	SubscriptionType *string
	AmountCents      *int64
	ExpiresOn        *string
}

type PrivateSubscriptionService struct {
	repo PrivateSubscriptionRepository
}

func NewPrivateSubscriptionService(repo PrivateSubscriptionRepository) *PrivateSubscriptionService {
	return &PrivateSubscriptionService{repo: repo}
}

func (s *PrivateSubscriptionService) Create(
	ctx context.Context,
	input *CreatePrivateSubscriptionInput,
) (*PrivateSubscription, error) {
	if input == nil {
		return nil, ErrPrivateSubscriptionInputRequired
	}

	name, err := normalizePrivateSubscriptionName(input.Name)
	if err != nil {
		return nil, err
	}
	subscriptionType, err := normalizePrivateSubscriptionType(input.SubscriptionType)
	if err != nil {
		return nil, err
	}
	if err := validatePrivateSubscriptionAmount(input.AmountCents); err != nil {
		return nil, err
	}
	expiresOn, err := parsePrivateSubscriptionDate(input.ExpiresOn)
	if err != nil {
		return nil, err
	}

	subscription := &PrivateSubscription{
		Name:             name,
		SubscriptionType: subscriptionType,
		AmountCents:      input.AmountCents,
		ExpiresOn:        expiresOn,
	}
	if err := s.repo.Create(ctx, subscription); err != nil {
		return nil, fmt.Errorf("create private subscription: %w", err)
	}
	return subscription, nil
}

func (s *PrivateSubscriptionService) GetByID(ctx context.Context, id int64) (*PrivateSubscription, error) {
	subscription, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get private subscription: %w", err)
	}
	return subscription, nil
}

func (s *PrivateSubscriptionService) Update(
	ctx context.Context,
	id int64,
	input *UpdatePrivateSubscriptionInput,
) (*PrivateSubscription, error) {
	if input == nil {
		return nil, ErrPrivateSubscriptionInputRequired
	}

	subscription, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get private subscription: %w", err)
	}

	if input.Name != nil {
		name, err := normalizePrivateSubscriptionName(*input.Name)
		if err != nil {
			return nil, err
		}
		subscription.Name = name
	}
	if input.SubscriptionType != nil {
		subscriptionType, err := normalizePrivateSubscriptionType(*input.SubscriptionType)
		if err != nil {
			return nil, err
		}
		subscription.SubscriptionType = subscriptionType
	}
	if input.AmountCents != nil {
		if err := validatePrivateSubscriptionAmount(*input.AmountCents); err != nil {
			return nil, err
		}
		subscription.AmountCents = *input.AmountCents
	}
	if input.ExpiresOn != nil {
		expiresOn, err := parsePrivateSubscriptionDate(*input.ExpiresOn)
		if err != nil {
			return nil, err
		}
		if !sameCalendarDate(subscription.ExpiresOn, expiresOn) {
			subscription.ExpiresOn = expiresOn
			subscription.ReminderSentForExpiry = nil
			subscription.ReminderSentAt = nil
		}
	}

	if err := s.repo.Update(ctx, subscription); err != nil {
		return nil, fmt.Errorf("update private subscription: %w", err)
	}
	return subscription, nil
}

func (s *PrivateSubscriptionService) Delete(ctx context.Context, id int64) error {
	if _, err := s.repo.GetByID(ctx, id); err != nil {
		return fmt.Errorf("get private subscription: %w", err)
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete private subscription: %w", err)
	}
	return nil
}

func (s *PrivateSubscriptionService) List(
	ctx context.Context,
	params pagination.PaginationParams,
	filters PrivateSubscriptionListFilters,
) ([]PrivateSubscription, *pagination.PaginationResult, error) {
	filters.Search = strings.TrimSpace(filters.Search)
	searchRunes := []rune(filters.Search)
	if len(searchRunes) > 120 {
		filters.Search = string(searchRunes[:120])
	}
	filters.Status = strings.ToLower(strings.TrimSpace(filters.Status))
	if filters.Status != "" && !isValidPrivateSubscriptionStatus(filters.Status) {
		return nil, nil, ErrPrivateSubscriptionStatusInvalid
	}
	filters.SubscriptionType = strings.TrimSpace(filters.SubscriptionType)
	if len(filters.SubscriptionType) > 50 {
		return nil, nil, ErrPrivateSubscriptionTypeInvalid
	}

	subscriptions, page, err := s.repo.List(ctx, params, filters, timezone.Today())
	if err != nil {
		return nil, nil, fmt.Errorf("list private subscriptions: %w", err)
	}
	return subscriptions, page, nil
}

func (s *PrivateSubscriptionService) Summary(ctx context.Context) (*PrivateSubscriptionSummary, error) {
	summary, err := s.repo.Summary(ctx, timezone.Today())
	if err != nil {
		return nil, fmt.Errorf("summarize private subscriptions: %w", err)
	}
	return summary, nil
}

func normalizePrivateSubscriptionName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 120 {
		return "", ErrPrivateSubscriptionNameInvalid
	}
	return value, nil
}

func normalizePrivateSubscriptionType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 50 {
		return "", ErrPrivateSubscriptionTypeInvalid
	}
	return value, nil
}

func validatePrivateSubscriptionAmount(value int64) error {
	if value < 0 || value > privateSubscriptionMaxAmountCents {
		return ErrPrivateSubscriptionAmountInvalid
	}
	return nil
}

func parsePrivateSubscriptionDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	parsed, err := timezone.ParseInLocation(privateSubscriptionDateLayout, value)
	if err != nil || parsed.Format(privateSubscriptionDateLayout) != value {
		return time.Time{}, ErrPrivateSubscriptionExpiryInvalid
	}
	return normalizeCalendarDate(parsed), nil
}

func isValidPrivateSubscriptionStatus(value string) bool {
	switch value {
	case PrivateSubscriptionStatusActive,
		PrivateSubscriptionStatusDueSoon,
		PrivateSubscriptionStatusExpired:
		return true
	default:
		return false
	}
}

func normalizeCalendarDate(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, timezone.Location())
}

func sameCalendarDate(left, right time.Time) bool {
	return left.Year() == right.Year() &&
		left.Month() == right.Month() &&
		left.Day() == right.Day()
}

func calendarDayDifference(from, to time.Time) int {
	fromDate := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	toDate := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	return int(toDate.Sub(fromDate) / (24 * time.Hour))
}
