//go:build integration

package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GatewayCacheSuite struct {
	IntegrationRedisSuite
	cache *gatewayCache
}

func (s *GatewayCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.cache = &gatewayCache{rdb: s.rdb}
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_Missing() {
	_, err := s.cache.GetSessionAccountID(s.ctx, 1, "nonexistent")
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing session")
}

func (s *GatewayCacheSuite) TestSetAndGetSessionAccountID() {
	sessionID := "s1"
	accountID := int64(99)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sid, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err, "GetSessionAccountID")
	require.Equal(s.T(), accountID, sid, "session id mismatch")
}

func (s *GatewayCacheSuite) TestSessionAccountID_TTL() {
	sessionID := "s2"
	accountID := int64(100)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL sessionKey after Set")
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL() {
	sessionID := "s3"
	accountID := int64(101)
	groupID := int64(1)
	initialTTL := 1 * time.Minute
	refreshTTL := 3 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, initialTTL), "SetSessionAccountID")

	require.NoError(s.T(), s.cache.RefreshSessionTTL(s.ctx, groupID, sessionID, refreshTTL), "RefreshSessionTTL")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL after Refresh")
	s.AssertTTLWithin(ttl, 1*time.Second, refreshTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL_MissingKey() {
	// RefreshSessionTTL on a missing key should not error (no-op)
	err := s.cache.RefreshSessionTTL(s.ctx, 1, "missing-session", 1*time.Minute)
	require.NoError(s.T(), err, "RefreshSessionTTL on missing key should not error")
}

func (s *GatewayCacheSuite) TestDeleteSessionAccountID() {
	sessionID := "openai:s4"
	accountID := int64(102)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")
	require.NoError(s.T(), s.cache.DeleteSessionAccountID(s.ctx, groupID, sessionID), "DeleteSessionAccountID")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil after delete")
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_CorruptedValue() {
	sessionID := "corrupted"
	groupID := int64(1)
	sessionKey := buildSessionKey(groupID, sessionID)

	// Set a non-integer value
	require.NoError(s.T(), s.rdb.Set(s.ctx, sessionKey, "not-a-number", 1*time.Minute).Err(), "Set invalid value")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.Error(s.T(), err, "expected error for corrupted value")
	require.False(s.T(), errors.Is(err, redis.Nil), "expected parsing error, not redis.Nil")
}

func (s *GatewayCacheSuite) TestOpenAIStickySessionIndexTracksLifecycle() {
	accountID := int64(103)
	groupID := int64(1)
	sessionID := "openai:session-index"

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, 3*time.Minute))
	count, err := s.cache.GetOpenAIActiveStickySessionCount(s.ctx, accountID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, count)

	require.NoError(s.T(), s.cache.RefreshSessionTTL(s.ctx, groupID, sessionID, 5*time.Minute))
	count, err = s.cache.GetOpenAIActiveStickySessionCount(s.ctx, accountID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, count)

	require.NoError(s.T(), s.cache.DeleteSessionAccountID(s.ctx, groupID, sessionID))
	count, err = s.cache.GetOpenAIActiveStickySessionCount(s.ctx, accountID)
	require.NoError(s.T(), err)
	require.Zero(s.T(), count)
}

func (s *GatewayCacheSuite) TestOpenAIStickySessionIndexMovesReboundSession() {
	groupID := int64(1)
	sessionID := "openai:session-rebound"

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, 104, time.Minute))
	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, 105, time.Minute))

	oldCount, err := s.cache.GetOpenAIActiveStickySessionCount(s.ctx, 104)
	require.NoError(s.T(), err)
	require.Zero(s.T(), oldCount)
	newCount, err := s.cache.GetOpenAIActiveStickySessionCount(s.ctx, 105)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, newCount)
}

func (s *GatewayCacheSuite) TestOpenAIStickySessionIndexPrunesExpiredBinding() {
	accountID := int64(106)
	groupID := int64(1)
	sessionID := "openai:session-expiring"

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, 100*time.Millisecond))
	require.Eventually(s.T(), func() bool {
		count, err := s.cache.GetOpenAIActiveStickySessionCount(s.ctx, accountID)
		return err == nil && count == 0
	}, 3*time.Second, 100*time.Millisecond)
}

func (s *GatewayCacheSuite) TestOpenAIStickySessionIndexTTLIsNotShortenedByLegacyBinding() {
	accountID := int64(107)
	groupID := int64(1)

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, "openai:primary", accountID, 5*time.Minute))
	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, "openai:legacy", accountID, time.Minute))

	ttl, err := s.rdb.TTL(s.ctx, openAIStickyAccountIndexKey(accountID)).Result()
	require.NoError(s.T(), err)
	require.Greater(s.T(), ttl, 4*time.Minute)
}

func TestGatewayCacheSuite(t *testing.T) {
	suite.Run(t, new(GatewayCacheSuite))
}
