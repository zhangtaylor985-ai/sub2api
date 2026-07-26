package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutesCodexModelsManifestPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet {
			registered[route.Path] = true
		}
	}

	require.True(t, registered["/backend-api/codex/models"])
	require.True(t, registered["/v1/models"])
}

func TestShouldDispatchCodexModelsManifest(t *testing.T) {
	tests := []struct {
		name          string
		platform      string
		clientVersion string
		apiKey        *service.APIKey
		want          bool
	}{
		{
			name:          "OpenAI GPT-enabled key",
			platform:      service.PlatformOpenAI,
			clientVersion: "0.137.0",
			apiKey:        &service.APIKey{AllowGPTFamily: true, ModelFamilyPolicySet: true},
			want:          true,
		},
		{
			name:          "OpenAI legacy policy defaults to GPT-enabled",
			platform:      service.PlatformOpenAI,
			clientVersion: "0.137.0",
			apiKey:        &service.APIKey{},
			want:          true,
		},
		{
			name:          "Claude-only OpenAI key keeps ordinary models handler",
			platform:      service.PlatformOpenAI,
			clientVersion: "0.137.0",
			apiKey:        &service.APIKey{AllowClaudeFamily: true, AllowGPTFamily: false, ModelFamilyPolicySet: true},
			want:          false,
		},
		{
			name:          "ordinary OpenAI models request",
			platform:      service.PlatformOpenAI,
			clientVersion: "",
			apiKey:        &service.APIKey{AllowGPTFamily: true, ModelFamilyPolicySet: true},
			want:          false,
		},
		{
			name:          "non-OpenAI group",
			platform:      service.PlatformAnthropic,
			clientVersion: "0.137.0",
			apiKey:        &service.APIKey{AllowGPTFamily: true, ModelFamilyPolicySet: true},
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version="+tt.clientVersion, nil)
			if tt.apiKey != nil {
				groupID := int64(1)
				tt.apiKey.GroupID = &groupID
				tt.apiKey.Group = &service.Group{ID: groupID, Platform: tt.platform}
				c.Set(string(servermiddleware.ContextKeyAPIKey), tt.apiKey)
			}
			require.Equal(t, tt.want, shouldDispatchCodexModelsManifest(c))
		})
	}
}

func TestGatewayRoutesClaudeOnlyOpenAIKeyKeepsClaudeModelsList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(51)
	apiKey := &service.APIKey{
		GroupID:              &groupID,
		AllowClaudeFamily:    true,
		AllowGPTFamily:       false,
		ModelFamilyPolicySet: true,
		Group: &service.Group{
			ID:                    groupID,
			Platform:              service.PlatformOpenAI,
			AllowMessagesDispatch: true,
		},
	}

	router := gin.New()
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		&config.Config{},
	)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.137.0", nil)
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)

	var response struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "list", response.Object)
	ids := make([]string, 0, len(response.Data))
	for _, model := range response.Data {
		ids = append(ids, model.ID)
	}
	require.Contains(t, ids, "claude-opus-4-8")
	for _, id := range ids {
		require.NotEqual(t, service.APIKeyModelFamilyGPT, service.RequestedModelFamily(id), "GPT model %q must not be exposed", id)
	}
}
