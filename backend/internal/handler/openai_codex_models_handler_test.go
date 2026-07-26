package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexModelsRejectsClaudeOnlyOpenAIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(42)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/backend-api/codex/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		GroupID:              &groupID,
		AllowClaudeFamily:    true,
		AllowGPTFamily:       false,
		ModelFamilyPolicySet: true,
		Group:                &service.Group{ID: groupID, Platform: service.PlatformOpenAI},
	})

	(&OpenAIGatewayHandler{}).CodexModels(c)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "gpt")
}

func TestCodexModelsRejectsNonOpenAIGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(43)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/backend-api/codex/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		GroupID:              &groupID,
		AllowGPTFamily:       true,
		ModelFamilyPolicySet: true,
		Group:                &service.Group{ID: groupID, Platform: service.PlatformAnthropic},
	})

	(&OpenAIGatewayHandler{}).CodexModels(c)

	require.Equal(t, http.StatusNotFound, recorder.Code)
}
