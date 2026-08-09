package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminUsageAPIKeySearchRepo struct {
	service.APIKeyRepository
	keyword string
	userID  int64
}

func (r *adminUsageAPIKeySearchRepo) SearchAPIKeys(_ context.Context, userID int64, keyword string, _ int) ([]service.APIKey, error) {
	r.userID = userID
	r.keyword = keyword
	return []service.APIKey{{
		ID:     496,
		UserID: 84,
		Key:    keyword,
		Name:   "Customer Key",
	}}, nil
}

func TestAdminUsageSearchAPIKeysSecureUsesJSONBodyAndHidesRawKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &adminUsageAPIKeySearchRepo{}
	apiKeyService := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, nil)
	handler := NewUsageHandler(nil, apiKeyService, nil, nil)
	router := gin.New()
	router.POST("/admin/usage/search-api-keys", handler.SearchAPIKeysSecure)

	const exactKey = "sk-test-exact-value-not-a-real-key"
	req := httptest.NewRequest(http.MethodPost, "/admin/usage/search-api-keys", bytes.NewBufferString(`{"keyword":"`+exactKey+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, exactKey, repo.keyword)
	require.Zero(t, repo.userID)
	require.NotContains(t, rec.Body.String(), exactKey)
	require.Contains(t, rec.Body.String(), "Customer Key")
}
