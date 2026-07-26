package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	privateSubscriptionReminderBatchSize = 500
	privateSubscriptionTelegramTimeout   = 10 * time.Second
)

type PrivateSubscriptionMessageSender interface {
	Enabled() bool
	SendMessage(ctx context.Context, text string) error
}

type telegramBotSender struct {
	token  string
	chatID string
	client *http.Client
}

func NewTelegramBotSender(
	token string,
	chatID string,
	client *http.Client,
) PrivateSubscriptionMessageSender {
	if client == nil {
		client = &http.Client{Timeout: privateSubscriptionTelegramTimeout}
	}
	return &telegramBotSender{
		token:  strings.TrimSpace(token),
		chatID: strings.TrimSpace(chatID),
		client: client,
	}
}

func NewPrivateSubscriptionTelegramSenderFromEnvironment() PrivateSubscriptionMessageSender {
	return NewTelegramBotSender(
		os.Getenv("TELEGRAM_BOT_TOKEN"),
		os.Getenv("TELEGRAM_SUBSCRIPTION_CHAT_ID"),
		&http.Client{Timeout: privateSubscriptionTelegramTimeout},
	)
}

func (s *telegramBotSender) Enabled() bool {
	return s != nil && s.token != "" && s.chatID != ""
}

func (s *telegramBotSender) SendMessage(ctx context.Context, text string) error {
	if !s.Enabled() {
		return fmt.Errorf("telegram subscription reminder is not configured")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("telegram subscription reminder text is empty")
	}

	payload, err := json.Marshal(map[string]any{
		"chat_id":                  s.chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return fmt.Errorf("encode telegram request: %w", err)
	}

	endpoint := "https://api.telegram.org/bot" + s.token + "/sendMessage"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create telegram request: %s", redactTelegramError(err.Error(), s.token))
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("telegram request failed: %s", redactTelegramError(err.Error(), s.token))
	}
	defer func() { _ = response.Body.Close() }()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	if readErr != nil {
		return fmt.Errorf("read telegram response: %s", redactTelegramError(readErr.Error(), s.token))
	}

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(body, &result)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || !result.OK {
		description := strings.TrimSpace(result.Description)
		if description == "" {
			description = http.StatusText(response.StatusCode)
		}
		return fmt.Errorf(
			"telegram send failed: status=%d description=%s",
			response.StatusCode,
			redactTelegramError(description, s.token),
		)
	}
	return nil
}

func redactTelegramError(value, token string) string {
	value = strings.TrimSpace(value)
	if token != "" {
		value = strings.ReplaceAll(
			value,
			"https://api.telegram.org/bot"+token,
			"telegram-api",
		)
		value = strings.ReplaceAll(value, token, "[REDACTED]")
	}
	value = strings.ReplaceAll(value, "https://api.telegram.org", "telegram-api")
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

type PrivateSubscriptionReminderRunResult struct {
	Due    int
	Sent   int
	Failed int
}

type PrivateSubscriptionReminderService struct {
	repo     PrivateSubscriptionRepository
	sender   PrivateSubscriptionMessageSender
	interval time.Duration
	now      func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	runMu     sync.Mutex
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func NewPrivateSubscriptionReminderService(
	repo PrivateSubscriptionRepository,
	sender PrivateSubscriptionMessageSender,
	interval time.Duration,
) *PrivateSubscriptionReminderService {
	return &PrivateSubscriptionReminderService{
		repo:     repo,
		sender:   sender,
		interval: interval,
		now:      timezone.Now,
		stopCh:   make(chan struct{}),
	}
}

func (s *PrivateSubscriptionReminderService) Start() {
	if s == nil || s.repo == nil || s.sender == nil || !s.sender.Enabled() || s.interval <= 0 {
		log.Printf("[PrivateSubscriptionReminder] Disabled: Telegram subscription channel is not configured")
		return
	}

	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(s.interval)
			defer ticker.Stop()

			s.runAndLog()
			for {
				select {
				case <-ticker.C:
					s.runAndLog()
				case <-s.stopCh:
					return
				}
			}
		}()
		log.Printf("[PrivateSubscriptionReminder] Started")
	})
}

func (s *PrivateSubscriptionReminderService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.wg.Wait()
}

func (s *PrivateSubscriptionReminderService) runAndLog() {
	ctx, cancel := context.WithTimeout(context.Background(), privateSubscriptionTelegramTimeout*time.Duration(2))
	defer cancel()

	result, err := s.runOnce(ctx)
	if err != nil {
		log.Printf("[PrivateSubscriptionReminder] Scan failed: %v", err)
		return
	}
	if result.Due > 0 || result.Failed > 0 {
		log.Printf(
			"[PrivateSubscriptionReminder] Completed: due=%d sent=%d failed=%d",
			result.Due,
			result.Sent,
			result.Failed,
		)
	}
}

func (s *PrivateSubscriptionReminderService) runOnce(
	ctx context.Context,
) (PrivateSubscriptionReminderRunResult, error) {
	var result PrivateSubscriptionReminderRunResult
	if s == nil || s.repo == nil || s.sender == nil || !s.sender.Enabled() {
		return result, nil
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	now := s.now()
	targetExpiry := normalizeCalendarDate(now.AddDate(0, 0, 1))
	subscriptions, err := s.repo.ListDueForReminder(
		ctx,
		targetExpiry,
		privateSubscriptionReminderBatchSize,
	)
	if err != nil {
		return result, fmt.Errorf("list due private subscriptions: %w", err)
	}

	result.Due = len(subscriptions)
	for i := range subscriptions {
		subscription := &subscriptions[i]
		message := formatPrivateSubscriptionReminder(subscription)
		if err := s.sender.SendMessage(ctx, message); err != nil {
			result.Failed++
			log.Printf(
				"[PrivateSubscriptionReminder] Send failed: subscription_id=%d err=%v",
				subscription.ID,
				err,
			)
			continue
		}

		marked, err := s.repo.MarkReminderSent(
			ctx,
			subscription.ID,
			subscription.ExpiresOn,
			now,
		)
		if err != nil {
			result.Failed++
			log.Printf(
				"[PrivateSubscriptionReminder] Mark sent failed: subscription_id=%d err=%v",
				subscription.ID,
				err,
			)
			continue
		}
		if !marked {
			result.Failed++
			log.Printf(
				"[PrivateSubscriptionReminder] Mark sent skipped: subscription_id=%d record changed",
				subscription.ID,
			)
			continue
		}
		result.Sent++
	}
	return result, nil
}

func formatPrivateSubscriptionReminder(subscription *PrivateSubscription) string {
	if subscription == nil {
		return ""
	}
	return strings.Join([]string{
		"⏰ 客户订阅到期提醒",
		"",
		"客户：" + subscription.Name,
		"订阅类型：" + subscription.SubscriptionType,
		"金额：" + formatCNYCents(subscription.AmountCents),
		"到期日期：" + subscription.ExpiresOn.Format(privateSubscriptionDateLayout),
		"剩余：1 天",
		"",
		"请及时联系客户确认续订。",
	}, "\n")
}

func formatCNYCents(amountCents int64) string {
	sign := ""
	if amountCents < 0 {
		sign = "-"
		amountCents = -amountCents
	}
	yuan := amountCents / 100
	cents := amountCents % 100
	return sign + "¥" + formatIntegerWithCommas(yuan) + "." + fmt.Sprintf("%02d", cents)
}

func formatIntegerWithCommas(value int64) string {
	raw := strconv.FormatInt(value, 10)
	if len(raw) <= 3 {
		return raw
	}
	first := len(raw) % 3
	if first == 0 {
		first = 3
	}
	var builder strings.Builder
	builder.Grow(len(raw) + len(raw)/3)
	builder.WriteString(raw[:first])
	for i := first; i < len(raw); i += 3 {
		builder.WriteByte(',')
		builder.WriteString(raw[i : i+3])
	}
	return builder.String()
}
