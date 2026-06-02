package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRejectAPIKeyModelFamilyPolicy(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		apiKey         *service.APIKey
		model          string
		openAIEndpoint bool
		wantRejected   bool
	}{
		{
			name:           "claude only allows claude messages route",
			apiKey:         &service.APIKey{AllowClaudeFamily: true, AllowGPTFamily: false, ModelFamilyPolicySet: true},
			model:          "claude-opus-4-7",
			openAIEndpoint: false,
			wantRejected:   false,
		},
		{
			name:           "claude only rejects openai route even for claude model",
			apiKey:         &service.APIKey{AllowClaudeFamily: true, AllowGPTFamily: false, ModelFamilyPolicySet: true},
			model:          "claude-opus-4-7",
			openAIEndpoint: true,
			wantRejected:   true,
		},
		{
			name:           "gpt only rejects claude model",
			apiKey:         &service.APIKey{AllowClaudeFamily: false, AllowGPTFamily: true, ModelFamilyPolicySet: true},
			model:          "claude-sonnet-4-6",
			openAIEndpoint: false,
			wantRejected:   true,
		},
		{
			name:           "both allows openai route",
			apiKey:         &service.APIKey{AllowClaudeFamily: true, AllowGPTFamily: true, ModelFamilyPolicySet: true},
			model:          "gpt-5.5",
			openAIEndpoint: true,
			wantRejected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

			rejected := rejectAPIKeyModelFamilyPolicy(c, tt.apiKey, tt.model, tt.openAIEndpoint, func(c *gin.Context, status int, errType, message string) {
				c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
			})
			require.Equal(t, tt.wantRejected, rejected)
			if !tt.wantRejected {
				return
			}
			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
			require.Equal(t, service.APIKeyModelAccessDeniedMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
			require.True(t, service.HasOpsClientBusinessLimited(c))
		})
	}
}
