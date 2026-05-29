package admin

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminAPIKeyHandler handles admin API key management
type AdminAPIKeyHandler struct {
	adminService service.AdminService
}

// NewAdminAPIKeyHandler creates a new admin API key handler
func NewAdminAPIKeyHandler(adminService service.AdminService) *AdminAPIKeyHandler {
	return &AdminAPIKeyHandler{
		adminService: adminService,
	}
}

// AdminUpdateAPIKeyGroupRequest represents the request to update an API key.
type AdminUpdateAPIKeyGroupRequest struct {
	GroupID             *int64   `json:"group_id"`               // nil=不修改, 0=解绑, >0=绑定到目标分组
	ResetRateLimitUsage *bool    `json:"reset_rate_limit_usage"` // true=重置 5h/1d/7d 限速用量
	Status              *string  `json:"status"`                 // nil=不修改, active/inactive
	Quota               *float64 `json:"quota"`                  // nil=不修改, 0=不限制
	ExpiresAt           *string  `json:"expires_at"`             // nil=不修改, ""=清空, RFC3339=设置
	ResetQuota          *bool    `json:"reset_quota"`            // true=重置总额度已用量
	RateLimit5h         *float64 `json:"rate_limit_5h"`          // nil=不修改, 0=不限制
	RateLimit1d         *float64 `json:"rate_limit_1d"`
	RateLimit7d         *float64 `json:"rate_limit_7d"`
}

// UpdateGroup handles updating an API key's admin-managed fields.
// PUT /api/v1/admin/api-keys/:id
func (h *AdminAPIKeyHandler) UpdateGroup(c *gin.Context) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	var req AdminUpdateAPIKeyGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	policyInput, policySet, parseErr := buildAdminAPIKeyPolicyInput(req)
	if parseErr != nil {
		response.BadRequest(c, parseErr.Error())
		return
	}

	result, err := h.adminService.AdminUpdateAPIKeyGroupID(c.Request.Context(), keyID, req.GroupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if policySet {
		key, err := h.adminService.AdminUpdateAPIKeyPolicy(c.Request.Context(), keyID, policyInput)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		result.APIKey = key
	}

	resp := struct {
		APIKey                 *dto.APIKey `json:"api_key"`
		AutoGrantedGroupAccess bool        `json:"auto_granted_group_access"`
		GrantedGroupID         *int64      `json:"granted_group_id,omitempty"`
		GrantedGroupName       string      `json:"granted_group_name,omitempty"`
	}{
		APIKey:                 dto.APIKeyFromService(result.APIKey),
		AutoGrantedGroupAccess: result.AutoGrantedGroupAccess,
		GrantedGroupID:         result.GrantedGroupID,
		GrantedGroupName:       result.GrantedGroupName,
	}
	response.Success(c, resp)
}

func buildAdminAPIKeyPolicyInput(req AdminUpdateAPIKeyGroupRequest) (service.AdminUpdateAPIKeyPolicyInput, bool, error) {
	if req.Status != nil {
		switch *req.Status {
		case service.StatusActive, "inactive", service.StatusAPIKeyDisabled:
		default:
			return service.AdminUpdateAPIKeyPolicyInput{}, false, fmt.Errorf("status must be active, inactive, or disabled")
		}
	}
	for name, value := range map[string]*float64{
		"quota":         req.Quota,
		"rate_limit_5h": req.RateLimit5h,
		"rate_limit_1d": req.RateLimit1d,
		"rate_limit_7d": req.RateLimit7d,
	} {
		if value != nil && *value < 0 {
			return service.AdminUpdateAPIKeyPolicyInput{}, false, fmt.Errorf("%s must be non-negative", name)
		}
	}

	input := service.AdminUpdateAPIKeyPolicyInput{
		Status:              req.Status,
		Quota:               req.Quota,
		RateLimit5h:         req.RateLimit5h,
		RateLimit1d:         req.RateLimit1d,
		RateLimit7d:         req.RateLimit7d,
		ResetQuota:          req.ResetQuota != nil && *req.ResetQuota,
		ResetRateLimitUsage: req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage,
	}

	if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			input.ClearExpires = true
		} else {
			expiresAt, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				return service.AdminUpdateAPIKeyPolicyInput{}, false, err
			}
			input.ExpiresAt = &expiresAt
		}
	}

	set := req.Status != nil ||
		req.Quota != nil ||
		req.ExpiresAt != nil ||
		req.ResetQuota != nil ||
		req.RateLimit5h != nil ||
		req.RateLimit1d != nil ||
		req.RateLimit7d != nil ||
		req.ResetRateLimitUsage != nil
	return input, set, nil
}
