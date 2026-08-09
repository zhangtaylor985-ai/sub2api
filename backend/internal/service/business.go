package service

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	BusinessRateScale = int64(1_000_000)

	BusinessCurrencyCNY = "CNY"

	BusinessCostClassDirect    = "direct"
	BusinessCostClassOperating = "operating"

	BusinessBillingCycleMonthly = "monthly"
	BusinessBillingCycleYearly  = "yearly"
	BusinessBillingCycleOneTime = "one_time"

	BusinessDataQualityActual    = "actual"
	BusinessDataQualityEstimated = "estimated"
	BusinessDataQualityManual    = "manual"

	BusinessSnapshotStatusLocked = "locked"

	BusinessItemRevenueAPIKey              = "revenue_api_key"
	BusinessItemRevenueTokenPackage        = "revenue_token_package"
	BusinessItemRevenuePrivateSubscription = "revenue_private_subscription"
	BusinessItemExcludedAPIKey             = "excluded_api_key"
	BusinessItemCostDirect                 = "cost_direct"
	BusinessItemCostOperating              = "cost_operating"

	BusinessSourceAPIKey              = "api_key"
	BusinessSourceTokenPackage        = "token_package"
	BusinessSourcePrivateSubscription = "private_subscription"
	BusinessSourceCostItem            = "cost_item"
	BusinessSourceSnapshot            = "snapshot"

	BusinessIssueMissingExpiry             = "missing_expiry"
	BusinessIssueExpiredActive             = "expired_active"
	BusinessIssueMissingPricingRule        = "missing_pricing_rule"
	BusinessIssueInactiveOwner             = "inactive_owner"
	BusinessIssueMissingExchangeRate       = "missing_exchange_rate"
	BusinessIssueMissingDirectCosts        = "missing_direct_costs"
	BusinessIssueMissingOperatingCosts     = "missing_operating_costs"
	BusinessIssueLinkedSubscriptionMissing = "linked_subscription_missing"
	BusinessIssueLinkedSubscriptionExpired = "linked_subscription_expired"
	BusinessIssueLinkedExpiryMismatch      = "linked_expiry_mismatch"
	BusinessIssuePossibleDuplicate         = "possible_duplicate"
	BusinessIssueHistoryGap                = "history_gap"

	BusinessIssueSeverityInfo    = "info"
	BusinessIssueSeverityWarning = "warning"
	BusinessIssueSeverityError   = "error"

	businessMonthLayout = "2006-01"
)

var (
	ErrBusinessMonthInvalid = infraerrors.BadRequest(
		"BUSINESS_MONTH_INVALID",
		"month must use YYYY-MM format",
	)
	ErrBusinessSnapshotNotFound = infraerrors.NotFound(
		"BUSINESS_SNAPSHOT_NOT_FOUND",
		"business snapshot not found",
	)
	ErrBusinessFutureMonth = infraerrors.BadRequest(
		"BUSINESS_FUTURE_MONTH",
		"future business months are not available",
	)
	ErrBusinessExchangeRateMissing = infraerrors.Conflict(
		"BUSINESS_EXCHANGE_RATE_MISSING",
		"required exchange rate is missing",
	)
	ErrBusinessCurrentMonthClose = infraerrors.Conflict(
		"BUSINESS_CURRENT_MONTH_CLOSE",
		"the current month cannot be closed as actual before it ends",
	)
	ErrBusinessDataQualityInvalid = infraerrors.BadRequest(
		"BUSINESS_DATA_QUALITY_INVALID",
		"data_quality must be actual, estimated, or manual",
	)
	ErrBusinessSnapshotNotesRequired = infraerrors.BadRequest(
		"BUSINESS_SNAPSHOT_NOTES_REQUIRED",
		"notes are required for estimated or manual business snapshots",
	)
)

// BusinessAPIKeySource contains only non-sensitive fields required by the
// operating report. The raw key is intentionally absent.
type BusinessAPIKeySource struct {
	ID                            int64      `json:"id"`
	Name                          string     `json:"name"`
	Status                        string     `json:"status"`
	CreatedAt                     time.Time  `json:"created_at"`
	ExpiresAt                     *time.Time `json:"expires_at,omitempty"`
	GroupID                       *int64     `json:"group_id,omitempty"`
	GroupName                     string     `json:"group_name"`
	GroupStatus                   string     `json:"group_status"`
	UserID                        int64      `json:"user_id"`
	UserEmail                     string     `json:"user_email"`
	UserStatus                    string     `json:"user_status"`
	TokenPackageRequired          bool       `json:"token_package_required"`
	TokenPackageRemainingUSDMinor int64      `json:"token_package_remaining_usd_minor"`
}

type BusinessTokenPackageSource struct {
	ID             int64     `json:"id"`
	APIKeyID       int64     `json:"api_key_id"`
	APIKeyName     string    `json:"api_key_name"`
	GroupName      string    `json:"group_name"`
	AmountUSDMinor int64     `json:"amount_usd_minor"`
	CreatedAt      time.Time `json:"created_at"`
}

type BusinessPrivateSubscriptionSource struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	SubscriptionType string    `json:"subscription_type"`
	AmountCents      int64     `json:"amount_cents"`
	ExpiresOn        time.Time `json:"expires_on"`
	CreatedAt        time.Time `json:"created_at"`
}

type BusinessPricingRule struct {
	ID                int64     `json:"id"`
	GroupID           int64     `json:"group_id"`
	GroupName         string    `json:"group_name,omitempty"`
	Tier              string    `json:"tier"`
	MonthlyPriceCents int64     `json:"monthly_price_cents"`
	Active            bool      `json:"active"`
	Notes             *string   `json:"notes,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type BusinessAPIKeyConfig struct {
	ID                    int64   `json:"id"`
	APIKeyID              int64   `json:"api_key_id"`
	RevenueExcluded       bool    `json:"revenue_excluded"`
	OverrideAmountCents   *int64  `json:"override_amount_cents,omitempty"`
	PrivateSubscriptionID *int64  `json:"private_subscription_id,omitempty"`
	Reason                *string `json:"reason,omitempty"`
}

type BusinessCostItem struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	CostClass         string     `json:"cost_class"`
	Category          string     `json:"category"`
	AmountMinor       int64      `json:"amount_minor"`
	Currency          string     `json:"currency"`
	BillingCycle      string     `json:"billing_cycle"`
	StartsOn          time.Time  `json:"starts_on"`
	EndsOn            *time.Time `json:"ends_on,omitempty"`
	AccountID         *int64     `json:"account_id,omitempty"`
	AccountIdentifier *string    `json:"account_identifier,omitempty"`
	IsFree            bool       `json:"is_free"`
	Active            bool       `json:"active"`
	Notes             *string    `json:"notes,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type BusinessExchangeRate struct {
	ID         int64     `json:"id"`
	Month      time.Time `json:"month"`
	Currency   string    `json:"currency"`
	RateScaled int64     `json:"rate_scaled"`
	Source     string    `json:"source"`
	Notes      *string   `json:"notes,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type BusinessSourceBundle struct {
	APIKeys              []BusinessAPIKeySource
	TokenPackages        []BusinessTokenPackageSource
	PrivateSubscriptions []BusinessPrivateSubscriptionSource
	PricingRules         []BusinessPricingRule
	APIKeyConfigs        []BusinessAPIKeyConfig
	Costs                []BusinessCostItem
	ExchangeRates        []BusinessExchangeRate
}

type BusinessLineItem struct {
	ID                  int64      `json:"id,omitempty"`
	ItemType            string     `json:"item_type"`
	SourceType          string     `json:"source_type"`
	SourceID            *int64     `json:"source_id,omitempty"`
	Name                string     `json:"name"`
	Category            *string    `json:"category,omitempty"`
	Tier                *string    `json:"tier,omitempty"`
	OriginalAmountMinor int64      `json:"original_amount_minor"`
	Currency            string     `json:"currency"`
	RateScaled          int64      `json:"rate_scaled"`
	AmountCNYCents      int64      `json:"amount_cny_cents"`
	ExpiresOn           *time.Time `json:"expires_on,omitempty"`
	Reason              *string    `json:"reason,omitempty"`
	Included            bool       `json:"included"`
	LinkedAPIKeyID      *int64     `json:"linked_api_key_id,omitempty"`
	GroupName           string     `json:"group_name,omitempty"`
	UserEmail           string     `json:"user_email,omitempty"`
}

type BusinessIssue struct {
	Type                  string     `json:"type"`
	Severity              string     `json:"severity"`
	SourceType            string     `json:"source_type"`
	SourceID              *int64     `json:"source_id,omitempty"`
	SourceName            string     `json:"source_name"`
	GroupID               *int64     `json:"group_id,omitempty"`
	GroupName             string     `json:"group_name,omitempty"`
	APIKeyExpiresAt       *time.Time `json:"api_key_expires_at,omitempty"`
	SubscriptionExpiresOn *time.Time `json:"subscription_expires_on,omitempty"`
	Message               string     `json:"message"`
	SuggestedAction       string     `json:"suggested_action,omitempty"`
}

type BusinessSummary struct {
	APIKeyCount                     int   `json:"api_key_count"`
	PrivateSubscriptionCount        int   `json:"private_subscription_count"`
	CustomerCount                   int   `json:"customer_count"`
	ExcludedAPIKeyCount             int   `json:"excluded_api_key_count"`
	APIKeyRevenueCents              int64 `json:"api_key_revenue_cents"`
	PrivateSubscriptionRevenueCents int64 `json:"private_subscription_revenue_cents"`
	TotalRevenueCents               int64 `json:"total_revenue_cents"`
	DirectCostCents                 int64 `json:"direct_cost_cents"`
	OperatingCostCents              int64 `json:"operating_cost_cents"`
	GrossProfitCents                int64 `json:"gross_profit_cents"`
	NetProfitCents                  int64 `json:"net_profit_cents"`
	GrossMarginBPS                  int64 `json:"gross_margin_bps"`
	NetMarginBPS                    int64 `json:"net_margin_bps"`
	CostsComplete                   bool  `json:"costs_complete"`
	AnomalyCount                    int   `json:"anomaly_count"`
}

type BusinessReport struct {
	ID          int64              `json:"id,omitempty"`
	Month       time.Time          `json:"month"`
	AsOf        time.Time          `json:"as_of"`
	Status      string             `json:"status"`
	DataQuality string             `json:"data_quality"`
	IsCurrent   bool               `json:"is_current"`
	Summary     BusinessSummary    `json:"summary"`
	Items       []BusinessLineItem `json:"items"`
	Issues      []BusinessIssue    `json:"issues"`
	Notes       *string            `json:"notes,omitempty"`
	ClosedAt    *time.Time         `json:"closed_at,omitempty"`
	ClosedBy    *int64             `json:"closed_by,omitempty"`
}

type BusinessHistoryPoint struct {
	ID            int64           `json:"id,omitempty"`
	Month         time.Time       `json:"month"`
	Status        string          `json:"status"`
	DataQuality   string          `json:"data_quality"`
	IsCurrent     bool            `json:"is_current"`
	Summary       BusinessSummary `json:"summary"`
	CustomerDelta *int            `json:"customer_delta,omitempty"`
	ClosedAt      *time.Time      `json:"closed_at,omitempty"`
}

type BusinessSnapshotWrite struct {
	Report      *BusinessReport
	DataQuality string
	Notes       *string
	ClosedAt    time.Time
	ClosedBy    *int64
}

type CloseBusinessMonthInput struct {
	Month       string  `json:"month"`
	DataQuality string  `json:"data_quality"`
	Notes       *string `json:"notes,omitempty"`
	ClosedBy    *int64  `json:"closed_by,omitempty"`
}

type BusinessRepository interface {
	LoadSources(ctx context.Context, month, asOf time.Time) (*BusinessSourceBundle, error)
	ListSnapshots(ctx context.Context, throughMonth time.Time) ([]BusinessHistoryPoint, error)
	GetSnapshot(ctx context.Context, month time.Time) (*BusinessReport, error)
	CloseSnapshot(ctx context.Context, input BusinessSnapshotWrite) (*BusinessReport, bool, error)
}

type BusinessService struct {
	repo                BusinessRepository
	now                 func() time.Time
	exchangeRateFetcher BusinessExchangeRateFetcher
}

func NewBusinessService(repo BusinessRepository) *BusinessService {
	return &BusinessService{
		repo:                repo,
		now:                 timezone.Now,
		exchangeRateFetcher: NewECBExchangeRateFetcher(nil),
	}
}

func (s *BusinessService) Current(ctx context.Context) (*BusinessReport, error) {
	return s.CurrentAt(ctx, s.now())
}

func (s *BusinessService) CurrentAt(ctx context.Context, asOf time.Time) (*BusinessReport, error) {
	month := businessMonthStart(asOf)
	bundle, err := s.repo.LoadSources(ctx, month, asOf)
	if err != nil {
		return nil, fmt.Errorf("load current business sources: %w", err)
	}
	report := CalculateBusinessReport(asOf, bundle)
	report.IsCurrent = true
	report.Status = "live"
	report.DataQuality = "live"
	return report, nil
}

func (s *BusinessService) History(ctx context.Context) ([]BusinessHistoryPoint, error) {
	now := s.now()
	currentMonth := businessMonthStart(now)
	points, err := s.repo.ListSnapshots(ctx, currentMonth)
	if err != nil {
		return nil, fmt.Errorf("list business snapshots: %w", err)
	}
	current, err := s.CurrentAt(ctx, now)
	if err != nil {
		return nil, err
	}
	points = append(points, BusinessHistoryPoint{
		Month:       current.Month,
		Status:      current.Status,
		DataQuality: current.DataQuality,
		IsCurrent:   true,
		Summary:     current.Summary,
	})
	sort.Slice(points, func(i, j int) bool { return points[i].Month.Before(points[j].Month) })
	for i := range points {
		if i == 0 {
			continue
		}
		delta := points[i].Summary.CustomerCount - points[i-1].Summary.CustomerCount
		points[i].CustomerDelta = &delta
	}
	return points, nil
}

func (s *BusinessService) Month(ctx context.Context, value string) (*BusinessReport, error) {
	month, err := parseBusinessMonth(value)
	if err != nil {
		return nil, err
	}
	now := s.now()
	currentMonth := businessMonthStart(now)
	if month.After(currentMonth) {
		return nil, ErrBusinessFutureMonth
	}
	if month.Equal(currentMonth) {
		return s.CurrentAt(ctx, now)
	}
	report, err := s.repo.GetSnapshot(ctx, month)
	if err != nil {
		return nil, fmt.Errorf("get business snapshot: %w", err)
	}
	return report, nil
}

func (s *BusinessService) CloseMonth(
	ctx context.Context,
	input CloseBusinessMonthInput,
) (*BusinessReport, bool, error) {
	month, err := parseBusinessMonth(input.Month)
	if err != nil {
		return nil, false, err
	}
	quality := strings.ToLower(strings.TrimSpace(input.DataQuality))
	if !isBusinessDataQuality(quality) {
		return nil, false, ErrBusinessDataQualityInvalid
	}
	notes := normalizeBusinessOptionalText(input.Notes)
	if quality != BusinessDataQualityActual && notes == nil {
		return nil, false, ErrBusinessSnapshotNotesRequired
	}
	now := s.now()
	currentMonth := businessMonthStart(now)
	if month.After(currentMonth) {
		return nil, false, ErrBusinessFutureMonth
	}
	if month.Equal(currentMonth) && quality == BusinessDataQualityActual {
		return nil, false, ErrBusinessCurrentMonthClose
	}

	asOf := now
	if month.Before(currentMonth) {
		asOf = month.AddDate(0, 1, 0).Add(-time.Nanosecond)
	}
	bundle, err := s.repo.LoadSources(ctx, month, asOf)
	if err != nil {
		return nil, false, fmt.Errorf("load business sources for close: %w", err)
	}
	report := CalculateBusinessReport(asOf, bundle)
	for _, issue := range report.Issues {
		if issue.Type == BusinessIssueMissingExchangeRate {
			return nil, false, ErrBusinessExchangeRateMissing.WithMetadata(map[string]string{
				"month": report.Month.Format(businessMonthLayout),
			})
		}
	}
	report.Status = BusinessSnapshotStatusLocked
	report.DataQuality = quality
	report.IsCurrent = false
	closed, created, err := s.repo.CloseSnapshot(ctx, BusinessSnapshotWrite{
		Report:      report,
		DataQuality: quality,
		Notes:       notes,
		ClosedAt:    now,
		ClosedBy:    input.ClosedBy,
	})
	if err != nil {
		return nil, false, fmt.Errorf("close business snapshot: %w", err)
	}
	return closed, created, nil
}

// CalculateBusinessReport is deterministic and intentionally independent from
// persistence so revenue and cost rules can be covered by table-driven tests.
func CalculateBusinessReport(asOf time.Time, bundle *BusinessSourceBundle) *BusinessReport {
	if bundle == nil {
		bundle = &BusinessSourceBundle{}
	}
	report := &BusinessReport{
		Month:  businessMonthStart(asOf),
		AsOf:   asOf,
		Items:  make([]BusinessLineItem, 0),
		Issues: make([]BusinessIssue, 0),
	}
	calculateBusinessRevenue(report, asOf, bundle)
	calculateBusinessCosts(report, asOf, bundle)
	finalizeBusinessSummary(report)
	sortBusinessReport(report)
	return report
}

func calculateBusinessRevenue(report *BusinessReport, asOf time.Time, bundle *BusinessSourceBundle) {
	today := normalizeBusinessDate(asOf)
	rules := make(map[int64]BusinessPricingRule, len(bundle.PricingRules))
	for _, rule := range bundle.PricingRules {
		if rule.Active {
			rules[rule.GroupID] = rule
		}
	}
	configs := make(map[int64]BusinessAPIKeyConfig, len(bundle.APIKeyConfigs))
	for _, config := range bundle.APIKeyConfigs {
		configs[config.APIKeyID] = config
	}
	keys := append([]BusinessAPIKeySource(nil), bundle.APIKeys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	for i := range keys {
		key := keys[i]
		if !strings.EqualFold(key.Status, "active") {
			continue
		}
		if !key.CreatedAt.IsZero() && key.CreatedAt.After(asOf) {
			continue
		}
		if key.ExpiresAt == nil {
			appendBusinessKeyIssue(report, key, BusinessIssueMissingExpiry, BusinessIssueSeverityError,
				"Active API key has no expiry and is excluded from strict revenue.",
				"Set an explicit expiry time or disable the key.")
			continue
		}
		if !key.ExpiresAt.After(asOf) {
			continue
		}
		if !strings.EqualFold(key.UserStatus, "active") ||
			(key.GroupID != nil && !strings.EqualFold(key.GroupStatus, "active")) {
			appendBusinessKeyIssue(report, key, BusinessIssueInactiveOwner, BusinessIssueSeverityWarning,
				"API key belongs to an inactive user or group and is excluded from revenue.",
				"Review the user and group status.")
			continue
		}

		config, hasConfig := configs[key.ID]
		if hasConfig && config.RevenueExcluded {
			reason := config.Reason
			if reason == nil || strings.TrimSpace(*reason) == "" {
				value := "Excluded by API key business configuration."
				reason = &value
			}
			report.Items = append(report.Items, BusinessLineItem{
				ItemType:   BusinessItemExcludedAPIKey,
				SourceType: BusinessSourceAPIKey,
				SourceID:   businessInt64Pointer(key.ID),
				Name:       key.Name,
				Currency:   BusinessCurrencyCNY,
				RateScaled: BusinessRateScale,
				ExpiresOn:  businessTimePointer(normalizeBusinessDate(*key.ExpiresAt)),
				Reason:     reason,
				Included:   false,
				GroupName:  key.GroupName,
				UserEmail:  key.UserEmail,
			})
			report.Summary.ExcludedAPIKeyCount++
			continue
		}

		if key.TokenPackageRequired {
			if key.TokenPackageRemainingUSDMinor > 0 {
				report.Summary.APIKeyCount++
			}
			continue
		}

		amount := int64(0)
		tier := ""
		reason := "Resolved from the API key group pricing rule."
		if hasConfig && config.OverrideAmountCents != nil {
			amount = *config.OverrideAmountCents
			tier = "override"
			reason = "API key business amount override."
		} else if key.GroupID != nil {
			if rule, ok := rules[*key.GroupID]; ok {
				amount = rule.MonthlyPriceCents
				tier = rule.Tier
			} else {
				appendBusinessKeyIssue(report, key, BusinessIssueMissingPricingRule, BusinessIssueSeverityError,
					"No active business pricing rule exists for the API key group.",
					"Create or enable a pricing rule for this group.")
				continue
			}
		} else {
			appendBusinessKeyIssue(report, key, BusinessIssueMissingPricingRule, BusinessIssueSeverityError,
				"API key has no group and cannot resolve a business price.",
				"Assign a group or configure an API key amount override.")
			continue
		}

		report.Items = append(report.Items, BusinessLineItem{
			ItemType:            BusinessItemRevenueAPIKey,
			SourceType:          BusinessSourceAPIKey,
			SourceID:            businessInt64Pointer(key.ID),
			Name:                key.Name,
			Tier:                businessStringPointer(tier),
			OriginalAmountMinor: amount,
			Currency:            BusinessCurrencyCNY,
			RateScaled:          BusinessRateScale,
			AmountCNYCents:      amount,
			ExpiresOn:           businessTimePointer(normalizeBusinessDate(*key.ExpiresAt)),
			Reason:              &reason,
			Included:            true,
			GroupName:           key.GroupName,
			UserEmail:           key.UserEmail,
		})
		report.Summary.APIKeyCount++
		report.Summary.APIKeyRevenueCents += amount
	}

	tokenPackages := append([]BusinessTokenPackageSource(nil), bundle.TokenPackages...)
	sort.Slice(tokenPackages, func(i, j int) bool { return tokenPackages[i].ID < tokenPackages[j].ID })
	for i := range tokenPackages {
		pkg := tokenPackages[i]
		if config, ok := configs[pkg.APIKeyID]; ok && config.RevenueExcluded {
			continue
		}
		amount := tokenPackageRevenueCents(pkg.AmountUSDMinor)
		reason := "Token package revenue recognized when quota is issued: CNY 60 per USD 100 quota."
		report.Items = append(report.Items, BusinessLineItem{
			ItemType:            BusinessItemRevenueTokenPackage,
			SourceType:          BusinessSourceTokenPackage,
			SourceID:            businessInt64Pointer(pkg.ID),
			Name:                pkg.APIKeyName,
			Category:            businessStringPointer("token_package"),
			Tier:                businessStringPointer("CNY60_per_USD100"),
			OriginalAmountMinor: pkg.AmountUSDMinor,
			Currency:            "USD",
			RateScaled:          600_000,
			AmountCNYCents:      amount,
			Reason:              &reason,
			Included:            true,
			LinkedAPIKeyID:      businessInt64Pointer(pkg.APIKeyID),
			GroupName:           pkg.GroupName,
		})
		report.Summary.APIKeyRevenueCents += amount
	}

	privateSubscriptions := append([]BusinessPrivateSubscriptionSource(nil), bundle.PrivateSubscriptions...)
	sort.Slice(privateSubscriptions, func(i, j int) bool { return privateSubscriptions[i].ID < privateSubscriptions[j].ID })
	for i := range privateSubscriptions {
		subscription := privateSubscriptions[i]
		if !subscription.CreatedAt.IsZero() && subscription.CreatedAt.After(asOf) {
			continue
		}
		if subscription.ExpiresOn.Before(today) {
			continue
		}
		reason := "Active private customer subscription is an independent revenue source."
		report.Items = append(report.Items, BusinessLineItem{
			ItemType:            BusinessItemRevenuePrivateSubscription,
			SourceType:          BusinessSourcePrivateSubscription,
			SourceID:            businessInt64Pointer(subscription.ID),
			Name:                subscription.Name,
			Category:            businessStringPointer(subscription.SubscriptionType),
			OriginalAmountMinor: subscription.AmountCents,
			Currency:            BusinessCurrencyCNY,
			RateScaled:          BusinessRateScale,
			AmountCNYCents:      subscription.AmountCents,
			ExpiresOn:           businessTimePointer(normalizeBusinessDate(subscription.ExpiresOn)),
			Reason:              &reason,
			Included:            true,
		})
		report.Summary.PrivateSubscriptionCount++
		report.Summary.PrivateSubscriptionRevenueCents += subscription.AmountCents
	}

}

func calculateBusinessCosts(report *BusinessReport, asOf time.Time, bundle *BusinessSourceBundle) {
	month := businessMonthStart(asOf)
	rates := map[string]int64{BusinessCurrencyCNY: BusinessRateScale}
	for _, rate := range bundle.ExchangeRates {
		if sameBusinessMonth(rate.Month, month) {
			rates[strings.ToUpper(rate.Currency)] = rate.RateScaled
		}
	}
	hasDirect := false
	hasOperating := false
	missingRate := false

	costs := append([]BusinessCostItem(nil), bundle.Costs...)
	sort.Slice(costs, func(i, j int) bool { return costs[i].ID < costs[j].ID })
	for i := range costs {
		cost := costs[i]
		if !businessCostApplies(cost, month) {
			continue
		}
		if cost.IsFree || cost.AmountMinor == 0 {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(cost.Currency))
		rate, ok := rates[currency]
		if !ok && (cost.IsFree || cost.AmountMinor == 0) {
			rate = BusinessRateScale
			ok = true
		}
		amountMinor := businessMonthlyCostMinor(cost)
		amountCNY := int64(0)
		if !ok {
			missingRate = true
			report.Issues = append(report.Issues, BusinessIssue{
				Type:            BusinessIssueMissingExchangeRate,
				Severity:        BusinessIssueSeverityError,
				SourceType:      BusinessSourceCostItem,
				SourceID:        businessInt64Pointer(cost.ID),
				SourceName:      cost.Name,
				Message:         fmt.Sprintf("No %s/CNY exchange rate exists for %s.", currency, month.Format(businessMonthLayout)),
				SuggestedAction: "Set the month-specific exchange rate before closing the month.",
			})
		} else {
			amountCNY = convertBusinessMinorToCNY(amountMinor, rate)
		}
		itemType := BusinessItemCostDirect
		if cost.CostClass == BusinessCostClassOperating {
			itemType = BusinessItemCostOperating
			if !cost.IsFree {
				hasOperating = true
			}
		} else if !cost.IsFree {
			hasDirect = true
		}
		reason := businessCostReason(cost)
		report.Items = append(report.Items, BusinessLineItem{
			ItemType:            itemType,
			SourceType:          BusinessSourceCostItem,
			SourceID:            businessInt64Pointer(cost.ID),
			Name:                cost.Name,
			Category:            businessStringPointer(cost.Category),
			OriginalAmountMinor: amountMinor,
			Currency:            currency,
			RateScaled:          rate,
			AmountCNYCents:      amountCNY,
			Reason:              &reason,
			Included:            ok,
		})
		if ok {
			if cost.CostClass == BusinessCostClassOperating {
				report.Summary.OperatingCostCents += amountCNY
			} else {
				report.Summary.DirectCostCents += amountCNY
			}
		}
	}
	if !hasDirect {
		report.Issues = append(report.Issues, BusinessIssue{
			Type:            BusinessIssueMissingDirectCosts,
			Severity:        BusinessIssueSeverityWarning,
			SourceType:      BusinessSourceCostItem,
			SourceName:      "direct costs",
			Message:         "No active paid direct cost is configured for this month.",
			SuggestedAction: "Record paid subscription-account or other direct costs.",
		})
	}
	if !hasOperating {
		report.Issues = append(report.Issues, BusinessIssue{
			Type:            BusinessIssueMissingOperatingCosts,
			Severity:        BusinessIssueSeverityWarning,
			SourceType:      BusinessSourceCostItem,
			SourceName:      "operating costs",
			Message:         "No active paid operating cost is configured for this month.",
			SuggestedAction: "Record server, proxy, domain, payment, or other operating costs; use a zero item only when confirmed.",
		})
	}
	report.Summary.CostsComplete = !missingRate && hasDirect && hasOperating
}

func finalizeBusinessSummary(report *BusinessReport) {
	summary := &report.Summary
	summary.CustomerCount = summary.APIKeyCount + summary.PrivateSubscriptionCount
	summary.TotalRevenueCents = summary.APIKeyRevenueCents + summary.PrivateSubscriptionRevenueCents
	summary.GrossProfitCents = summary.TotalRevenueCents - summary.DirectCostCents
	summary.NetProfitCents = summary.GrossProfitCents - summary.OperatingCostCents
	summary.GrossMarginBPS = businessRatioBPS(summary.GrossProfitCents, summary.TotalRevenueCents)
	summary.NetMarginBPS = businessRatioBPS(summary.NetProfitCents, summary.TotalRevenueCents)
	summary.AnomalyCount = len(report.Issues)
}

func businessCostApplies(cost BusinessCostItem, month time.Time) bool {
	if !cost.Active {
		return false
	}
	month = businessMonthStart(month)
	startMonth := businessMonthStart(cost.StartsOn)
	if month.Before(startMonth) {
		return false
	}
	if cost.EndsOn != nil && month.After(businessMonthStart(*cost.EndsOn)) {
		return false
	}
	switch cost.BillingCycle {
	case BusinessBillingCycleMonthly:
		return true
	case BusinessBillingCycleYearly:
		return true
	case BusinessBillingCycleOneTime:
		return month.Equal(startMonth)
	default:
		return false
	}
}

func businessCostReason(cost BusinessCostItem) string {
	if cost.IsFree {
		return "Free cost record retained for operating structure visibility."
	}
	switch cost.BillingCycle {
	case BusinessBillingCycleYearly:
		return "Yearly recurring cost amortized evenly across 12 operating months."
	case BusinessBillingCycleOneTime:
		return "One-time cost charged in its occurrence month."
	default:
		return "Monthly recurring cost active in this month."
	}
}

func businessMonthlyCostMinor(cost BusinessCostItem) int64 {
	if cost.BillingCycle != BusinessBillingCycleYearly {
		return cost.AmountMinor
	}
	return (cost.AmountMinor + 6) / 12
}

func tokenPackageRevenueCents(amountUSDMinor int64) int64 {
	if amountUSDMinor <= 0 {
		return 0
	}
	value := new(big.Int).Mul(big.NewInt(amountUSDMinor), big.NewInt(3))
	value.Add(value, big.NewInt(2))
	value.Quo(value, big.NewInt(5))
	if !value.IsInt64() {
		return int64(^uint64(0) >> 1)
	}
	return value.Int64()
}

func convertBusinessMinorToCNY(amountMinor, rateScaled int64) int64 {
	if amountMinor == 0 || rateScaled <= 0 {
		return 0
	}
	product := new(big.Int).Mul(big.NewInt(amountMinor), big.NewInt(rateScaled))
	product.Add(product, big.NewInt(BusinessRateScale/2))
	product.Quo(product, big.NewInt(BusinessRateScale))
	if !product.IsInt64() {
		if product.Sign() < 0 {
			return -int64(^uint64(0)>>1) - 1
		}
		return int64(^uint64(0) >> 1)
	}
	return product.Int64()
}

func businessRatioBPS(numerator, denominator int64) int64 {
	if denominator == 0 {
		return 0
	}
	value := new(big.Int).Mul(big.NewInt(numerator), big.NewInt(10_000))
	value.Quo(value, big.NewInt(denominator))
	if !value.IsInt64() {
		return 0
	}
	return value.Int64()
}

func appendBusinessKeyIssue(
	report *BusinessReport,
	key BusinessAPIKeySource,
	issueType, severity, message, suggestedAction string,
) {
	report.Issues = append(report.Issues, businessIssueForKey(key, issueType, severity, message, suggestedAction))
}

func businessIssueForKey(
	key BusinessAPIKeySource,
	issueType, severity, message, suggestedAction string,
) BusinessIssue {
	return BusinessIssue{
		Type:            issueType,
		Severity:        severity,
		SourceType:      BusinessSourceAPIKey,
		SourceID:        businessInt64Pointer(key.ID),
		SourceName:      key.Name,
		GroupID:         key.GroupID,
		GroupName:       key.GroupName,
		APIKeyExpiresAt: key.ExpiresAt,
		Message:         message,
		SuggestedAction: suggestedAction,
	}
}

func appendPossibleDuplicateIssues(report *BusinessReport) {
	apiItems := make(map[string][]BusinessLineItem)
	privateItems := make(map[string][]BusinessLineItem)
	for _, item := range report.Items {
		if !item.Included {
			continue
		}
		name := normalizeBusinessName(item.Name)
		if name == "" {
			continue
		}
		switch item.ItemType {
		case BusinessItemRevenueAPIKey:
			apiItems[name] = append(apiItems[name], item)
		case BusinessItemRevenuePrivateSubscription:
			if item.LinkedAPIKeyID == nil {
				privateItems[name] = append(privateItems[name], item)
			}
		}
	}
	for name, keys := range apiItems {
		subscriptions := privateItems[name]
		for _, key := range keys {
			for _, subscription := range subscriptions {
				report.Issues = append(report.Issues, BusinessIssue{
					Type:                  BusinessIssuePossibleDuplicate,
					Severity:              BusinessIssueSeverityWarning,
					SourceType:            BusinessSourceAPIKey,
					SourceID:              key.SourceID,
					SourceName:            key.Name,
					GroupName:             key.GroupName,
					APIKeyExpiresAt:       key.ExpiresOn,
					SubscriptionExpiresOn: subscription.ExpiresOn,
					Message:               "API key and private subscription have the same normalized name but are not explicitly linked.",
					SuggestedAction:       "Review the records and create an explicit link only if they are the same customer.",
				})
			}
		}
	}
}

func sortBusinessReport(report *BusinessReport) {
	sort.SliceStable(report.Items, func(i, j int) bool {
		if report.Items[i].ItemType != report.Items[j].ItemType {
			return report.Items[i].ItemType < report.Items[j].ItemType
		}
		if report.Items[i].Name != report.Items[j].Name {
			return report.Items[i].Name < report.Items[j].Name
		}
		return businessPointerValue(report.Items[i].SourceID) < businessPointerValue(report.Items[j].SourceID)
	})
	sort.SliceStable(report.Issues, func(i, j int) bool {
		if report.Issues[i].Severity != report.Issues[j].Severity {
			return report.Issues[i].Severity < report.Issues[j].Severity
		}
		if report.Issues[i].Type != report.Issues[j].Type {
			return report.Issues[i].Type < report.Issues[j].Type
		}
		return report.Issues[i].SourceName < report.Issues[j].SourceName
	})
}

func parseBusinessMonth(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	parsed, err := timezone.ParseInLocation(businessMonthLayout, value)
	if err != nil || parsed.Format(businessMonthLayout) != value {
		return time.Time{}, ErrBusinessMonthInvalid
	}
	return businessMonthStart(parsed), nil
}

func isBusinessDataQuality(value string) bool {
	switch value {
	case BusinessDataQualityActual, BusinessDataQualityEstimated, BusinessDataQualityManual:
		return true
	default:
		return false
	}
}

func normalizeBusinessOptionalText(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func businessMonthStart(value time.Time) time.Time {
	value = value.In(timezone.Location())
	return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, timezone.Location())
}

func normalizeBusinessDate(value time.Time) time.Time {
	value = value.In(timezone.Location())
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, timezone.Location())
}

func sameBusinessDate(left, right time.Time) bool {
	left = left.In(timezone.Location())
	right = right.In(timezone.Location())
	return left.Year() == right.Year() && left.Month() == right.Month() && left.Day() == right.Day()
}

func sameBusinessMonth(left, right time.Time) bool {
	left = left.In(timezone.Location())
	right = right.In(timezone.Location())
	return left.Year() == right.Year() && left.Month() == right.Month()
}

func normalizeBusinessName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func businessInt64Pointer(value int64) *int64 { return &value }

func businessStringPointer(value string) *string { return &value }

func businessTimePointer(value time.Time) *time.Time { return &value }

func businessPointerValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
