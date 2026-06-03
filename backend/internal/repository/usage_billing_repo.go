package repository

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type usageBillingRepository struct {
	db *sql.DB
}

func NewUsageBillingRepository(_ *dbent.Client, sqlDB *sql.DB) service.UsageBillingRepository {
	return &usageBillingRepository{db: sqlDB}
}

func (r *usageBillingRepository) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (_ *service.UsageBillingApplyResult, err error) {
	if cmd == nil {
		return &service.UsageBillingApplyResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}

	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingKey(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if !applied {
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}

	result := &service.UsageBillingApplyResult{Applied: true}
	if err := r.applyUsageBillingEffects(ctx, tx, cmd, result); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func (r *usageBillingRepository) claimUsageBillingKey(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) (bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (request_id, api_key_id) DO NOTHING
		RETURNING id
	`, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var existingFingerprint string
		if err := tx.QueryRowContext(ctx, `
			SELECT request_fingerprint
			FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		`, cmd.RequestID, cmd.APIKeyID).Scan(&existingFingerprint); err != nil {
			return false, err
		}
		if strings.TrimSpace(existingFingerprint) != strings.TrimSpace(cmd.RequestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var archivedFingerprint string
	err = tx.QueryRowContext(ctx, `
		SELECT request_fingerprint
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, cmd.RequestID, cmd.APIKeyID).Scan(&archivedFingerprint)
	if err == nil {
		if strings.TrimSpace(archivedFingerprint) != strings.TrimSpace(cmd.RequestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, nil
}

func (r *usageBillingRepository) applyUsageBillingEffects(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) error {
	if cmd.SubscriptionCost > 0 && cmd.SubscriptionID != nil {
		if err := incrementUsageBillingSubscription(ctx, tx, *cmd.SubscriptionID, cmd.SubscriptionCost); err != nil {
			return err
		}
	}

	if cmd.BalanceCost > 0 {
		newBalance, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, cmd.BalanceCost)
		if err != nil {
			return err
		}
		result.NewBalance = &newBalance
	}

	if cmd.APIKeyQuotaCost > 0 {
		exhausted, err := incrementUsageBillingAPIKeyQuota(ctx, tx, cmd.APIKeyID, cmd.APIKeyQuotaCost)
		if err != nil {
			return err
		}
		result.APIKeyQuotaExhausted = exhausted
	}

	if cmd.APIKeyRateLimitCost > 0 {
		if err := incrementUsageBillingAPIKeyRateLimit(ctx, tx, cmd, cmd.APIKeyRateLimitCost); err != nil {
			return err
		}
	}

	if cmd.AccountQuotaCost > 0 && (strings.EqualFold(cmd.AccountType, service.AccountTypeAPIKey) || strings.EqualFold(cmd.AccountType, service.AccountTypeBedrock)) {
		quotaState, err := incrementUsageBillingAccountQuota(ctx, tx, cmd.AccountID, cmd.AccountQuotaCost)
		if err != nil {
			return err
		}
		result.QuotaState = quotaState
	}

	return nil
}

func incrementUsageBillingSubscription(ctx context.Context, tx *sql.Tx, subscriptionID int64, costUSD float64) error {
	const updateSQL = `
		UPDATE user_subscriptions us
		SET
			daily_usage_usd = us.daily_usage_usd + $1,
			weekly_usage_usd = us.weekly_usage_usd + $1,
			monthly_usage_usd = us.monthly_usage_usd + $1,
			updated_at = NOW()
		FROM groups g
		WHERE us.id = $2
			AND us.deleted_at IS NULL
			AND us.group_id = g.id
			AND g.deleted_at IS NULL
	`
	res, err := tx.ExecContext(ctx, updateSQL, costUSD, subscriptionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return service.ErrSubscriptionNotFound
}

func deductUsageBillingBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (float64, error) {
	var newBalance float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING balance
	`, amount, userID).Scan(&newBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrUserNotFound
	}
	if err != nil {
		return 0, err
	}
	return newBalance, nil
}

func incrementUsageBillingAPIKeyQuota(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) (bool, error) {
	var exhausted bool
	err := tx.QueryRowContext(ctx, `
		UPDATE api_keys
		SET quota_used = quota_used + $1,
			status = CASE
				WHEN quota > 0
					AND status = $3
					AND quota_used < quota
					AND quota_used + $1 >= quota
				THEN $4
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING quota > 0 AND quota_used >= quota AND quota_used - $1 < quota
	`, amount, apiKeyID, service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).Scan(&exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrAPIKeyNotFound
	}
	if err != nil {
		return false, err
	}
	return exhausted, nil
}

func incrementUsageBillingAPIKeyRateLimit(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, cost float64) error {
	apiKeyID := cmd.APIKeyID
	state, err := lockAPIKeyRateLimitState(ctx, tx, apiKeyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrAPIKeyNotFound
		}
		return err
	}

	now := time.Now().UTC()
	state.resetExpired(now)

	baseCovered := cost
	if state.limit1d > 0 {
		baseCovered = math.Min(baseCovered, positiveRemaining(state.limit1d, state.usage1d))
	}
	if state.limit7d > 0 {
		baseCovered = math.Min(baseCovered, positiveRemaining(state.limit7d, state.usage7d))
	}
	if baseCovered < 0 {
		baseCovered = 0
	}
	packageCost := cost - baseCovered
	if packageCost < 0 {
		packageCost = 0
	}

	usage5hAdd := cost
	usage1dAdd := cost
	if state.limit1d > 0 {
		usage1dAdd = baseCovered
	}
	usage7dAdd := cost
	if state.limit7d > 0 {
		usage7dAdd = baseCovered
	}

	if err := updateAPIKeyRateLimitState(ctx, tx, apiKeyID, state, usage5hAdd, usage1dAdd, usage7dAdd); err != nil {
		return err
	}
	if packageCost > 0 {
		return allocateAPIKeyTokenPackages(ctx, tx, apiKeyID, packageCost, cmd.RequestID, cmd.RequestFingerprint, cmd.Model)
	}
	return nil
}

type apiKeyRateLimitSQLState struct {
	usage5h float64
	usage1d float64
	usage7d float64
	limit1d float64
	limit7d float64
	w5h     *time.Time
	w1d     *time.Time
	w7d     *time.Time
}

func lockAPIKeyRateLimitState(ctx context.Context, tx *sql.Tx, apiKeyID int64) (*apiKeyRateLimitSQLState, error) {
	var (
		usage5h, usage1d, usage7d float64
		limit1d, groupDailyLimit  sql.NullFloat64
		limit7d, groupWeeklyLimit sql.NullFloat64
		w5h, w1d, w7d             sql.NullTime
	)
	err := tx.QueryRowContext(ctx, `
		SELECT
			k.usage_5h,
			k.usage_1d,
			k.usage_7d,
			k.window_5h_start,
			k.window_1d_start,
			k.window_7d_start,
			NULLIF(k.rate_limit_1d, 0),
			g.daily_limit_usd,
			NULLIF(k.rate_limit_7d, 0),
			g.weekly_limit_usd
		FROM api_keys k
		LEFT JOIN groups g ON g.id = k.group_id AND g.deleted_at IS NULL
		WHERE k.id = $1 AND k.deleted_at IS NULL
		FOR UPDATE OF k`,
		apiKeyID,
	).Scan(&usage5h, &usage1d, &usage7d, &w5h, &w1d, &w7d, &limit1d, &groupDailyLimit, &limit7d, &groupWeeklyLimit)
	if err != nil {
		return nil, err
	}
	state := &apiKeyRateLimitSQLState{
		usage5h: usage5h,
		usage1d: usage1d,
		usage7d: usage7d,
		limit1d: nullableFloat(limit1d),
		limit7d: nullableFloat(limit7d),
	}
	if state.limit1d <= 0 {
		state.limit1d = nullableFloat(groupDailyLimit)
	}
	if state.limit7d <= 0 {
		state.limit7d = nullableFloat(groupWeeklyLimit)
	}
	if w5h.Valid {
		state.w5h = &w5h.Time
	}
	if w1d.Valid {
		state.w1d = &w1d.Time
	}
	if w7d.Valid {
		state.w7d = &w7d.Time
	}
	return state, nil
}

func (s *apiKeyRateLimitSQLState) resetExpired(now time.Time) {
	if s.w5h == nil || s.w5h.Add(service.RateLimitWindow5h).Before(now) || s.w5h.Add(service.RateLimitWindow5h).Equal(now) {
		s.usage5h = 0
		s.w5h = &now
	}
	if s.w1d == nil || s.w1d.Add(service.RateLimitWindow1d).Before(now) || s.w1d.Add(service.RateLimitWindow1d).Equal(now) {
		s.usage1d = 0
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		s.w1d = &start
	}
	if s.w7d == nil || s.w7d.Add(service.RateLimitWindow7d).Before(now) || s.w7d.Add(service.RateLimitWindow7d).Equal(now) {
		s.usage7d = 0
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		s.w7d = &start
	}
}

func updateAPIKeyRateLimitState(ctx context.Context, tx *sql.Tx, apiKeyID int64, state *apiKeyRateLimitSQLState, usage5hAdd, usage1dAdd, usage7dAdd float64) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE api_keys
		SET usage_5h = $1,
			usage_1d = $2,
			usage_7d = $3,
			window_5h_start = $4,
			window_1d_start = $5,
			window_7d_start = $6,
			updated_at = NOW()
		WHERE id = $7 AND deleted_at IS NULL`,
		state.usage5h+usage5hAdd,
		state.usage1d+usage1dAdd,
		state.usage7d+usage7dAdd,
		state.w5h,
		state.w1d,
		state.w7d,
		apiKeyID,
	)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

func allocateAPIKeyTokenPackages(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64, requestID, requestFingerprint, model string) error {
	remaining := amount
	rows, err := tx.QueryContext(ctx, `
		SELECT id, amount_usd, used_usd
		FROM api_key_token_packages
		WHERE api_key_id = $1
			AND started_at <= NOW()
			AND amount_usd > used_usd
		ORDER BY started_at ASC, id ASC
		FOR UPDATE`,
		apiKeyID,
	)
	if err != nil {
		return err
	}

	type pkgRow struct {
		id     int64
		amount float64
		used   float64
	}
	packages := make([]pkgRow, 0)
	for rows.Next() {
		var p pkgRow
		if err := rows.Scan(&p.id, &p.amount, &p.used); err != nil {
			return err
		}
		packages = append(packages, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, p := range packages {
		if remaining <= 0 {
			break
		}
		available := p.amount - p.used
		if available <= 0 {
			continue
		}
		covered := math.Min(remaining, available)
		if _, err := tx.ExecContext(ctx, `
			UPDATE api_key_token_packages
			SET used_usd = used_usd + $1, updated_at = NOW()
			WHERE id = $2`,
			covered, p.id,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_key_token_package_usage
				(package_id, api_key_id, request_id, request_fingerprint, model, cost_usd)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6)`,
			p.id, apiKeyID, strings.TrimSpace(requestID), strings.TrimSpace(requestFingerprint), strings.TrimSpace(model), covered,
		); err != nil {
			return err
		}
		remaining -= covered
	}
	return nil
}

func positiveRemaining(limit, used float64) float64 {
	remaining := limit - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

func nullableFloat(v sql.NullFloat64) float64 {
	if !v.Valid {
		return 0
	}
	return v.Float64
}

func incrementUsageBillingAccountQuota(ctx context.Context, tx *sql.Tx, accountID int64, amount float64) (*service.AccountQuotaState, error) {
	rows, err := tx.QueryContext(ctx,
		`UPDATE accounts SET extra = (
			COALESCE(extra, '{}'::jsonb)
			|| jsonb_build_object('quota_used', COALESCE((extra->>'quota_used')::numeric, 0) + $1)
			|| CASE WHEN COALESCE((extra->>'quota_daily_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_daily_used',
					CASE WHEN `+dailyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_daily_used')::numeric, 0) + $1 END,
					'quota_daily_start',
					CASE WHEN `+dailyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_daily_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+dailyExpiredExpr+` AND `+nextDailyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_daily_reset_at', `+nextDailyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
			|| CASE WHEN COALESCE((extra->>'quota_weekly_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_weekly_used',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_weekly_used')::numeric, 0) + $1 END,
					'quota_weekly_start',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_weekly_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+weeklyExpiredExpr+` AND `+nextWeeklyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_weekly_reset_at', `+nextWeeklyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
		), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING
			COALESCE((extra->>'quota_used')::numeric, 0),
			COALESCE((extra->>'quota_limit')::numeric, 0),
			COALESCE((extra->>'quota_daily_used')::numeric, 0),
			COALESCE((extra->>'quota_daily_limit')::numeric, 0),
			COALESCE((extra->>'quota_weekly_used')::numeric, 0),
			COALESCE((extra->>'quota_weekly_limit')::numeric, 0)`,
		amount, accountID)
	if err != nil {
		return nil, err
	}

	var state service.AccountQuotaState
	if rows.Next() {
		if err := rows.Scan(
			&state.TotalUsed, &state.TotalLimit,
			&state.DailyUsed, &state.DailyLimit,
			&state.WeeklyUsed, &state.WeeklyLimit,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
	} else {
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		return nil, service.ErrAccountNotFound
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// 必须在执行下一条 SQL 前显式关闭 rows：pq 驱动在同一连接上
	// 不允许前一条查询的结果集未耗尽时启动新查询，否则会返回
	// "unexpected Parse response" 错误。
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// 任意维度额度在本次递增中从"未超"跨越到"已超"时，必须刷新调度快照，
	// 否则 Redis 中缓存的 Account 仍显示旧的 used 值，后续请求会继续选中本账号，
	// 最终观察到 daily_used / weekly_used 大幅超过配置的 limit。
	// 对于日/周额度，即使本次触发了周期重置（pre=0、post=amount），
	// 判定式 (post-amount) < limit 同样成立，逻辑与总额度保持一致。
	crossedTotal := state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed-amount) < state.TotalLimit
	crossedDaily := state.DailyLimit > 0 && state.DailyUsed >= state.DailyLimit && (state.DailyUsed-amount) < state.DailyLimit
	crossedWeekly := state.WeeklyLimit > 0 && state.WeeklyUsed >= state.WeeklyLimit && (state.WeeklyUsed-amount) < state.WeeklyLimit
	if crossedTotal || crossedDaily || crossedWeekly {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			logger.LegacyPrintf("repository.usage_billing", "[SchedulerOutbox] enqueue quota exceeded failed: account=%d err=%v", accountID, err)
			return nil, err
		}
	}
	return &state, nil
}
