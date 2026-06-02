package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func testContextWithRequestID(path, requestID string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if requestID != "" {
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.RequestID, requestID))
	}
	c.Request = req
	return c, rec
}

func sseDataJSON(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	t.Fatalf("SSE data line missing in body: %q", body)
	return ""
}

func TestGatewayAnthropicErrorResponseIncludesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := testContextWithRequestID("/v1/messages", "rid-anthropic")

	(&GatewayHandler{}).errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "error", gjson.GetBytes(rec.Body.Bytes(), "type").String())
	require.Equal(t, "api_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream request failed", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
	require.Equal(t, "rid-anthropic", gjson.GetBytes(rec.Body.Bytes(), "request_id").String())
}

func TestOpenAIErrorResponseIncludesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := testContextWithRequestID("/v1/chat/completions", "rid-openai")

	(&OpenAIGatewayHandler{}).errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "api_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream request failed", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
	require.Equal(t, "rid-openai", gjson.GetBytes(rec.Body.Bytes(), "error.request_id").String())
}

func TestResponsesErrorResponseIncludesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := testContextWithRequestID("/v1/responses", "rid-responses")

	(&GatewayHandler{}).responsesErrorResponse(c, http.StatusBadGateway, "server_error", "All available accounts exhausted")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "server_error", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Equal(t, "All available accounts exhausted", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
	require.Equal(t, "rid-responses", gjson.GetBytes(rec.Body.Bytes(), "error.request_id").String())
}

func TestAnthropicStreamingErrorIncludesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := testContextWithRequestID("/v1/messages", "rid-stream")

	(&OpenAIGatewayHandler{}).anthropicStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream stream error", true)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "event: error")
	data := sseDataJSON(t, rec.Body.String())
	require.Equal(t, "rid-stream", gjson.Get(data, "request_id").String())
	require.Equal(t, "api_error", gjson.Get(data, "error.type").String())
}

func TestResponsesFailedSSEIncludesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := testContextWithRequestID("/v1/responses", "rid-failed")

	require.True(t, writeResponsesFailedSSE(c, "api_error", "Upstream request failed"))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "event: response.failed")
	data := sseDataJSON(t, rec.Body.String())
	require.Equal(t, "rid-failed", gjson.Get(data, "response.error.request_id").String())
	require.Equal(t, "server_error", gjson.Get(data, "response.error.code").String())
}
