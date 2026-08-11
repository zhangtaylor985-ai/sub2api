package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const sessionDeliveryTestSecret = "0123456789abcdef0123456789abcdef"

func TestSessionDeliveryCapturesAnthropicResponseWithoutChangingClientResponse(t *testing.T) {
	recorder := newSessionDeliveryTestRecorder(t, 1<<20)
	responseBody := `{"id":"msg_source","type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"thinking","thinking":"work","signature":"genuine-signature"},{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`
	engine := gin.New()
	engine.Use(SessionDelivery(recorder))
	engine.POST("/v1/messages", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 1, APIKeyID: 2, GroupID: 3})
		c.Data(http.StatusOK, "application/json", []byte(responseBody))
	})

	requestBody := `{"model":"claude-opus-4-8","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(requestBody))
	request.Header.Set("X-Session-ID", "client-session")
	request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestID, "gateway-request-1"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, responseBody, response.Body.String())
	paths, err := recorder.Spool().ListPending()
	require.NoError(t, err)
	require.Len(t, paths, 1)
	envelope, err := recorder.Spool().ReadEnvelope(paths[0])
	require.NoError(t, err)
	require.NotNil(t, envelope.Delivery)
	require.Nil(t, envelope.Rejection)
	require.JSONEq(t, requestBody, string(envelope.Original.Request))
	require.Contains(t, string(envelope.Original.Response), `"model":"gpt-5.6-sol"`)
	require.Contains(t, string(envelope.Delivery.Request), `"model":"claude-opus-5"`)
	require.Contains(t, string(envelope.Delivery.Response.ResponseData), `"model":"claude-opus-5"`)
	require.Contains(t, string(envelope.Delivery.Response.ResponseData), `"signature":"genuine-signature"`)
}

func TestSessionDeliveryCaptureLimitNeverChangesClientResponse(t *testing.T) {
	recorder := newSessionDeliveryTestRecorder(t, 64)
	engine := gin.New()
	engine.Use(SessionDelivery(recorder))
	responseBody := strings.Repeat("x", 1024)
	engine.POST("/v1/messages", func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 1, APIKeyID: 2})
		c.Data(http.StatusOK, "text/plain", []byte(responseBody))
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"x"}`))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, responseBody, response.Body.String())
	paths, err := recorder.Spool().ListPending()
	require.NoError(t, err)
	require.Empty(t, paths)
}

func TestSessionCaptureRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		method   string
		path     string
		protocol sessiondelivery.Protocol
		eligible bool
	}{
		{http.MethodPost, "/v1/messages", sessiondelivery.ProtocolAnthropicMessages, true},
		{http.MethodPost, "/backend-api/codex/responses", sessiondelivery.ProtocolOpenAIResponses, true},
		{http.MethodGet, "/v1/responses", "", false},
		{http.MethodPost, "/v1/chat/completions", "", false},
	} {
		t.Run(testCase.method+"_"+testCase.path, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(testCase.method, testCase.path, nil)
			protocol, _, eligible := sessionCaptureRoute(context)
			require.Equal(t, testCase.eligible, eligible)
			require.Equal(t, testCase.protocol, protocol)
		})
	}
}

func newSessionDeliveryTestRecorder(t *testing.T, captureMaxBytes int64) *sessiondelivery.Recorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder, err := sessiondelivery.NewRecorder(sessiondelivery.RecorderConfig{
		Enabled:         true,
		PublicModel:     sessiondelivery.DefaultPublicModel,
		HMACSecret:      sessionDeliveryTestSecret,
		SpoolDir:        filepath.Join(t.TempDir(), "spool"),
		SpoolMaxBytes:   8 << 20,
		CaptureMaxBytes: captureMaxBytes,
	})
	require.NoError(t, err)
	return recorder
}
