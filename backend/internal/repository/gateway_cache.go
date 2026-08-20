package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	stickySessionPrefix            = "sticky_session:"
	openAIStickyAccountIndexPrefix = "openai_sticky_account:"
)

var (
	setOpenAIStickySessionScript = redis.NewScript(`
		local sessionKey = KEYS[1]
		local indexPrefix = ARGV[1]
		local accountID = ARGV[2]
		local ttl = tonumber(ARGV[3])
		local oldAccountID = redis.call('GET', sessionKey)
		local timeResult = redis.call('TIME')
		local expiresAt = tonumber(timeResult[1]) + ttl

		if oldAccountID and oldAccountID ~= accountID then
			redis.call('ZREM', indexPrefix .. oldAccountID, sessionKey)
		end
		redis.call('SET', sessionKey, accountID, 'EX', ttl)
		local indexKey = indexPrefix .. accountID
		redis.call('ZADD', indexKey, expiresAt, sessionKey)
		local desiredIndexTTL = ttl + 60
		if redis.call('TTL', indexKey) < desiredIndexTTL then
			redis.call('EXPIRE', indexKey, desiredIndexTTL)
		end
		return 1
	`)

	refreshOpenAIStickySessionScript = redis.NewScript(`
		local sessionKey = KEYS[1]
		local indexPrefix = ARGV[1]
		local ttl = tonumber(ARGV[2])
		local accountID = redis.call('GET', sessionKey)
		if not accountID then
			return 0
		end
		local timeResult = redis.call('TIME')
		local expiresAt = tonumber(timeResult[1]) + ttl
		redis.call('EXPIRE', sessionKey, ttl)
		local indexKey = indexPrefix .. accountID
		redis.call('ZADD', indexKey, expiresAt, sessionKey)
		local desiredIndexTTL = ttl + 60
		if redis.call('TTL', indexKey) < desiredIndexTTL then
			redis.call('EXPIRE', indexKey, desiredIndexTTL)
		end
		return 1
	`)

	deleteOpenAIStickySessionScript = redis.NewScript(`
		local sessionKey = KEYS[1]
		local indexPrefix = ARGV[1]
		local accountID = redis.call('GET', sessionKey)
		redis.call('DEL', sessionKey)
		if accountID then
			redis.call('ZREM', indexPrefix .. accountID, sessionKey)
		end
		return 1
	`)

	countOpenAIStickySessionsScript = redis.NewScript(`
		local indexKey = KEYS[1]
		local timeResult = redis.call('TIME')
		local now = tonumber(timeResult[1])
		redis.call('ZREMRANGEBYSCORE', indexKey, '-inf', now)
		return redis.call('ZCARD', indexKey)
	`)
)

type gatewayCache struct {
	rdb *redis.Client
}

func NewGatewayCache(rdb *redis.Client) service.GatewayCache {
	return &gatewayCache{rdb: rdb}
}

// buildSessionKey 构建 session key，包含 groupID 实现分组隔离
// 格式: sticky_session:{groupID}:{sessionHash}
func buildSessionKey(groupID int64, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s", stickySessionPrefix, groupID, sessionHash)
}

func openAIStickyAccountIndexKey(accountID int64) string {
	return fmt.Sprintf("%s%d", openAIStickyAccountIndexPrefix, accountID)
}

func isOpenAIStickySessionKey(sessionHash string) bool {
	return strings.HasPrefix(strings.TrimSpace(sessionHash), "openai:")
}

func stickyTTLSeconds(ttl time.Duration) int64 {
	seconds := int64((ttl + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (c *gatewayCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Get(ctx, key).Int64()
}

func (c *gatewayCache) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	if isOpenAIStickySessionKey(sessionHash) && accountID > 0 && ttl > 0 {
		return setOpenAIStickySessionScript.Run(
			ctx,
			c.rdb,
			[]string{key},
			openAIStickyAccountIndexPrefix,
			accountID,
			stickyTTLSeconds(ttl),
		).Err()
	}
	return c.rdb.Set(ctx, key, accountID, ttl).Err()
}

func (c *gatewayCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	if isOpenAIStickySessionKey(sessionHash) && ttl > 0 {
		return refreshOpenAIStickySessionScript.Run(
			ctx,
			c.rdb,
			[]string{key},
			openAIStickyAccountIndexPrefix,
			stickyTTLSeconds(ttl),
		).Err()
	}
	return c.rdb.Expire(ctx, key, ttl).Err()
}

// DeleteSessionAccountID 删除粘性会话与账号的绑定关系。
// 当检测到绑定的账号不可用（如状态错误、禁用、不可调度等）时调用，
// 以便下次请求能够重新选择可用账号。
//
// DeleteSessionAccountID removes the sticky session binding for the given session.
// Called when the bound account becomes unavailable (e.g., error status, disabled,
// or unschedulable), allowing subsequent requests to select a new available account.
func (c *gatewayCache) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	key := buildSessionKey(groupID, sessionHash)
	if isOpenAIStickySessionKey(sessionHash) {
		return deleteOpenAIStickySessionScript.Run(
			ctx,
			c.rdb,
			[]string{key},
			openAIStickyAccountIndexPrefix,
		).Err()
	}
	return c.rdb.Del(ctx, key).Err()
}

func (c *gatewayCache) GetOpenAIActiveStickySessionCount(ctx context.Context, accountID int64) (int, error) {
	if accountID <= 0 {
		return 0, nil
	}
	return countOpenAIStickySessionsScript.Run(
		ctx,
		c.rdb,
		[]string{openAIStickyAccountIndexKey(accountID)},
	).Int()
}
