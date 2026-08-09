package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	businessDateLayout       = "2006-01-02"
	businessMaxAmountMinor   = int64(99_999_999_999)
	businessMaxRateScaled    = int64(1_000_000_000_000)
	businessDefaultCostNotes = "Managed by business profitability defaults."
)

var (
	ErrBusinessManagementUnavailable = errors.New("business management repository is unavailable")
	ErrBusinessCostNotFound          = infraerrors.NotFound("BUSINESS_COST_NOT_FOUND", "business cost item not found")
	ErrBusinessCostInvalid           = infraerrors.BadRequest("BUSINESS_COST_INVALID", "business cost item is invalid")
	ErrBusinessPricingRuleInvalid    = infraerrors.BadRequest("BUSINESS_PRICING_RULE_INVALID", "business pricing rule is invalid")
	ErrBusinessAPIKeyConfigInvalid   = infraerrors.BadRequest("BUSINESS_API_KEY_CONFIG_INVALID", "business API key config is invalid")
	ErrBusinessExchangeRateInvalid   = infraerrors.BadRequest("BUSINESS_EXCHANGE_RATE_INVALID", "business exchange rate is invalid")
)

type BusinessGroupReference struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	IsExclusive bool   `json:"is_exclusive"`
}

type BusinessAPIKeyReference struct {
	ID        int64                 `json:"id"`
	Name      string                `json:"name"`
	Status    string                `json:"status"`
	ExpiresAt *time.Time            `json:"expires_at,omitempty"`
	GroupID   *int64                `json:"group_id,omitempty"`
	GroupName string                `json:"group_name"`
	UserEmail string                `json:"user_email"`
	Config    *BusinessAPIKeyConfig `json:"config,omitempty"`
}

type BusinessAccountReference struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
}

type BusinessReferenceData struct {
	Groups               []BusinessGroupReference            `json:"groups"`
	APIKeys              []BusinessAPIKeyReference           `json:"api_keys"`
	Accounts             []BusinessAccountReference          `json:"accounts"`
	PrivateSubscriptions []BusinessPrivateSubscriptionSource `json:"private_subscriptions"`
}

type CreateBusinessCostInput struct {
	Name              string  `json:"name"`
	CostClass         string  `json:"cost_class"`
	Category          string  `json:"category"`
	AmountMinor       int64   `json:"amount_minor"`
	Currency          string  `json:"currency"`
	BillingCycle      string  `json:"billing_cycle"`
	StartsOn          string  `json:"starts_on"`
	EndsOn            *string `json:"ends_on,omitempty"`
	AccountID         *int64  `json:"account_id,omitempty"`
	AccountIdentifier *string `json:"account_identifier,omitempty"`
	IsFree            bool    `json:"is_free"`
	Active            bool    `json:"active"`
	Notes             *string `json:"notes,omitempty"`
}

type UpdateBusinessCostInput = CreateBusinessCostInput

type UpsertBusinessPricingRuleInput struct {
	GroupID           int64   `json:"group_id"`
	Tier              string  `json:"tier"`
	MonthlyPriceCents int64   `json:"monthly_price_cents"`
	Active            bool    `json:"active"`
	Notes             *string `json:"notes,omitempty"`
}

type UpsertBusinessAPIKeyConfigInput struct {
	RevenueExcluded       bool    `json:"revenue_excluded"`
	OverrideAmountCents   *int64  `json:"override_amount_cents,omitempty"`
	PrivateSubscriptionID *int64  `json:"private_subscription_id,omitempty"`
	Reason                *string `json:"reason,omitempty"`
}

type UpsertBusinessExchangeRateInput struct {
	Currency   string  `json:"currency"`
	RateScaled int64   `json:"rate_scaled"`
	Source     string  `json:"source"`
	Notes      *string `json:"notes,omitempty"`
}

type BusinessDefaultPricing struct {
	GroupID           int64
	Tier              string
	MonthlyPriceCents int64
}

type BusinessDefaultExcludedKey struct {
	APIKeyID int64
	Reason   string
}

type BusinessDefaultCost struct {
	Name              string
	CostClass         string
	Category          string
	BillingCycle      string
	AccountID         *int64
	AccountIdentifier string
	AmountMinor       int64
	Currency          string
	IsFree            bool
}

type BusinessDefaultInitialization struct {
	Month         time.Time
	Pricing       []BusinessDefaultPricing
	ExcludedKeys  []BusinessDefaultExcludedKey
	Costs         []BusinessDefaultCost
	USDRateScaled int64
}

type BusinessInitializationResult struct {
	PricingCreated       int      `json:"pricing_created"`
	PricingExisting      int      `json:"pricing_existing"`
	ExclusionsCreated    int      `json:"exclusions_created"`
	ExclusionsExisting   int      `json:"exclusions_existing"`
	CostsCreated         int      `json:"costs_created"`
	CostsExisting        int      `json:"costs_existing"`
	ExchangeRateCreated  bool     `json:"exchange_rate_created"`
	MissingPricingTiers  []string `json:"missing_pricing_tiers"`
	MissingExcludedNames []string `json:"missing_excluded_names"`
	MissingAccountNames  []string `json:"missing_account_names"`
}

type BusinessReconciliationResult struct {
	AsOf         time.Time       `json:"as_of"`
	Issues       []BusinessIssue `json:"issues"`
	ErrorCount   int             `json:"error_count"`
	WarningCount int             `json:"warning_count"`
	InfoCount    int             `json:"info_count"`
}

type BusinessManagementRepository interface {
	ListCosts(ctx context.Context) ([]BusinessCostItem, error)
	CreateCost(ctx context.Context, cost *BusinessCostItem) error
	UpdateCost(ctx context.Context, cost *BusinessCostItem) error
	DeleteCost(ctx context.Context, id int64) error
	ListPricingRules(ctx context.Context) ([]BusinessPricingRule, error)
	UpsertPricingRule(ctx context.Context, rule *BusinessPricingRule) error
	ListExchangeRates(ctx context.Context, month time.Time) ([]BusinessExchangeRate, error)
	UpsertExchangeRate(ctx context.Context, rate *BusinessExchangeRate) error
	UpsertAPIKeyConfig(ctx context.Context, config *BusinessAPIKeyConfig) error
	ListReferences(ctx context.Context) (*BusinessReferenceData, error)
	AccountExists(ctx context.Context, id int64) (bool, error)
	GroupExists(ctx context.Context, id int64) (bool, error)
	APIKeyExists(ctx context.Context, id int64) (bool, error)
	PrivateSubscriptionExists(ctx context.Context, id int64) (bool, error)
	InitializeDefaults(ctx context.Context, defaults BusinessDefaultInitialization) (*BusinessInitializationResult, error)
}

func (s *BusinessService) managementRepository() (BusinessManagementRepository, error) {
	repo, ok := s.repo.(BusinessManagementRepository)
	if !ok {
		return nil, ErrBusinessManagementUnavailable
	}
	return repo, nil
}

func (s *BusinessService) ListCosts(ctx context.Context) ([]BusinessCostItem, error) {
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	items, err := repo.ListCosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list business costs: %w", err)
	}
	return items, nil
}

func (s *BusinessService) CreateCost(
	ctx context.Context,
	input CreateBusinessCostInput,
) (*BusinessCostItem, error) {
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	cost, err := s.validateBusinessCostInput(ctx, repo, input)
	if err != nil {
		return nil, err
	}
	if err := repo.CreateCost(ctx, cost); err != nil {
		return nil, fmt.Errorf("create business cost: %w", err)
	}
	return cost, nil
}

func (s *BusinessService) UpdateCost(
	ctx context.Context,
	id int64,
	input UpdateBusinessCostInput,
) (*BusinessCostItem, error) {
	if id <= 0 {
		return nil, ErrBusinessCostInvalid
	}
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	cost, err := s.validateBusinessCostInput(ctx, repo, input)
	if err != nil {
		return nil, err
	}
	cost.ID = id
	if err := repo.UpdateCost(ctx, cost); err != nil {
		return nil, fmt.Errorf("update business cost: %w", err)
	}
	return cost, nil
}

func (s *BusinessService) DeleteCost(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrBusinessCostInvalid
	}
	repo, err := s.managementRepository()
	if err != nil {
		return err
	}
	if err := repo.DeleteCost(ctx, id); err != nil {
		return fmt.Errorf("delete business cost: %w", err)
	}
	return nil
}

func (s *BusinessService) ListPricingRules(ctx context.Context) ([]BusinessPricingRule, error) {
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	items, err := repo.ListPricingRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list business pricing rules: %w", err)
	}
	return items, nil
}

func (s *BusinessService) UpsertPricingRule(
	ctx context.Context,
	input UpsertBusinessPricingRuleInput,
) (*BusinessPricingRule, error) {
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	input.Tier = strings.TrimSpace(input.Tier)
	if input.GroupID <= 0 || !isBusinessPricingTier(input.Tier) ||
		input.MonthlyPriceCents < 0 || input.MonthlyPriceCents > businessMaxAmountMinor {
		return nil, ErrBusinessPricingRuleInvalid
	}
	exists, err := repo.GroupExists(ctx, input.GroupID)
	if err != nil {
		return nil, fmt.Errorf("check business pricing group: %w", err)
	}
	if !exists {
		return nil, ErrBusinessPricingRuleInvalid.WithMetadata(map[string]string{"field": "group_id"})
	}
	rule := &BusinessPricingRule{
		GroupID:           input.GroupID,
		Tier:              input.Tier,
		MonthlyPriceCents: input.MonthlyPriceCents,
		Active:            input.Active,
		Notes:             normalizeBusinessOptionalText(input.Notes),
	}
	if err := repo.UpsertPricingRule(ctx, rule); err != nil {
		return nil, fmt.Errorf("upsert business pricing rule: %w", err)
	}
	return rule, nil
}

func (s *BusinessService) ListExchangeRates(
	ctx context.Context,
	monthValue string,
) ([]BusinessExchangeRate, error) {
	month, err := parseBusinessMonth(monthValue)
	if err != nil {
		return nil, err
	}
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	items, err := repo.ListExchangeRates(ctx, month)
	if err != nil {
		return nil, fmt.Errorf("list business exchange rates: %w", err)
	}
	return items, nil
}

func (s *BusinessService) UpsertExchangeRate(
	ctx context.Context,
	monthValue string,
	input UpsertBusinessExchangeRateInput,
) (*BusinessExchangeRate, error) {
	month, err := parseBusinessMonth(monthValue)
	if err != nil {
		return nil, err
	}
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	currency := normalizeBusinessCurrency(input.Currency)
	if !isSupportedBusinessCurrency(currency) || input.RateScaled <= 0 || input.RateScaled > businessMaxRateScaled {
		return nil, ErrBusinessExchangeRateInvalid
	}
	if currency == BusinessCurrencyCNY && input.RateScaled != BusinessRateScale {
		return nil, ErrBusinessExchangeRateInvalid.WithMetadata(map[string]string{"field": "rate_scaled"})
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "manual"
	}
	if len([]rune(source)) > 32 {
		return nil, ErrBusinessExchangeRateInvalid
	}
	rate := &BusinessExchangeRate{
		Month:      month,
		Currency:   currency,
		RateScaled: input.RateScaled,
		Source:     source,
		Notes:      normalizeBusinessOptionalText(input.Notes),
	}
	if err := repo.UpsertExchangeRate(ctx, rate); err != nil {
		return nil, fmt.Errorf("upsert business exchange rate: %w", err)
	}
	return rate, nil
}

func (s *BusinessService) UpsertAPIKeyConfig(
	ctx context.Context,
	apiKeyID int64,
	input UpsertBusinessAPIKeyConfigInput,
) (*BusinessAPIKeyConfig, error) {
	conflictingStrategies := input.RevenueExcluded &&
		(input.OverrideAmountCents != nil || input.PrivateSubscriptionID != nil) ||
		input.OverrideAmountCents != nil && input.PrivateSubscriptionID != nil
	if apiKeyID <= 0 || conflictingStrategies || input.OverrideAmountCents != nil &&
		(*input.OverrideAmountCents < 0 || *input.OverrideAmountCents > businessMaxAmountMinor) {
		return nil, ErrBusinessAPIKeyConfigInvalid
	}
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	exists, err := repo.APIKeyExists(ctx, apiKeyID)
	if err != nil {
		return nil, fmt.Errorf("check business API key: %w", err)
	}
	if !exists {
		return nil, ErrBusinessAPIKeyConfigInvalid.WithMetadata(map[string]string{"field": "api_key_id"})
	}
	if input.PrivateSubscriptionID != nil {
		if *input.PrivateSubscriptionID <= 0 {
			return nil, ErrBusinessAPIKeyConfigInvalid
		}
		exists, err := repo.PrivateSubscriptionExists(ctx, *input.PrivateSubscriptionID)
		if err != nil {
			return nil, fmt.Errorf("check private subscription: %w", err)
		}
		if !exists {
			return nil, ErrBusinessAPIKeyConfigInvalid.WithMetadata(map[string]string{"field": "private_subscription_id"})
		}
	}
	config := &BusinessAPIKeyConfig{
		APIKeyID:              apiKeyID,
		RevenueExcluded:       input.RevenueExcluded,
		OverrideAmountCents:   input.OverrideAmountCents,
		PrivateSubscriptionID: input.PrivateSubscriptionID,
		Reason:                normalizeBusinessOptionalText(input.Reason),
	}
	if err := repo.UpsertAPIKeyConfig(ctx, config); err != nil {
		return nil, fmt.Errorf("upsert business API key config: %w", err)
	}
	return config, nil
}

func (s *BusinessService) References(ctx context.Context) (*BusinessReferenceData, error) {
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	data, err := repo.ListReferences(ctx)
	if err != nil {
		return nil, fmt.Errorf("list business references: %w", err)
	}
	return data, nil
}

func (s *BusinessService) InitializeDefaults(ctx context.Context) (*BusinessInitializationResult, error) {
	repo, err := s.managementRepository()
	if err != nil {
		return nil, err
	}
	references, err := repo.ListReferences(ctx)
	if err != nil {
		return nil, fmt.Errorf("list business references for initialization: %w", err)
	}
	defaults, result := buildBusinessDefaultInitialization(businessMonthStart(s.now()), references)
	stored, err := repo.InitializeDefaults(ctx, defaults)
	if err != nil {
		return nil, fmt.Errorf("initialize business defaults: %w", err)
	}
	stored.MissingPricingTiers = result.MissingPricingTiers
	stored.MissingExcludedNames = result.MissingExcludedNames
	stored.MissingAccountNames = result.MissingAccountNames
	return stored, nil
}

func (s *BusinessService) Reconciliation(ctx context.Context) (*BusinessReconciliationResult, error) {
	now := s.now()
	current, err := s.CurrentAt(ctx, now)
	if err != nil {
		return nil, err
	}
	issues := append([]BusinessIssue(nil), current.Issues...)
	history, err := s.repo.ListSnapshots(ctx, businessMonthStart(now))
	if err != nil {
		return nil, fmt.Errorf("list snapshots for reconciliation: %w", err)
	}
	issues = append(issues, businessHistoryGapIssues(history, businessMonthStart(now))...)
	result := &BusinessReconciliationResult{AsOf: now, Issues: issues}
	for _, issue := range issues {
		switch issue.Severity {
		case BusinessIssueSeverityError:
			result.ErrorCount++
		case BusinessIssueSeverityWarning:
			result.WarningCount++
		default:
			result.InfoCount++
		}
	}
	return result, nil
}

func (s *BusinessService) validateBusinessCostInput(
	ctx context.Context,
	repo BusinessManagementRepository,
	input CreateBusinessCostInput,
) (*BusinessCostItem, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.CostClass = strings.ToLower(strings.TrimSpace(input.CostClass))
	input.Category = strings.TrimSpace(input.Category)
	input.Currency = normalizeBusinessCurrency(input.Currency)
	input.BillingCycle = strings.ToLower(strings.TrimSpace(input.BillingCycle))
	if input.Name == "" || len([]rune(input.Name)) > 160 ||
		input.Category == "" || len([]rune(input.Category)) > 50 ||
		!isBusinessCostClass(input.CostClass) || !isBusinessBillingCycle(input.BillingCycle) ||
		!isSupportedBusinessCurrency(input.Currency) || input.AmountMinor < 0 ||
		input.AmountMinor > businessMaxAmountMinor || input.IsFree && input.AmountMinor != 0 {
		return nil, ErrBusinessCostInvalid
	}
	startsOn, err := parseBusinessDate(input.StartsOn)
	if err != nil {
		return nil, err
	}
	var endsOn *time.Time
	if input.EndsOn != nil && strings.TrimSpace(*input.EndsOn) != "" {
		parsed, err := parseBusinessDate(*input.EndsOn)
		if err != nil || parsed.Before(startsOn) {
			return nil, ErrBusinessCostInvalid.WithMetadata(map[string]string{"field": "ends_on"})
		}
		endsOn = &parsed
	}
	if input.AccountID != nil {
		if *input.AccountID <= 0 {
			return nil, ErrBusinessCostInvalid
		}
		exists, err := repo.AccountExists(ctx, *input.AccountID)
		if err != nil {
			return nil, fmt.Errorf("check cost account: %w", err)
		}
		if !exists {
			return nil, ErrBusinessCostInvalid.WithMetadata(map[string]string{"field": "account_id"})
		}
	}
	return &BusinessCostItem{
		Name:              input.Name,
		CostClass:         input.CostClass,
		Category:          input.Category,
		AmountMinor:       input.AmountMinor,
		Currency:          input.Currency,
		BillingCycle:      input.BillingCycle,
		StartsOn:          startsOn,
		EndsOn:            endsOn,
		AccountID:         input.AccountID,
		AccountIdentifier: normalizeBusinessOptionalText(input.AccountIdentifier),
		IsFree:            input.IsFree,
		Active:            input.Active,
		Notes:             normalizeBusinessOptionalText(input.Notes),
	}, nil
}

func buildBusinessDefaultInitialization(
	month time.Time,
	references *BusinessReferenceData,
) (BusinessDefaultInitialization, BusinessInitializationResult) {
	defaults := BusinessDefaultInitialization{Month: month, USDRateScaled: 6_750_000}
	result := BusinessInitializationResult{}
	if references == nil {
		references = &BusinessReferenceData{}
	}
	prices := map[string]int64{
		"dedicated": 146_000,
		"double":    73_000,
		"triple":    48_500,
		"quad":      36_500,
	}
	foundTiers := make(map[string]bool)
	for _, group := range references.Groups {
		tier := classifyBusinessPricingTier(group.Name, group.IsExclusive)
		price, ok := prices[tier]
		if !ok {
			continue
		}
		foundTiers[tier] = true
		defaults.Pricing = append(defaults.Pricing, BusinessDefaultPricing{
			GroupID: group.ID, Tier: tier, MonthlyPriceCents: price,
		})
	}
	for _, tier := range []string{"dedicated", "double", "triple", "quad"} {
		if !foundTiers[tier] {
			result.MissingPricingTiers = append(result.MissingPricingTiers, tier)
		}
	}

	excludedNames := []string{"TW - Lily", "Larry", "TW - jane", "TW - cloud", "TW - Dow", "TW"}
	keysByName := make(map[string][]BusinessAPIKeyReference)
	for _, key := range references.APIKeys {
		name := normalizeBusinessName(key.Name)
		keysByName[name] = append(keysByName[name], key)
	}
	for _, name := range excludedNames {
		matched := keysByName[normalizeBusinessName(name)]
		if len(matched) == 0 {
			result.MissingExcludedNames = append(result.MissingExcludedNames, name)
			continue
		}
		for _, key := range matched {
			defaults.ExcludedKeys = append(defaults.ExcludedKeys, BusinessDefaultExcludedKey{
				APIKeyID: key.ID,
				Reason:   "Excluded from operating revenue by confirmed business policy.",
			})
		}
	}

	accountsByName := make(map[string]BusinessAccountReference)
	for _, account := range references.Accounts {
		accountsByName[normalizeBusinessName(account.Name)] = account
	}
	paidAccounts := []string{"hoangthihang05041997@gmail.com", "anhduc250391@gmail.com"}
	for _, name := range paidAccounts {
		accountID := businessAccountIDByName(accountsByName, name, &result)
		defaults.Costs = append(defaults.Costs, BusinessDefaultCost{
			Name: name + " subscription", CostClass: BusinessCostClassDirect,
			Category: "subscription_account", BillingCycle: BusinessBillingCycleMonthly,
			AccountID: accountID, AccountIdentifier: name,
			AmountMinor: 15_000, Currency: "USD", IsFree: false,
		})
	}
	defaults.Costs = append(defaults.Costs, BusinessDefaultCost{
		Name: "claudepool.com domain", CostClass: BusinessCostClassOperating,
		Category: "domain", BillingCycle: BusinessBillingCycleYearly,
		AccountIdentifier: "claudepool.com", AmountMinor: 3_000, Currency: "USD",
	})
	sort.Strings(result.MissingPricingTiers)
	sort.Strings(result.MissingExcludedNames)
	sort.Strings(result.MissingAccountNames)
	return defaults, result
}

func businessAccountIDByName(
	accounts map[string]BusinessAccountReference,
	name string,
	result *BusinessInitializationResult,
) *int64 {
	account, ok := accounts[normalizeBusinessName(name)]
	if !ok {
		result.MissingAccountNames = append(result.MissingAccountNames, name)
		return nil
	}
	return businessInt64Pointer(account.ID)
}

func classifyBusinessPricingTier(name string, exclusive bool) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), ""))
	switch {
	case strings.Contains(normalized, "独享") || strings.Contains(normalized, "dedicated"):
		return "dedicated"
	case regexp.MustCompile(`(^|[^0-9])2人`).MatchString(normalized) || strings.Contains(normalized, "双人") || strings.Contains(normalized, "double"):
		return "double"
	case regexp.MustCompile(`(^|[^0-9])3人`).MatchString(normalized) || strings.Contains(normalized, "三人") || strings.Contains(normalized, "triple"):
		return "triple"
	case regexp.MustCompile(`(^|[^0-9])4人`).MatchString(normalized) || strings.Contains(normalized, "四人") || strings.Contains(normalized, "quad"):
		return "quad"
	case exclusive:
		return "dedicated"
	default:
		return ""
	}
}

func businessHistoryGapIssues(
	points []BusinessHistoryPoint,
	currentMonth time.Time,
) []BusinessIssue {
	previousMonth := currentMonth.AddDate(0, -1, 0)
	if len(points) == 0 {
		return []BusinessIssue{businessHistoryGapIssue(previousMonth)}
	}
	existing := make(map[string]struct{}, len(points))
	earliest := previousMonth
	for _, point := range points {
		existing[point.Month.Format(businessMonthLayout)] = struct{}{}
		if point.Month.Before(earliest) {
			earliest = businessMonthStart(point.Month)
		}
	}
	issues := make([]BusinessIssue, 0)
	for month := earliest; !month.After(previousMonth); month = month.AddDate(0, 1, 0) {
		if _, ok := existing[month.Format(businessMonthLayout)]; !ok {
			issues = append(issues, businessHistoryGapIssue(month))
		}
	}
	return issues
}

func businessHistoryGapIssue(month time.Time) BusinessIssue {
	name := month.Format(businessMonthLayout)
	return BusinessIssue{
		Type:            BusinessIssueHistoryGap,
		Severity:        BusinessIssueSeverityInfo,
		SourceType:      BusinessSourceSnapshot,
		SourceName:      name,
		Message:         "No locked business snapshot exists for this historical month.",
		SuggestedAction: "Close the month as actual when source data is reliable, otherwise use estimated or manual with notes.",
	}
}

func parseBusinessDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	parsed, err := timezone.ParseInLocation(businessDateLayout, value)
	if err != nil || parsed.Format(businessDateLayout) != value {
		return time.Time{}, ErrBusinessCostInvalid.WithMetadata(map[string]string{"field": "date"})
	}
	return normalizeBusinessDate(parsed), nil
}

func normalizeBusinessCurrency(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func isBusinessCostClass(value string) bool {
	return value == BusinessCostClassDirect || value == BusinessCostClassOperating
}

func isBusinessBillingCycle(value string) bool {
	switch value {
	case BusinessBillingCycleMonthly, BusinessBillingCycleYearly, BusinessBillingCycleOneTime:
		return true
	default:
		return false
	}
}

func isBusinessPricingTier(value string) bool {
	switch value {
	case "dedicated", "double", "triple", "quad":
		return true
	default:
		return false
	}
}

func isSupportedBusinessCurrency(value string) bool {
	switch value {
	case "CNY", "USD", "PHP", "EUR", "HKD", "SGD", "JPY", "GBP", "AUD", "CAD":
		return true
	default:
		return false
	}
}
