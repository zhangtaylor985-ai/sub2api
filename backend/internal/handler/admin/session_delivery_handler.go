package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SessionDeliveryHandler struct {
	service *service.SessionDeliveryAdminService
}

func NewSessionDeliveryHandler(service *service.SessionDeliveryAdminService) *SessionDeliveryHandler {
	return &SessionDeliveryHandler{service: service}
}

func (h *SessionDeliveryHandler) Overview(c *gin.Context) {
	if h == nil || h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "Session delivery service is unavailable")
		return
	}
	overview, err := h.service.Overview(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, overview)
}

func (h *SessionDeliveryHandler) GetPolicy(c *gin.Context) {
	if h == nil || h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "Session delivery service is unavailable")
		return
	}
	page, pageSize := response.ParsePagination(c)
	summary, keys, err := h.service.GetPolicy(c.Request.Context(), c.Query("q"), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"summary": summary, "api_keys": keys})
}

func (h *SessionDeliveryHandler) UpdateMode(c *gin.Context) {
	var request struct {
		Mode service.SessionCaptureMode `json:"mode" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid Session capture mode")
		return
	}
	if request.Mode != service.SessionCaptureModeAll && request.Mode != service.SessionCaptureModeSelected && request.Mode != service.SessionCaptureModeDisabled {
		response.BadRequest(c, "Invalid Session capture mode")
		return
	}
	if err := h.service.UpdateMode(c.Request.Context(), request.Mode, adminActorUserID(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"mode": request.Mode})
}

func (h *SessionDeliveryHandler) UpdateAPIKey(c *gin.Context) {
	apiKeyID, ok := parseSessionCaptureAPIKeyID(c)
	if !ok {
		return
	}
	var request struct {
		Policy service.SessionCaptureKeyPolicy `json:"policy" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid Session capture API key policy")
		return
	}
	if request.Policy != service.SessionCaptureKeyPolicyInherit && request.Policy != service.SessionCaptureKeyPolicyInclude && request.Policy != service.SessionCaptureKeyPolicyExclude {
		response.BadRequest(c, "Invalid Session capture API key policy")
		return
	}
	if err := h.service.UpdateAPIKey(c.Request.Context(), apiKeyID, request.Policy, adminActorUserID(c)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			response.NotFound(c, "API key not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"api_key_id": apiKeyID, "policy": request.Policy})
}

func (h *SessionDeliveryHandler) SetOnlyAPIKey(c *gin.Context) {
	apiKeyID, ok := parseSessionCaptureAPIKeyID(c)
	if !ok {
		return
	}
	if err := h.service.SetOnlyAPIKey(c.Request.Context(), apiKeyID, adminActorUserID(c)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			response.NotFound(c, "API key not found")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"api_key_id": apiKeyID,
		"mode":       service.SessionCaptureModeSelected,
		"policy":     service.SessionCaptureKeyPolicyInclude,
	})
}

func parseSessionCaptureAPIKeyID(c *gin.Context) (int64, bool) {
	apiKeyID, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || apiKeyID <= 0 {
		response.BadRequest(c, "Invalid API key id")
		return 0, false
	}
	return apiKeyID, true
}

func adminActorUserID(c *gin.Context) int64 {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		return 0
	}
	return subject.UserID
}
