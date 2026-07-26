package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CodexModels serves the live ChatGPT Codex models manifest to compatible
// Codex clients. It is intentionally limited to GPT-enabled API keys in
// OpenAI groups so Claude-only dispatch keys retain their Claude model view.
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if apiKey.Group.Platform != service.PlatformOpenAI || !apiKey.AllowsGPTFamily() {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is not available for this API key")
		return
	}

	manifest, err := h.gatewayService.FetchCodexModelsManifestWithFailover(
		c.Request.Context(),
		apiKey.GroupID,
		c.Query("client_version"),
		c.GetHeader("If-None-Match"),
		h.maxAccountSwitches,
	)
	if err != nil {
		h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
		return
	}

	if manifest.ETag != "" {
		c.Header("ETag", manifest.ETag)
	}
	if manifest.NotModified {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json", manifest.Body)
}
