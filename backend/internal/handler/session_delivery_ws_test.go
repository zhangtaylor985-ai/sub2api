package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestOpenAIWSSessionCaptureBuildsCodexAnthropicDelivery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder, err := sessiondelivery.NewRecorder(sessiondelivery.RecorderConfig{
		Enabled:         true,
		PublicModel:     sessiondelivery.DefaultPublicModel,
		HMACSecret:      "0123456789abcdef0123456789abcdef",
		SpoolDir:        filepath.Join(t.TempDir(), "spool"),
		SpoolMaxBytes:   8 << 20,
		CaptureMaxBytes: 1 << 20,
	})
	require.NoError(t, err)

	engine := gin.New()
	engine.Use(middleware2.SessionDelivery(recorder))
	engine.GET("/v1/responses", func(c *gin.Context) {
		capture := newOpenAIWSSessionCapture(c, middleware2.AuthSubject{UserID: 1, APIKeyID: 2, GroupID: 3})
		capture.remember(1, []byte(`{"type":"response.create","model":"gpt-5.6-sol","prompt_cache_key":"codex-ws-session","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`))
		capture.finish(1, &service.OpenAIForwardResult{
			Duration:          1500 * time.Millisecond,
			WSTerminalPayload: []byte(`{"type":"response.completed","response":{"id":"resp_ws","object":"response","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`),
		}, nil, zap.NewNop())
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestID, "gateway-ws-request"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)

	paths, err := recorder.Spool().ListPending()
	require.NoError(t, err)
	require.Len(t, paths, 1)
	envelope, err := recorder.Spool().ReadEnvelope(paths[0])
	require.NoError(t, err)
	require.NotNil(t, envelope.Delivery)
	require.Nil(t, envelope.Rejection)
	require.Contains(t, string(envelope.Original.Request), `"model":"gpt-5.6-sol"`)
	require.Contains(t, string(envelope.Delivery.Request), `"model":"claude-opus-5"`)
	require.Contains(t, string(envelope.Delivery.Response.ResponseData), `"model":"claude-opus-5"`)
	require.NotContains(t, string(envelope.Delivery.Response.ResponseData), "gpt-5.6")
}
