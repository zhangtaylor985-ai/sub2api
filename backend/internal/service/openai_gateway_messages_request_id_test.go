package service

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

func serviceTestContextWithRequestID(requestID string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if requestID != "" {
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.RequestID, requestID))
	}
	c.Request = req
	return c, rec
}

func serviceSSEDataJSON(t *testing.T, body string) string {
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

func TestWriteAnthropicErrorIncludesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := serviceTestContextWithRequestID("rid-service-json")

	writeAnthropicError(c, http.StatusBadGateway, "api_error", "Upstream request failed")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "error", gjson.GetBytes(rec.Body.Bytes(), "type").String())
	require.Equal(t, "api_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream request failed", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
	require.Equal(t, "rid-service-json", gjson.GetBytes(rec.Body.Bytes(), "request_id").String())
}

func TestWriteAnthropicStreamErrorEventIncludesRequestID(t *testing.T) {
	var out strings.Builder

	writeAnthropicStreamErrorEvent(&out, "rid-service-stream", "api_error", "Upstream stream error")

	data := serviceSSEDataJSON(t, out.String())
	require.Equal(t, "error", gjson.Get(data, "type").String())
	require.Equal(t, "api_error", gjson.Get(data, "error.type").String())
	require.Equal(t, "Upstream stream error", gjson.Get(data, "error.message").String())
	require.Equal(t, "rid-service-stream", gjson.Get(data, "request_id").String())
}
