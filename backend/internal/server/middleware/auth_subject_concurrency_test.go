package middleware

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuthSubjectFromAPIKeyConcurrencyScope(t *testing.T) {
	user := &service.User{ID: 7, Concurrency: 10}
	groupID := int64(3)

	tests := []struct {
		name              string
		key               *service.APIKey
		wantConcurrency   int
		wantScope         ConcurrencyScope
		wantScopeID       int64
		wantResolvedScope ConcurrencyScope
		wantResolvedID    int64
	}{
		{
			name: "key override",
			key: &service.APIKey{
				ID:          11,
				User:        user,
				GroupID:     &groupID,
				Group:       &service.Group{ID: groupID, Concurrency: 2},
				Concurrency: 4,
			},
			wantConcurrency:   4,
			wantScope:         ConcurrencyScopeAPIKey,
			wantScopeID:       11,
			wantResolvedScope: ConcurrencyScopeAPIKey,
			wantResolvedID:    11,
		},
		{
			name: "group inherited limit uses api key scope",
			key: &service.APIKey{
				ID:      12,
				User:    user,
				GroupID: &groupID,
				Group:   &service.Group{ID: groupID, Concurrency: 2},
			},
			wantConcurrency:   2,
			wantScope:         ConcurrencyScopeAPIKey,
			wantScopeID:       12,
			wantResolvedScope: ConcurrencyScopeAPIKey,
			wantResolvedID:    12,
		},
		{
			name: "user fallback",
			key: &service.APIKey{
				ID:   13,
				User: user,
			},
			wantConcurrency:   10,
			wantScope:         ConcurrencyScopeUser,
			wantScopeID:       user.ID,
			wantResolvedScope: ConcurrencyScopeUser,
			wantResolvedID:    user.ID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := authSubjectFromAPIKey(tt.key)
			require.Equal(t, user.ID, got.UserID)
			require.Equal(t, tt.key.ID, got.APIKeyID)
			require.Equal(t, tt.wantConcurrency, got.Concurrency)
			require.Equal(t, tt.wantScope, got.ConcurrencyScope)
			require.Equal(t, tt.wantScopeID, got.ConcurrencyScopeID)
			scope, id := got.ResolvedConcurrencyScope()
			require.Equal(t, tt.wantResolvedScope, scope)
			require.Equal(t, tt.wantResolvedID, id)
		})
	}
}
