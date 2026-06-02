package requestid

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(ctxkey.RequestID).(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}

func FromRequest(req *http.Request) string {
	if req == nil {
		return ""
	}
	return FromContext(req.Context())
}
