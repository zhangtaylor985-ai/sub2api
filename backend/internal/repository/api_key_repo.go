package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"

	entsql "entgo.io/ent/dialect/sql"
)

type apiKeyRepository struct {
	client *dbent.Client
	sql    sqlExecutor
	db     *sql.DB
}

func NewAPIKeyRepository(client *dbent.Client, sqlDB *sql.DB) service.APIKeyRepository {
	return newAPIKeyRepositoryWithSQL(client, sqlDB)
}

func newAPIKeyRepositoryWithSQL(client *dbent.Client, sqlq sqlExecutor) *apiKeyRepository {
	repo := &apiKeyRepository{client: client, sql: sqlq}
	if db, ok := sqlq.(*sql.DB); ok {
		repo.db = db
	}
	return repo
}

func (r *apiKeyRepository) activeQuery() *dbent.APIKeyQuery {
	// 默认过滤已软删除记录，避免删除后仍被查询到。
	return r.client.APIKey.Query().Where(apikey.DeletedAtIsNil())
}

func (r *apiKeyRepository) Create(ctx context.Context, key *service.APIKey) error {
	service.NormalizeAPIKeyModelFamilyPolicy(key)
	if key.ExpiresAt == nil {
		expiresAt := service.DefaultAPIKeyExpiresAt(time.Now())
		key.ExpiresAt = &expiresAt
	}
	builder := r.client.APIKey.Create().
		SetUserID(key.UserID).
		SetKey(key.Key).
		SetName(key.Name).
		SetStatus(key.Status).
		SetConcurrency(key.Concurrency).
		SetRateMultiplier(key.BillingRateMultiplier()).
		SetTokenPackageRequired(key.TokenPackageRequired).
		SetAllowClaudeFamily(key.AllowClaudeFamily).
		SetAllowGptFamily(key.AllowGPTFamily).
		SetAllowImageGeneration(key.AllowImageGeneration).
		SetMessagesDispatchModelConfig(key.MessagesDispatchModelConfig).
		SetNillableGroupID(key.GroupID).
		SetNillableLastUsedAt(key.LastUsedAt).
		SetQuota(key.Quota).
		SetQuotaUsed(key.QuotaUsed).
		SetNillableExpiresAt(key.ExpiresAt).
		SetRateLimit5h(key.RateLimit5h).
		SetRateLimit1d(key.RateLimit1d).
		SetRateLimit7d(key.RateLimit7d)

	if len(key.IPWhitelist) > 0 {
		builder.SetIPWhitelist(key.IPWhitelist)
	}
	if len(key.IPBlacklist) > 0 {
		builder.SetIPBlacklist(key.IPBlacklist)
	}

	created, err := builder.Save(ctx)
	if err == nil {
		key.ID = created.ID
		key.LastUsedAt = created.LastUsedAt
		key.CreatedAt = created.CreatedAt
		key.UpdatedAt = created.UpdatedAt
	}
	return translatePersistenceError(err, nil, service.ErrAPIKeyExists)
}

func (r *apiKeyRepository) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	m, err := r.activeQuery().
		Where(apikey.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return apiKeyEntityToService(m), nil
}

// GetKeyAndOwnerID 根据 API Key ID 获取其 key 与所有者（用户）ID。
// 相比 GetByID，此方法性能更优，因为：
//   - 使用 Select() 只查询必要字段，减少数据传输量
//   - 不加载完整的 API Key 实体及其关联数据（User、Group 等）
//   - 适用于删除等只需 key 与用户 ID 的场景
func (r *apiKeyRepository) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	m, err := r.activeQuery().
		Where(apikey.IDEQ(id)).
		Select(apikey.FieldKey, apikey.FieldUserID).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return "", 0, service.ErrAPIKeyNotFound
		}
		return "", 0, err
	}
	return m.Key, m.UserID, nil
}

func (r *apiKeyRepository) GetByKey(ctx context.Context, key string) (*service.APIKey, error) {
	m, err := r.activeQuery().
		Where(apikey.KeyEQ(key)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return apiKeyEntityToService(m), nil
}

func (r *apiKeyRepository) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	m, err := r.activeQuery().
		Where(apikey.KeyEQ(key)).
		Select(
			apikey.FieldID,
			apikey.FieldUserID,
			apikey.FieldGroupID,
			apikey.FieldName,
			apikey.FieldStatus,
			apikey.FieldConcurrency,
			apikey.FieldRateMultiplier,
			apikey.FieldTokenPackageRequired,
			apikey.FieldAllowClaudeFamily,
			apikey.FieldAllowGptFamily,
			apikey.FieldAllowImageGeneration,
			apikey.FieldMessagesDispatchModelConfig,
			apikey.FieldIPWhitelist,
			apikey.FieldIPBlacklist,
			apikey.FieldQuota,
			apikey.FieldQuotaUsed,
			apikey.FieldExpiresAt,
			apikey.FieldRateLimit5h,
			apikey.FieldRateLimit1d,
			apikey.FieldRateLimit7d,
		).
		WithUser(func(q *dbent.UserQuery) {
			q.Select(
				user.FieldID,
				user.FieldEmail,
				user.FieldUsername,
				user.FieldStatus,
				user.FieldRole,
				user.FieldBalance,
				user.FieldConcurrency,
				user.FieldBalanceNotifyEnabled,
				user.FieldBalanceNotifyThresholdType,
				user.FieldBalanceNotifyThreshold,
				user.FieldBalanceNotifyExtraEmails,
				user.FieldTotalRecharged,
				user.FieldSignupSource,
				user.FieldLastLoginAt,
				user.FieldLastActiveAt,
				user.FieldRpmLimit,
			)
		}).
		WithGroup(func(q *dbent.GroupQuery) {
			q.Select(
				group.FieldID,
				group.FieldName,
				group.FieldPlatform,
				group.FieldStatus,
				group.FieldSubscriptionType,
				group.FieldRateMultiplier,
				group.FieldDedicatedUnlimited,
				group.FieldDailyLimitUsd,
				group.FieldWeeklyLimitUsd,
				group.FieldMonthlyLimitUsd,
				group.FieldAllowImageGeneration,
				group.FieldImageRateIndependent,
				group.FieldImageRateMultiplier,
				group.FieldImagePrice1k,
				group.FieldImagePrice2k,
				group.FieldImagePrice4k,
				group.FieldClaudeCodeOnly,
				group.FieldFallbackGroupID,
				group.FieldFallbackGroupIDOnInvalidRequest,
				group.FieldModelRoutingEnabled,
				group.FieldModelRouting,
				group.FieldMcpXMLInject,
				group.FieldSupportedModelScopes,
				group.FieldAllowMessagesDispatch,
				group.FieldDefaultMappedModel,
				group.FieldMessagesDispatchModelConfig,
				group.FieldRpmLimit,
				group.FieldConcurrency,
			)
		}).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	result := apiKeyEntityToService(m)
	if err := r.hydratePlanPackageSummary(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *apiKeyRepository) Update(ctx context.Context, key *service.APIKey) error {
	if key.ExpiresAt == nil {
		return service.ErrAPIKeyExpirationRequired
	}
	// 使用原子操作：将软删除检查与更新合并到同一语句，避免竞态条件。
	// 之前的实现先检查 Exist 再 UpdateOneID，若在两步之间发生软删除，
	// 则会更新已删除的记录。
	// 这里选择 Update().Where()，确保只有未软删除记录能被更新。
	// 同时显式设置 updated_at，避免二次查询带来的并发可见性问题。
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	builder := client.APIKey.Update().
		Where(apikey.IDEQ(key.ID), apikey.DeletedAtIsNil()).
		SetName(key.Name).
		SetStatus(key.Status).
		SetConcurrency(key.Concurrency).
		SetRateMultiplier(key.BillingRateMultiplier()).
		SetTokenPackageRequired(key.TokenPackageRequired).
		SetAllowClaudeFamily(key.AllowsClaudeFamily()).
		SetAllowGptFamily(key.AllowsGPTFamily()).
		SetAllowImageGeneration(key.AllowsImageGeneration()).
		SetMessagesDispatchModelConfig(key.MessagesDispatchModelConfig).
		SetQuota(key.Quota).
		SetQuotaUsed(key.QuotaUsed).
		SetRateLimit5h(key.RateLimit5h).
		SetRateLimit1d(key.RateLimit1d).
		SetRateLimit7d(key.RateLimit7d).
		SetUsage5h(key.Usage5h).
		SetUsage1d(key.Usage1d).
		SetUsage7d(key.Usage7d).
		SetUpdatedAt(now)
	if key.GroupID != nil {
		builder.SetGroupID(*key.GroupID)
	} else {
		builder.ClearGroupID()
	}

	// API keys always have a mandatory expiration time.
	builder.SetExpiresAt(*key.ExpiresAt)

	// Rate limit window start times
	if key.Window5hStart != nil {
		builder.SetWindow5hStart(*key.Window5hStart)
	} else {
		builder.ClearWindow5hStart()
	}
	if key.Window1dStart != nil {
		builder.SetWindow1dStart(*key.Window1dStart)
	} else {
		builder.ClearWindow1dStart()
	}
	if key.Window7dStart != nil {
		builder.SetWindow7dStart(*key.Window7dStart)
	} else {
		builder.ClearWindow7dStart()
	}

	// IP 限制字段
	if len(key.IPWhitelist) > 0 {
		builder.SetIPWhitelist(key.IPWhitelist)
	} else {
		builder.ClearIPWhitelist()
	}
	if len(key.IPBlacklist) > 0 {
		builder.SetIPBlacklist(key.IPBlacklist)
	} else {
		builder.ClearIPBlacklist()
	}

	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		// 更新影响行数为 0，说明记录不存在或已被软删除。
		return service.ErrAPIKeyNotFound
	}

	// 使用同一时间戳回填，避免并发删除导致二次查询失败。
	key.UpdatedAt = now
	return nil
}

func (r *apiKeyRepository) Delete(ctx context.Context, id int64) error {
	// 存在唯一键约束 生成tombstone key 用来释放原key，长度远小于 128，满足 schema 限制
	tombstoneKey := fmt.Sprintf("__deleted__%d__%d", id, time.Now().UnixNano())
	// 显式软删除：避免依赖 Hook 行为，确保 deleted_at 一定被设置。
	affected, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		SetKey(tombstoneKey).
		SetDeletedAt(time.Now()).
		Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return service.ErrAPIKeyNotFound
		}
		return err
	}
	if affected == 0 {
		exists, err := r.client.APIKey.Query().
			Where(apikey.IDEQ(id)).
			Exist(mixins.SkipSoftDelete(ctx))
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		return service.ErrAPIKeyNotFound
	}
	return nil
}

func (r *apiKeyRepository) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	q := r.activeQuery().Where(apikey.UserIDEQ(userID))

	// Apply filters
	if filters.Search != "" {
		q = q.Where(apikey.Or(
			apikey.NameContainsFold(filters.Search),
			apikey.KeyContainsFold(filters.Search),
		))
	}
	if filters.Status != "" {
		q = q.Where(apikey.StatusEQ(filters.Status))
	}
	if filters.GroupID != nil {
		if *filters.GroupID == 0 {
			q = q.Where(apikey.GroupIDIsNil())
		} else {
			q = q.Where(apikey.GroupIDEQ(*filters.GroupID))
		}
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	keysQuery := q.
		WithGroup().
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range apiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}

	return outKeys, paginationResultFromTotal(int64(total), params), nil
}

func (r *apiKeyRepository) ListAdmin(ctx context.Context, params pagination.PaginationParams, filters service.AdminAPIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	q := r.activeQuery()

	if filters.Search != "" {
		keyword := strings.TrimSpace(filters.Search)
		q = q.Where(apikey.Or(
			apikey.NameContainsFold(keyword),
			apikey.KeyContainsFold(keyword),
			apikey.HasUserWith(
				user.Or(
					user.EmailContainsFold(keyword),
					user.UsernameContainsFold(keyword),
				),
			),
		))
	}
	if filters.Status != "" {
		q = q.Where(apikey.StatusEQ(filters.Status))
	}
	if filters.UserID != nil && *filters.UserID > 0 {
		q = q.Where(apikey.UserIDEQ(*filters.UserID))
	}
	if filters.GroupID != nil {
		if *filters.GroupID == 0 {
			q = q.Where(apikey.GroupIDIsNil())
		} else {
			q = q.Where(apikey.GroupIDEQ(*filters.GroupID))
		}
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	keysQuery := q.
		WithUser().
		WithGroup().
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range apiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}
	return outKeys, paginationResultFromTotal(int64(total), params), nil
}

func (r *apiKeyRepository) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if len(apiKeyIDs) == 0 {
		return []int64{}, nil
	}

	ids, err := r.client.APIKey.Query().
		Where(apikey.UserIDEQ(userID), apikey.IDIn(apiKeyIDs...), apikey.DeletedAtIsNil()).
		IDs(ctx)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *apiKeyRepository) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	count, err := r.activeQuery().Where(apikey.UserIDEQ(userID)).Count(ctx)
	return int64(count), err
}

func (r *apiKeyRepository) ExistsByKey(ctx context.Context, key string) (bool, error) {
	count, err := r.activeQuery().Where(apikey.KeyEQ(key)).Count(ctx)
	return count > 0, err
}

func (r *apiKeyRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	q := r.activeQuery().Where(apikey.GroupIDEQ(groupID))

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	keysQuery := q.
		WithUser().
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range apiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}

	return outKeys, paginationResultFromTotal(int64(total), params), nil
}

func apiKeyListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)

	var field string
	switch sortBy {
	case "name":
		field = apikey.FieldName
	case "status":
		field = apikey.FieldStatus
	case "expires_at":
		field = apikey.FieldExpiresAt
	case "last_used_at":
		field = apikey.FieldLastUsedAt
	case "concurrency":
		field = apikey.FieldConcurrency
	case "created_at":
		field = apikey.FieldCreatedAt
	default:
		field = apikey.FieldID
	}

	if sortOrder == pagination.SortOrderAsc {
		return []func(*entsql.Selector){dbent.Asc(field), dbent.Asc(apikey.FieldID)}
	}
	return []func(*entsql.Selector){dbent.Desc(field), dbent.Desc(apikey.FieldID)}
}

// SearchAPIKeys searches API keys by user ID and keyword.
// Names support fuzzy matching while the raw key only supports exact matching.
func (r *apiKeyRepository) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]service.APIKey, error) {
	q := r.activeQuery()
	if userID > 0 {
		q = q.Where(apikey.UserIDEQ(userID))
	}

	if keyword != "" {
		exact, err := q.Clone().Where(apikey.KeyEQ(keyword)).Only(ctx)
		if err == nil {
			return []service.APIKey{*apiKeyEntityToService(exact)}, nil
		}
		if !dbent.IsNotFound(err) {
			return nil, err
		}

		q = q.Where(apikey.NameContainsFold(keyword))
	}

	keys, err := q.Limit(limit).Order(dbent.Desc(apikey.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}
	return outKeys, nil
}

// ClearGroupIDByGroupID 将指定分组的所有 API Key 的 group_id 设为 nil
func (r *apiKeyRepository) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	n, err := r.client.APIKey.Update().
		Where(apikey.GroupIDEQ(groupID), apikey.DeletedAtIsNil()).
		ClearGroupID().
		Save(ctx)
	return int64(n), err
}

// UpdateGroupIDByUserAndGroup 将用户下绑定 oldGroupID 的所有 Key 迁移到 newGroupID
func (r *apiKeyRepository) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	n, err := client.APIKey.Update().
		Where(apikey.UserIDEQ(userID), apikey.GroupIDEQ(oldGroupID), apikey.DeletedAtIsNil()).
		SetGroupID(newGroupID).
		Save(ctx)
	return int64(n), err
}

// CountByGroupID 获取分组的 API Key 数量
func (r *apiKeyRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	count, err := r.activeQuery().Where(apikey.GroupIDEQ(groupID)).Count(ctx)
	return int64(count), err
}

func (r *apiKeyRepository) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	keys, err := r.activeQuery().
		Where(apikey.UserIDEQ(userID)).
		Select(apikey.FieldKey).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (r *apiKeyRepository) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	keys, err := r.activeQuery().
		Where(apikey.GroupIDEQ(groupID)).
		Select(apikey.FieldKey).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// IncrementQuotaUsed 使用 Ent 原子递增 quota_used 字段并返回新值
func (r *apiKeyRepository) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	updated, err := r.client.APIKey.UpdateOneID(id).
		Where(apikey.DeletedAtIsNil()).
		AddQuotaUsed(amount).
		Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return 0, service.ErrAPIKeyNotFound
		}
		return 0, err
	}
	return updated.QuotaUsed, nil
}

// IncrementQuotaUsedAndGetState atomically increments quota_used, conditionally marks the key
// as quota_exhausted, and returns the latest quota state in one round trip.
func (r *apiKeyRepository) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*service.APIKeyQuotaUsageState, error) {
	query := `
		UPDATE api_keys
		SET
			quota_used = quota_used + $1,
			status = CASE
				WHEN quota > 0 AND quota_used + $1 >= quota THEN $2
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL
		RETURNING quota_used, quota, key, status
	`

	state := &service.APIKeyQuotaUsageState{}
	if err := scanSingleRow(ctx, r.sql, query, []any{amount, service.StatusAPIKeyQuotaExhausted, id}, &state.QuotaUsed, &state.Quota, &state.Key, &state.Status); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return state, nil
}

func (r *apiKeyRepository) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	affected, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		SetLastUsedAt(usedAt).
		SetUpdatedAt(usedAt).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

// IncrementRateLimitUsage atomically increments all rate limit usage counters and initializes
// window start times via COALESCE if not already set.
func (r *apiKeyRepository) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`,
		cost, id)
	return err
}

// ResetRateLimitWindows resets expired rate limit windows atomically.
func (r *apiKeyRepository) ResetRateLimitWindows(ctx context.Context, id int64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN 0 ELSE usage_5h END,
			window_5h_start = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN 0 ELSE usage_1d END,
			window_1d_start = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN 0 ELSE usage_7d END,
			window_7d_start = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`,
		id)
	return err
}

// GetRateLimitData returns the current rate limit usage and window start times for an API key.
func (r *apiKeyRepository) GetRateLimitData(ctx context.Context, id int64) (result *service.APIKeyRateLimitData, err error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT usage_5h, usage_1d, usage_7d, window_5h_start, window_1d_start, window_7d_start
		FROM api_keys
		WHERE id = $1 AND deleted_at IS NULL`,
		id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	if !rows.Next() {
		return nil, service.ErrAPIKeyNotFound
	}
	data := &service.APIKeyRateLimitData{}
	if err := rows.Scan(&data.Usage5h, &data.Usage1d, &data.Usage7d, &data.Window5hStart, &data.Window1dStart, &data.Window7dStart); err != nil {
		return nil, err
	}
	return data, rows.Err()
}

// GetTokenPackageState returns active token package allowance totals for an API key.
func (r *apiKeyRepository) GetTokenPackageState(ctx context.Context, id int64) (*service.APIKeyTokenPackageState, error) {
	state := &service.APIKeyTokenPackageState{}
	err := scanSingleRow(ctx, r.sql, `
		SELECT
			COALESCE(SUM(amount_usd), 0),
			COALESCE(SUM(used_usd), 0),
			COALESCE(SUM(GREATEST(amount_usd - used_usd, 0)), 0)
		FROM api_key_token_packages
		WHERE api_key_id = $1 AND started_at <= NOW()`,
		[]any{id},
		&state.TotalUSD,
		&state.UsedUSD,
		&state.RemainingUSD)
	if err != nil {
		return nil, err
	}
	return state, nil
}

// GetTokenPackageRemaining returns the active remaining token package allowance for an API key.
func (r *apiKeyRepository) GetTokenPackageRemaining(ctx context.Context, id int64) (float64, error) {
	state, err := r.GetTokenPackageState(ctx, id)
	if err != nil {
		return 0, err
	}
	return state.RemainingUSD, nil
}

// AddTokenPackage creates a new token package top-up for an API key.
func (r *apiKeyRepository) AddTokenPackage(ctx context.Context, id int64, amount float64, note, createdBy string) (*service.APIKeyTokenPackage, error) {
	if amount <= 0 {
		return nil, service.ErrAPIKeyNotFound
	}
	var exists bool
	if err := scanSingleRow(ctx, r.sql, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id = $1 AND deleted_at IS NULL)`, []any{id}, &exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, service.ErrAPIKeyNotFound
	}

	pkg := &service.APIKeyTokenPackage{}
	err := scanSingleRow(ctx, r.sql, `
		INSERT INTO api_key_token_packages (api_key_id, amount_usd, note, created_by)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))
		RETURNING id, api_key_id, amount_usd, used_usd, COALESCE(note, ''), COALESCE(created_by, ''), started_at, created_at, updated_at`,
		[]any{id, amount, strings.TrimSpace(note), strings.TrimSpace(createdBy)},
		&pkg.ID, &pkg.APIKeyID, &pkg.AmountUSD, &pkg.UsedUSD, &pkg.Note, &pkg.CreatedBy, &pkg.StartedAt, &pkg.CreatedAt, &pkg.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return pkg, nil
}

func (r *apiKeyRepository) ListTokenPackages(ctx context.Context, id int64, limit int) ([]service.APIKeyTokenPackage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, api_key_id, amount_usd, used_usd, COALESCE(note, ''), COALESCE(created_by, ''), started_at, created_at, updated_at
		FROM api_key_token_packages
		WHERE api_key_id = $1
		ORDER BY started_at DESC, id DESC
		LIMIT $2`,
		id, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	packages := make([]service.APIKeyTokenPackage, 0)
	for rows.Next() {
		var pkg service.APIKeyTokenPackage
		if err := rows.Scan(&pkg.ID, &pkg.APIKeyID, &pkg.AmountUSD, &pkg.UsedUSD, &pkg.Note, &pkg.CreatedBy, &pkg.StartedAt, &pkg.CreatedAt, &pkg.UpdatedAt); err != nil {
			return nil, err
		}
		packages = append(packages, pkg)
	}
	return packages, rows.Err()
}

func (r *apiKeyRepository) ListTokenPackageUsage(ctx context.Context, id int64, limit int) ([]service.APIKeyTokenPackageUsage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, package_id, api_key_id, COALESCE(request_id, ''), COALESCE(request_fingerprint, ''),
		       COALESCE(model, ''), cost_usd,
		       input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_tokens,
		       requested_at, created_at
		FROM api_key_token_package_usage
		WHERE api_key_id = $1
		ORDER BY requested_at DESC, id DESC
		LIMIT $2`,
		id, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	usages := make([]service.APIKeyTokenPackageUsage, 0)
	for rows.Next() {
		var usage service.APIKeyTokenPackageUsage
		if err := rows.Scan(
			&usage.ID,
			&usage.PackageID,
			&usage.APIKeyID,
			&usage.RequestID,
			&usage.RequestFingerprint,
			&usage.Model,
			&usage.CostUSD,
			&usage.InputTokens,
			&usage.OutputTokens,
			&usage.CacheCreationTokens,
			&usage.CacheReadTokens,
			&usage.TotalTokens,
			&usage.RequestedAt,
			&usage.CreatedAt,
		); err != nil {
			return nil, err
		}
		usages = append(usages, usage)
	}
	return usages, rows.Err()
}

type apiKeyPlanPackageRowScanner interface {
	Scan(dest ...any) error
}

func scanAPIKeyPlanPackage(scanner apiKeyPlanPackageRowScanner, pkg *service.APIKeyPlanPackage) error {
	return scanner.Scan(
		&pkg.ID,
		&pkg.APIKeyID,
		&pkg.GroupID,
		&pkg.RequestID,
		&pkg.PackageName,
		&pkg.DailyLimitUSD,
		&pkg.WeeklyLimitUSD,
		&pkg.Concurrency,
		&pkg.Months,
		&pkg.StartsAt,
		&pkg.ExpiresAt,
		&pkg.Source,
		&pkg.Note,
		&pkg.CreatedBy,
		&pkg.CreatedAt,
		&pkg.UpdatedAt,
		&pkg.IsActive,
		&pkg.IsUpcoming,
	)
}

const apiKeyPlanPackageBaseColumns = `
	id, api_key_id, group_id, request_id, package_name,
	daily_limit_usd, weekly_limit_usd, concurrency, months,
	starts_at, expires_at, source, COALESCE(note, ''), COALESCE(created_by, ''),
	created_at, updated_at`

const apiKeyPlanPackageSelectColumns = apiKeyPlanPackageBaseColumns + `,
	starts_at <= $2 AND expires_at > $2 AS is_active,
	starts_at > $2 AS is_upcoming`

func (r *apiKeyRepository) AddPlanPackage(ctx context.Context, input service.AddAPIKeyPlanPackageInput) (*service.AddAPIKeyPlanPackageResult, error) {
	if r.db == nil {
		return nil, service.ErrAPIKeyPlanPackageUnavailable
	}
	if input.APIKeyID <= 0 || input.GroupID <= 0 || strings.TrimSpace(input.RequestID) == "" || input.Months < 1 || input.Months > 24 {
		return nil, service.ErrAPIKeyPlanPackageInvalid
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		keyValue      string
		keyStatus     string
		legacyGroupID sql.NullInt64
		legacyExpires sql.NullTime
		keyCreatedAt  time.Time
	)
	err = tx.QueryRowContext(ctx, `
		SELECT key, status, group_id, expires_at, created_at
		FROM api_keys
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`, input.APIKeyID).Scan(&keyValue, &keyStatus, &legacyGroupID, &legacyExpires, &keyCreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}

	requestID := strings.TrimSpace(input.RequestID)
	existing := &service.APIKeyPlanPackage{}
	err = scanAPIKeyPlanPackage(tx.QueryRowContext(ctx, `SELECT `+apiKeyPlanPackageSelectColumns+`
		FROM api_key_plan_packages
		WHERE api_key_id = $1 AND request_id = $3`, input.APIKeyID, now, requestID), existing)
	if err == nil {
		if existing.GroupID != input.GroupID || existing.Months != input.Months {
			return nil, service.ErrAPIKeyPlanPackageInvalid
		}
		summary, summaryErr := getAPIKeyPlanPackageSummary(ctx, tx, input.APIKeyID, now)
		if summaryErr != nil {
			return nil, summaryErr
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &service.AddAPIKeyPlanPackageResult{Package: *existing, Summary: *summary, Key: keyValue, Idempotent: true}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	var (
		packageName string
		dailyLimit  float64
		weeklyLimit float64
		concurrency int
		groupStatus string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT name, COALESCE(daily_limit_usd, 0), COALESCE(weekly_limit_usd, 0), concurrency, status
		FROM groups
		WHERE id = $1 AND deleted_at IS NULL
		FOR SHARE`, input.GroupID).Scan(&packageName, &dailyLimit, &weeklyLimit, &concurrency, &groupStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrGroupNotFound
		}
		return nil, err
	}
	if groupStatus != service.StatusActive || (dailyLimit <= 0 && weeklyLimit <= 0 && concurrency <= 0) {
		return nil, service.ErrAPIKeyPlanPackageInvalid
	}

	var packageCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_key_plan_packages WHERE api_key_id = $1`, input.APIKeyID).Scan(&packageCount); err != nil {
		return nil, err
	}
	if packageCount == 0 && legacyGroupID.Valid && legacyExpires.Valid && legacyExpires.Time.After(now) {
		var (
			legacyName        string
			legacyDaily       float64
			legacyWeekly      float64
			legacyConcurrency int
		)
		legacyErr := tx.QueryRowContext(ctx, `
			SELECT name, COALESCE(daily_limit_usd, 0), COALESCE(weekly_limit_usd, 0), concurrency
			FROM groups
			WHERE id = $1 AND deleted_at IS NULL`, legacyGroupID.Int64).Scan(&legacyName, &legacyDaily, &legacyWeekly, &legacyConcurrency)
		if legacyErr != nil && legacyErr != sql.ErrNoRows {
			return nil, legacyErr
		}
		if legacyErr == nil {
			legacyStart := service.AddCalendarMonthsClamped(legacyExpires.Time, -1)
			if keyCreatedAt.After(legacyStart) && keyCreatedAt.Before(legacyExpires.Time) {
				legacyStart = keyCreatedAt
			}
			_, err = tx.ExecContext(ctx, `
				INSERT INTO api_key_plan_packages (
					api_key_id, group_id, request_id, package_name,
					daily_limit_usd, weekly_limit_usd, concurrency, months,
					starts_at, expires_at, source, note, created_by
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $9, 'legacy_baseline', $10, 'system')
				ON CONFLICT (api_key_id, request_id) DO NOTHING`,
				input.APIKeyID, legacyGroupID.Int64, fmt.Sprintf("legacy-baseline:%d", input.APIKeyID), legacyName,
				legacyDaily, legacyWeekly, legacyConcurrency, legacyStart, legacyExpires.Time,
				"Imported from the API key state that existed before plan stacking was enabled.")
			if err != nil {
				return nil, err
			}
		}
	}

	startsAt := now
	expiryBase := now
	var samePackageExpiry sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(expires_at)
		FROM api_key_plan_packages
		WHERE api_key_id = $1 AND group_id = $2`, input.APIKeyID, input.GroupID).Scan(&samePackageExpiry); err != nil {
		return nil, err
	}
	if samePackageExpiry.Valid && samePackageExpiry.Time.After(expiryBase) {
		expiryBase = samePackageExpiry.Time
	}
	expiresAt := service.AddCalendarMonthsClamped(expiryBase, input.Months)

	pkg := &service.APIKeyPlanPackage{}
	err = scanAPIKeyPlanPackage(tx.QueryRowContext(ctx, `
		INSERT INTO api_key_plan_packages (
			api_key_id, group_id, request_id, package_name,
			daily_limit_usd, weekly_limit_usd, concurrency, months,
			starts_at, expires_at, source, note, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'admin', NULLIF($11, ''), NULLIF($12, ''))
		RETURNING `+apiKeyPlanPackageBaseColumns+`,
			starts_at <= $13 AND expires_at > $13 AS is_active,
			starts_at > $13 AS is_upcoming`,
		input.APIKeyID, input.GroupID, requestID, packageName,
		dailyLimit, weeklyLimit, concurrency, input.Months, startsAt, expiresAt,
		strings.TrimSpace(input.Note), strings.TrimSpace(input.CreatedBy), now), pkg)
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE api_keys
		SET expires_at = (
				SELECT MAX(expires_at) FROM api_key_plan_packages WHERE api_key_id = $1
			),
			status = CASE WHEN status = $2 THEN $3 ELSE status END,
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`, input.APIKeyID, service.StatusAPIKeyExpired, service.StatusAPIKeyActive)
	if err != nil {
		return nil, err
	}

	summary, err := getAPIKeyPlanPackageSummary(ctx, tx, input.APIKeyID, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &service.AddAPIKeyPlanPackageResult{Package: *pkg, Summary: *summary, Key: keyValue}, nil
}

func (r *apiKeyRepository) ListPlanPackages(ctx context.Context, id int64, limit int, now time.Time) ([]service.APIKeyPlanPackage, error) {
	if r.sql == nil {
		return nil, service.ErrAPIKeyPlanPackageUnavailable
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if now.IsZero() {
		now = time.Now()
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT `+apiKeyPlanPackageSelectColumns+`
		FROM api_key_plan_packages
		WHERE api_key_id = $1
		ORDER BY starts_at ASC, id ASC
		LIMIT $3`, id, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]service.APIKeyPlanPackage, 0)
	for rows.Next() {
		var pkg service.APIKeyPlanPackage
		if err := scanAPIKeyPlanPackage(rows, &pkg); err != nil {
			return nil, err
		}
		items = append(items, pkg)
	}
	return items, rows.Err()
}

func (r *apiKeyRepository) GetPlanPackageSummary(ctx context.Context, id int64, now time.Time) (*service.APIKeyPlanPackageSummary, error) {
	if r.sql == nil {
		return nil, service.ErrAPIKeyPlanPackageUnavailable
	}
	if now.IsZero() {
		now = time.Now()
	}
	return getAPIKeyPlanPackageSummary(ctx, r.sql, id, now)
}

func getAPIKeyPlanPackageSummary(ctx context.Context, executor sqlExecutor, id int64, now time.Time) (*service.APIKeyPlanPackageSummary, error) {
	summary := &service.APIKeyPlanPackageSummary{}
	err := scanSingleRow(ctx, executor, `
		SELECT
			COUNT(*) > 0 AS managed,
			COUNT(*) FILTER (WHERE starts_at <= $2 AND expires_at > $2) AS active_count,
			COALESCE(SUM(daily_limit_usd) FILTER (WHERE starts_at <= $2 AND expires_at > $2), 0),
			COALESCE(SUM(weekly_limit_usd) FILTER (WHERE starts_at <= $2 AND expires_at > $2), 0),
			COALESCE(SUM(concurrency) FILTER (WHERE starts_at <= $2 AND expires_at > $2), 0),
			MAX(expires_at),
			(
				SELECT MIN(transition_at)
				FROM (
					SELECT starts_at AS transition_at
					FROM api_key_plan_packages
					WHERE api_key_id = $1 AND starts_at > $2
					UNION ALL
					SELECT expires_at AS transition_at
					FROM api_key_plan_packages
					WHERE api_key_id = $1 AND starts_at <= $2 AND expires_at > $2
				) transitions
			)
		FROM api_key_plan_packages
		WHERE api_key_id = $1`, []any{id, now},
		&summary.Managed,
		&summary.ActiveCount,
		&summary.DailyLimitUSD,
		&summary.WeeklyLimitUSD,
		&summary.Concurrency,
		&summary.LatestExpiresAt,
		&summary.NextTransitionAt)
	if err != nil {
		return nil, err
	}
	return summary, nil
}

func (r *apiKeyRepository) hydratePlanPackageSummary(ctx context.Context, key *service.APIKey) error {
	if key == nil || r.sql == nil {
		return nil
	}
	summary, err := r.GetPlanPackageSummary(ctx, key.ID, time.Now())
	if err != nil {
		return err
	}
	if summary.Managed {
		key.PlanPackageSummary = summary
	}
	return nil
}

func apiKeyEntityToService(m *dbent.APIKey) *service.APIKey {
	if m == nil {
		return nil
	}
	out := &service.APIKey{
		ID:                          m.ID,
		UserID:                      m.UserID,
		Key:                         m.Key,
		Name:                        m.Name,
		Status:                      m.Status,
		RateMultiplier:              m.RateMultiplier,
		TokenPackageRequired:        m.TokenPackageRequired,
		AllowClaudeFamily:           m.AllowClaudeFamily,
		AllowGPTFamily:              m.AllowGptFamily,
		ModelFamilyPolicySet:        true,
		AllowImageGeneration:        m.AllowImageGeneration,
		ImageGenerationPolicySet:    true,
		MessagesDispatchModelConfig: m.MessagesDispatchModelConfig,
		IPWhitelist:                 m.IPWhitelist,
		IPBlacklist:                 m.IPBlacklist,
		LastUsedAt:                  m.LastUsedAt,
		CreatedAt:                   m.CreatedAt,
		UpdatedAt:                   m.UpdatedAt,
		GroupID:                     m.GroupID,
		Quota:                       m.Quota,
		Concurrency:                 m.Concurrency,
		QuotaUsed:                   m.QuotaUsed,
		ExpiresAt:                   m.ExpiresAt,
		RateLimit5h:                 m.RateLimit5h,
		RateLimit1d:                 m.RateLimit1d,
		RateLimit7d:                 m.RateLimit7d,
		Usage5h:                     m.Usage5h,
		Usage1d:                     m.Usage1d,
		Usage7d:                     m.Usage7d,
		Window5hStart:               m.Window5hStart,
		Window1dStart:               m.Window1dStart,
		Window7dStart:               m.Window7dStart,
	}
	if m.Edges.User != nil {
		out.User = userEntityToService(m.Edges.User)
	}
	if m.Edges.Group != nil {
		out.Group = groupEntityToService(m.Edges.Group)
	}
	return out
}

func userEntityToService(u *dbent.User) *service.User {
	if u == nil {
		return nil
	}
	out := &service.User{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Notes:                      u.Notes,
		PasswordHash:               u.PasswordHash,
		Role:                       u.Role,
		Balance:                    u.Balance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		SignupSource:               u.SignupSource,
		LastLoginAt:                u.LastLoginAt,
		LastActiveAt:               u.LastActiveAt,
		TotpSecretEncrypted:        u.TotpSecretEncrypted,
		TotpEnabled:                u.TotpEnabled,
		TotpEnabledAt:              u.TotpEnabledAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RpmLimit,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
	}
	// Parse extra emails JSON (supports both old []string and new []NotifyEmailEntry format)
	if u.BalanceNotifyExtraEmails != "" && u.BalanceNotifyExtraEmails != "[]" {
		out.BalanceNotifyExtraEmails = service.ParseNotifyEmails(u.BalanceNotifyExtraEmails)
	}
	return out
}

func groupEntityToService(g *dbent.Group) *service.Group {
	if g == nil {
		return nil
	}
	return &service.Group{
		ID:                              g.ID,
		Name:                            g.Name,
		Description:                     derefString(g.Description),
		Platform:                        g.Platform,
		RateMultiplier:                  g.RateMultiplier,
		IsExclusive:                     g.IsExclusive,
		DedicatedUnlimited:              g.DedicatedUnlimited,
		Status:                          g.Status,
		Hydrated:                        true,
		SubscriptionType:                g.SubscriptionType,
		DailyLimitUSD:                   g.DailyLimitUsd,
		WeeklyLimitUSD:                  g.WeeklyLimitUsd,
		MonthlyLimitUSD:                 g.MonthlyLimitUsd,
		AllowImageGeneration:            g.AllowImageGeneration,
		ImageRateIndependent:            g.ImageRateIndependent,
		ImageRateMultiplier:             g.ImageRateMultiplier,
		ImagePrice1K:                    g.ImagePrice1k,
		ImagePrice2K:                    g.ImagePrice2k,
		ImagePrice4K:                    g.ImagePrice4k,
		DefaultValidityDays:             g.DefaultValidityDays,
		ClaudeCodeOnly:                  g.ClaudeCodeOnly,
		FallbackGroupID:                 g.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: g.FallbackGroupIDOnInvalidRequest,
		ModelRouting:                    g.ModelRouting,
		ModelRoutingEnabled:             g.ModelRoutingEnabled,
		MCPXMLInject:                    g.McpXMLInject,
		SupportedModelScopes:            g.SupportedModelScopes,
		SortOrder:                       g.SortOrder,
		AllowMessagesDispatch:           g.AllowMessagesDispatch,
		RequireOAuthOnly:                g.RequireOauthOnly,
		RequirePrivacySet:               g.RequirePrivacySet,
		DefaultMappedModel:              g.DefaultMappedModel,
		MessagesDispatchModelConfig:     g.MessagesDispatchModelConfig,
		RPMLimit:                        g.RpmLimit,
		Concurrency:                     g.Concurrency,
		CreatedAt:                       g.CreatedAt,
		UpdatedAt:                       g.UpdatedAt,
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
