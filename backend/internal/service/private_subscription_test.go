package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type privateSubscriptionRepoStub struct {
	item         *PrivateSubscription
	created      *PrivateSubscription
	updated      *PrivateSubscription
	deletedID    int64
	listFilters  PrivateSubscriptionListFilters
	listToday    time.Time
	summary      *PrivateSubscriptionSummary
	due          []PrivateSubscription
	dueExpiry    time.Time
	markCalls    int
	markResult   bool
	markErr      error
	markedID     int64
	markedExpiry time.Time
	markedSentAt time.Time
}

func (r *privateSubscriptionRepoStub) Create(
	_ context.Context,
	subscription *PrivateSubscription,
) error {
	subscription.ID = 42
	r.created = clonePrivateSubscription(subscription)
	return nil
}

func (r *privateSubscriptionRepoStub) GetByID(
	context.Context,
	int64,
) (*PrivateSubscription, error) {
	if r.item == nil {
		return nil, ErrPrivateSubscriptionNotFound
	}
	return clonePrivateSubscription(r.item), nil
}

func (r *privateSubscriptionRepoStub) Update(
	_ context.Context,
	subscription *PrivateSubscription,
) error {
	r.updated = clonePrivateSubscription(subscription)
	return nil
}

func (r *privateSubscriptionRepoStub) Delete(_ context.Context, id int64) error {
	r.deletedID = id
	return nil
}

func (r *privateSubscriptionRepoStub) List(
	_ context.Context,
	_ pagination.PaginationParams,
	filters PrivateSubscriptionListFilters,
	today time.Time,
) ([]PrivateSubscription, *pagination.PaginationResult, error) {
	r.listFilters = filters
	r.listToday = today
	return nil, &pagination.PaginationResult{Page: 1, PageSize: 20, Total: 0, Pages: 1}, nil
}

func (r *privateSubscriptionRepoStub) Summary(
	context.Context,
	time.Time,
) (*PrivateSubscriptionSummary, error) {
	if r.summary == nil {
		return &PrivateSubscriptionSummary{}, nil
	}
	copy := *r.summary
	return &copy, nil
}

func (r *privateSubscriptionRepoStub) ListDueForReminder(
	_ context.Context,
	expiresOn time.Time,
	_ int,
) ([]PrivateSubscription, error) {
	r.dueExpiry = expiresOn
	return append([]PrivateSubscription(nil), r.due...), nil
}

func (r *privateSubscriptionRepoStub) MarkReminderSent(
	_ context.Context,
	id int64,
	expiresOn, sentAt time.Time,
) (bool, error) {
	r.markCalls++
	r.markedID = id
	r.markedExpiry = expiresOn
	r.markedSentAt = sentAt
	if r.markErr != nil {
		return false, r.markErr
	}
	return r.markResult, nil
}

func clonePrivateSubscription(input *PrivateSubscription) *PrivateSubscription {
	if input == nil {
		return nil
	}
	copy := *input
	if input.ReminderSentForExpiry != nil {
		value := *input.ReminderSentForExpiry
		copy.ReminderSentForExpiry = &value
	}
	if input.ReminderSentAt != nil {
		value := *input.ReminderSentAt
		copy.ReminderSentAt = &value
	}
	return &copy
}

func TestPrivateSubscriptionServiceCreateNormalizesInput(t *testing.T) {
	repo := &privateSubscriptionRepoStub{}
	svc := NewPrivateSubscriptionService(repo)

	item, err := svc.Create(context.Background(), &CreatePrivateSubscriptionInput{
		Name:             "  张三  ",
		SubscriptionType: " 20X ",
		AmountCents:      123_456,
		ExpiresOn:        "2026-08-26",
	})

	require.NoError(t, err)
	require.Equal(t, int64(42), item.ID)
	require.Equal(t, "张三", repo.created.Name)
	require.Equal(t, "20X", repo.created.SubscriptionType)
	require.Equal(t, int64(123_456), repo.created.AmountCents)
	require.Equal(t, "2026-08-26", repo.created.ExpiresOn.Format(privateSubscriptionDateLayout))
}

func TestPrivateSubscriptionServiceCreateValidation(t *testing.T) {
	tests := []struct {
		name  string
		input *CreatePrivateSubscriptionInput
		want  error
	}{
		{
			name: "nil input",
			want: ErrPrivateSubscriptionInputRequired,
		},
		{
			name: "blank name",
			input: &CreatePrivateSubscriptionInput{
				SubscriptionType: "5X",
				ExpiresOn:        "2026-08-26",
			},
			want: ErrPrivateSubscriptionNameInvalid,
		},
		{
			name: "blank type",
			input: &CreatePrivateSubscriptionInput{
				Name:      "Alice",
				ExpiresOn: "2026-08-26",
			},
			want: ErrPrivateSubscriptionTypeInvalid,
		},
		{
			name: "negative amount",
			input: &CreatePrivateSubscriptionInput{
				Name:             "Alice",
				SubscriptionType: "5X",
				AmountCents:      -1,
				ExpiresOn:        "2026-08-26",
			},
			want: ErrPrivateSubscriptionAmountInvalid,
		},
		{
			name: "invalid date",
			input: &CreatePrivateSubscriptionInput{
				Name:             "Alice",
				SubscriptionType: "5X",
				ExpiresOn:        "2026-02-30",
			},
			want: ErrPrivateSubscriptionExpiryInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := NewPrivateSubscriptionService(&privateSubscriptionRepoStub{})
			_, err := svc.Create(context.Background(), test.input)
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestPrivateSubscriptionServiceUpdateExpiryResetsReminder(t *testing.T) {
	oldExpiry := time.Date(2026, 8, 26, 0, 0, 0, 0, time.Local)
	sentAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.Local)
	repo := &privateSubscriptionRepoStub{
		item: &PrivateSubscription{
			ID:                    7,
			Name:                  "Alice",
			SubscriptionType:      "5X",
			AmountCents:           50_000,
			ExpiresOn:             oldExpiry,
			ReminderSentForExpiry: &oldExpiry,
			ReminderSentAt:        &sentAt,
		},
	}
	svc := NewPrivateSubscriptionService(repo)
	newExpiry := "2026-09-26"

	item, err := svc.Update(context.Background(), 7, &UpdatePrivateSubscriptionInput{
		ExpiresOn: &newExpiry,
	})

	require.NoError(t, err)
	require.Equal(t, newExpiry, item.ExpiresOn.Format(privateSubscriptionDateLayout))
	require.Nil(t, item.ReminderSentForExpiry)
	require.Nil(t, item.ReminderSentAt)
	require.Nil(t, repo.updated.ReminderSentForExpiry)
	require.Nil(t, repo.updated.ReminderSentAt)
}

func TestPrivateSubscriptionServiceListTruncatesSearchByRunes(t *testing.T) {
	repo := &privateSubscriptionRepoStub{}
	svc := NewPrivateSubscriptionService(repo)

	_, _, err := svc.List(
		context.Background(),
		pagination.PaginationParams{Page: 1, PageSize: 20},
		PrivateSubscriptionListFilters{Search: strings.Repeat("客", 121)},
	)

	require.NoError(t, err)
	require.True(t, utf8.ValidString(repo.listFilters.Search))
	require.Len(t, []rune(repo.listFilters.Search), 120)
}

func TestPrivateSubscriptionStatusBoundaries(t *testing.T) {
	today := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		expiry time.Time
		status string
		days   int
	}{
		{today.AddDate(0, 0, -1), PrivateSubscriptionStatusExpired, -1},
		{today, PrivateSubscriptionStatusDueSoon, 0},
		{today.AddDate(0, 0, 1), PrivateSubscriptionStatusDueSoon, 1},
		{today.AddDate(0, 0, 7), PrivateSubscriptionStatusDueSoon, 7},
		{today.AddDate(0, 0, 8), PrivateSubscriptionStatusActive, 8},
	}

	for _, test := range tests {
		item := &PrivateSubscription{ExpiresOn: test.expiry}
		require.Equal(t, test.status, item.StatusAt(today))
		require.Equal(t, test.days, item.DaysRemainingAt(today))
	}
}

type privateSubscriptionSenderStub struct {
	enabled  bool
	err      error
	messages []string
}

func (s *privateSubscriptionSenderStub) Enabled() bool {
	return s.enabled
}

func (s *privateSubscriptionSenderStub) SendMessage(_ context.Context, text string) error {
	s.messages = append(s.messages, text)
	return s.err
}

func TestPrivateSubscriptionReminderRunOnceSendsAndMarks(t *testing.T) {
	expiry := time.Date(2026, 7, 27, 0, 0, 0, 0, time.Local)
	repo := &privateSubscriptionRepoStub{
		due: []PrivateSubscription{{
			ID:               9,
			Name:             "李四",
			SubscriptionType: "20X",
			AmountCents:      123_456,
			ExpiresOn:        expiry,
		}},
		markResult: true,
	}
	sender := &privateSubscriptionSenderStub{enabled: true}
	svc := NewPrivateSubscriptionReminderService(repo, sender, time.Minute)
	now := time.Date(2026, 7, 26, 8, 30, 0, 0, time.Local)
	svc.now = func() time.Time { return now }

	result, err := svc.runOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, PrivateSubscriptionReminderRunResult{Due: 1, Sent: 1}, result)
	require.Len(t, sender.messages, 1)
	require.Contains(t, sender.messages[0], "客户：李四")
	require.Contains(t, sender.messages[0], "订阅类型：20X")
	require.Contains(t, sender.messages[0], "金额：¥1,234.56")
	require.Contains(t, sender.messages[0], "到期日期：2026-07-27")
	require.Equal(t, 1, repo.markCalls)
	require.Equal(t, int64(9), repo.markedID)
	require.True(t, sameCalendarDate(expiry, repo.markedExpiry))
	require.Equal(t, now, repo.markedSentAt)
	require.Equal(t, "2026-07-27", repo.dueExpiry.Format(privateSubscriptionDateLayout))
}

func TestPrivateSubscriptionReminderSendFailureDoesNotMark(t *testing.T) {
	repo := &privateSubscriptionRepoStub{
		due: []PrivateSubscription{{
			ID:               10,
			Name:             "Alice",
			SubscriptionType: "5X",
			ExpiresOn:        time.Date(2026, 7, 27, 0, 0, 0, 0, time.Local),
		}},
		markResult: true,
	}
	sender := &privateSubscriptionSenderStub{
		enabled: true,
		err:     errors.New("network unavailable"),
	}
	svc := NewPrivateSubscriptionReminderService(repo, sender, time.Minute)
	svc.now = func() time.Time {
		return time.Date(2026, 7, 26, 8, 30, 0, 0, time.Local)
	}

	result, err := svc.runOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, PrivateSubscriptionReminderRunResult{Due: 1, Failed: 1}, result)
	require.Zero(t, repo.markCalls)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestTelegramBotSenderSendsJSON(t *testing.T) {
	var requestBody string
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			requestBody = string(body)
			require.Equal(t, http.MethodPost, request.Method)
			require.Equal(t, "application/json", request.Header.Get("Content-Type"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	sender := NewTelegramBotSender("test-token", "-100123", client)

	err := sender.SendMessage(context.Background(), "hello")

	require.NoError(t, err)
	require.Contains(t, requestBody, `"chat_id":"-100123"`)
	require.Contains(t, requestBody, `"text":"hello"`)
}

func TestTelegramBotSenderRedactsTokenFromTransportErrors(t *testing.T) {
	const token = "top-secret-token"
	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New(
				"request to https://api.telegram.org/bot" + token + "/sendMessage failed",
			)
		}),
	}
	sender := NewTelegramBotSender(token, "-100123", client)

	err := sender.SendMessage(context.Background(), "hello")

	require.Error(t, err)
	require.NotContains(t, err.Error(), token)
	require.NotContains(t, err.Error(), "api.telegram.org")
	require.Contains(t, err.Error(), "telegram-api/sendMessage")
}
