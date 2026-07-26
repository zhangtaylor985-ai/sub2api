//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type reauthorizationAdminService struct {
	*stubAdminService
	account     *service.Account
	updateInput *service.UpdateAccountInput
}

func (s *reauthorizationAdminService) GetAccount(_ context.Context, _ int64) (*service.Account, error) {
	return s.account, nil
}

func (s *reauthorizationAdminService) UpdateAccount(_ context.Context, _ int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updateInput = input
	s.account.Type = input.Type
	s.account.Credentials = input.Credentials
	return s.account, nil
}

func (s *reauthorizationAdminService) ClearAccountError(_ context.Context, _ int64) (*service.Account, error) {
	return s.account, nil
}

func TestApplyOAuthCredentials_PreservesExistingCredentialPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminService := &reauthorizationAdminService{
		stubAdminService: newStubAdminService(),
		account: &service.Account{
			ID:       6,
			Name:     "openai-free",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeOAuth,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"model_exclusions": []any{"gpt-5.6-*"},
			},
		},
	}
	handler := NewAccountHandler(adminService, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/api/v1/admin/accounts/:id/apply-oauth-credentials", handler.ApplyOAuthCredentials)

	payload, err := json.Marshal(ApplyOAuthCredentialsRequest{
		Type: service.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "at-new",
			"refresh_token": "rt-new",
		},
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/6/apply-oauth-credentials", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, adminService.updateInput)
	require.True(t, adminService.updateInput.PreserveExistingCredentials)
}
