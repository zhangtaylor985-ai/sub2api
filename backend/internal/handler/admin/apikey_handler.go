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
	GroupID                     *int64                                     `json:"group_id"`                       // nil=不修改, 0=解绑, >0=绑定到目标分组
	ResetRateLimitUsage         *bool                                      `json:"reset_rate_limit_usage"`         // true=重置 5h/1d/7d 限速用量
	Status                      *string                                    `json:"status"`                         // nil=不修改, active/inactive
	Quota                       *float64                                   `json:"quota"`                          // nil=不修改, 0=不限制
	RateMultiplier              *float64                                   `json:"rate_multiplier"`                // nil=不修改, >0=计费倍率
	ExpiresAt                   *string                                    `json:"expires_at"`                     // nil=不修改, ""=清空, RFC3339=设置
	ResetQuota                  *bool                                      `json:"reset_quota"`                    // true=重置总额度已用量
	Concurrency                 *int                                       `json:"concurrency"`                    // nil=不修改, 0=继承分组/用户, >0=单 key 并发
	AllowClaudeFamily           *bool                                      `json:"allow_claude_family"`            // nil=不修改
	AllowGPTFamily              *bool                                      `json:"allow_gpt_family"`               // nil=不修改
	MessagesDispatchModelConfig *service.OpenAIMessagesDispatchModelConfig `json:"messages_dispatch_model_config"` // nil=不修改
	RateLimit5h                 *float64                                   `json:"rate_limit_5h"`                  // nil=不修改, 0=不限制
	RateLimit1d                 *float64                                   `json:"rate_limit_1d"`
	RateLimit7d                 *float64                                   `json:"rate_limit_7d"`
	Window7dStart               *string                                    `json:"window_7d_start"` // nil=不修改, ""=清空, RFC3339=设置当前 7d 窗口起点
}

type AdminCreateAPIKeyRequest struct {
	UserID                      *int64                                     `json:"user_id"`
	Name                        string                                     `json:"name" binding:"required"`
	CustomKey                   *string                                    `json:"custom_key"`
	GroupID                     *int64                                     `json:"group_id"`
	Status                      *string                                    `json:"status"`
	Quota                       float64                                    `json:"quota"`
	RateMultiplier              *float64                                   `json:"rate_multiplier"`
	ExpiresAt                   *string                                    `json:"expires_at"`
	RateLimit5h                 float64                                    `json:"rate_limit_5h"`
	RateLimit1d                 float64                                    `json:"rate_limit_1d"`
	RateLimit7d                 float64                                    `json:"rate_limit_7d"`
	Concurrency                 int                                        `json:"concurrency"`
	AllowClaudeFamily           *bool                                      `json:"allow_claude_family"`
	AllowGPTFamily              *bool                                      `json:"allow_gpt_family"`
	MessagesDispatchModelConfig *service.OpenAIMessagesDispatchModelConfig `json:"messages_dispatch_model_config"`
}

type AdminAddAPIKeyTokenPackageRequest struct {
	AmountUSD float64 `json:"amount_usd" binding:"required"`
	Note      string  `json:"note"`
}

// List handles listing API keys across all users.
// GET /api/v1/admin/api-keys
func (h *AdminAPIKeyHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	filters := service.AdminAPIKeyListFilters{
		Search: c.Query("search"),
		Status: c.Query("status"),
	}
	if raw := c.Query("group_id"); raw != "" {
		groupID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		filters.GroupID = &groupID
	}
	if raw := c.Query("user_id"); raw != "" {
		userID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		filters.UserID = &userID
	}

	keys, total, err := h.adminService.AdminListAPIKeys(c.Request.Context(), page, pageSize, filters, c.Query("sort_by"), c.Query("sort_order"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.AdminAPIKey, 0, len(keys))
	for i := range keys {
		out = append(out, *dto.APIKeyFromServiceAdmin(&keys[i]))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// Create handles admin-created API keys.
// POST /api/v1/admin/api-keys
func (h *AdminAPIKeyHandler) Create(c *gin.Context) {
	var req AdminCreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Quota < 0 || req.RateLimit5h < 0 || req.RateLimit1d < 0 || req.RateLimit7d < 0 {
		response.BadRequest(c, "quota and rate limits must be non-negative")
		return
	}
	if req.RateMultiplier != nil && *req.RateMultiplier <= 0 {
		response.BadRequest(c, "rate_multiplier must be greater than 0")
		return
	}
	if req.Concurrency < 0 {
		response.BadRequest(c, "concurrency must be non-negative")
		return
	}
	rateMultiplier := 0.0
	if req.RateMultiplier != nil {
		rateMultiplier = *req.RateMultiplier
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			response.BadRequest(c, "Invalid expires_at")
			return
		}
		expiresAt = &parsed
	}
	result, err := h.adminService.AdminCreateAPIKey(c.Request.Context(), service.AdminCreateAPIKeyInput{
		UserID:                      req.UserID,
		Name:                        req.Name,
		CustomKey:                   req.CustomKey,
		GroupID:                     req.GroupID,
		Status:                      req.Status,
		Quota:                       req.Quota,
		RateMultiplier:              rateMultiplier,
		ExpiresAt:                   expiresAt,
		RateLimit5h:                 req.RateLimit5h,
		RateLimit1d:                 req.RateLimit1d,
		RateLimit7d:                 req.RateLimit7d,
		Concurrency:                 req.Concurrency,
		AllowClaudeFamily:           req.AllowClaudeFamily,
		AllowGPTFamily:              req.AllowGPTFamily,
		MessagesDispatchModelConfig: req.MessagesDispatchModelConfig,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, adminAPIKeyResultResponse(result))
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

	response.Success(c, adminAPIKeyResultResponse(result))
}

func adminAPIKeyResultResponse(result *service.AdminUpdateAPIKeyGroupIDResult) any {
	if result == nil {
		return gin.H{"api_key": nil}
	}
	return struct {
		APIKey                 *dto.AdminAPIKey `json:"api_key"`
		AutoGrantedGroupAccess bool             `json:"auto_granted_group_access"`
		GrantedGroupID         *int64           `json:"granted_group_id,omitempty"`
		GrantedGroupName       string           `json:"granted_group_name,omitempty"`
	}{
		APIKey:                 dto.APIKeyFromServiceAdmin(result.APIKey),
		AutoGrantedGroupAccess: result.AutoGrantedGroupAccess,
		GrantedGroupID:         result.GrantedGroupID,
		GrantedGroupName:       result.GrantedGroupName,
	}
}

// AddTokenPackage handles API key token package top-ups.
// POST /api/v1/admin/api-keys/:id/token-packages
func (h *AdminAPIKeyHandler) AddTokenPackage(c *gin.Context) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}
	var req AdminAddAPIKeyTokenPackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pkg, err := h.adminService.AdminAddAPIKeyTokenPackage(c.Request.Context(), keyID, req.AmountUSD, req.Note, "admin")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, apiKeyTokenPackageResponse(pkg))
}

// ListTokenPackages returns token package top-ups and usage ledger.
// GET /api/v1/admin/api-keys/:id/token-packages
func (h *AdminAPIKeyHandler) ListTokenPackages(c *gin.Context) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}
	summary, err := h.adminService.AdminListAPIKeyTokenPackages(c.Request.Context(), keyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	packages := make([]any, 0, len(summary.Packages))
	for i := range summary.Packages {
		packages = append(packages, apiKeyTokenPackageResponse(&summary.Packages[i]))
	}
	usages := make([]any, 0, len(summary.Usages))
	for i := range summary.Usages {
		usages = append(usages, apiKeyTokenPackageUsageResponse(&summary.Usages[i]))
	}
	response.Success(c, gin.H{
		"packages":      packages,
		"usages":        usages,
		"remaining_usd": summary.Remaining,
	})
}

func apiKeyTokenPackageResponse(pkg *service.APIKeyTokenPackage) any {
	if pkg == nil {
		return nil
	}
	return gin.H{
		"id":            pkg.ID,
		"api_key_id":    pkg.APIKeyID,
		"amount_usd":    pkg.AmountUSD,
		"used_usd":      pkg.UsedUSD,
		"remaining_usd": pkg.RemainingUSD(),
		"note":          pkg.Note,
		"created_by":    pkg.CreatedBy,
		"started_at":    pkg.StartedAt,
		"created_at":    pkg.CreatedAt,
		"updated_at":    pkg.UpdatedAt,
	}
}

func apiKeyTokenPackageUsageResponse(usage *service.APIKeyTokenPackageUsage) any {
	if usage == nil {
		return nil
	}
	return gin.H{
		"id":                    usage.ID,
		"package_id":            usage.PackageID,
		"api_key_id":            usage.APIKeyID,
		"request_id":            usage.RequestID,
		"request_fingerprint":   usage.RequestFingerprint,
		"model":                 usage.Model,
		"cost_usd":              usage.CostUSD,
		"input_tokens":          usage.InputTokens,
		"output_tokens":         usage.OutputTokens,
		"cache_creation_tokens": usage.CacheCreationTokens,
		"cache_read_tokens":     usage.CacheReadTokens,
		"total_tokens":          usage.TotalTokens,
		"requested_at":          usage.RequestedAt,
		"created_at":            usage.CreatedAt,
	}
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
		"quota":           req.Quota,
		"rate_limit_5h":   req.RateLimit5h,
		"rate_limit_1d":   req.RateLimit1d,
		"rate_limit_7d":   req.RateLimit7d,
		"rate_multiplier": req.RateMultiplier,
	} {
		if value != nil && *value < 0 {
			return service.AdminUpdateAPIKeyPolicyInput{}, false, fmt.Errorf("%s must be non-negative", name)
		}
	}
	if req.RateMultiplier != nil && *req.RateMultiplier <= 0 {
		return service.AdminUpdateAPIKeyPolicyInput{}, false, fmt.Errorf("rate_multiplier must be greater than 0")
	}
	if req.Concurrency != nil && *req.Concurrency < 0 {
		return service.AdminUpdateAPIKeyPolicyInput{}, false, fmt.Errorf("concurrency must be non-negative")
	}

	input := service.AdminUpdateAPIKeyPolicyInput{
		Status:                      req.Status,
		Quota:                       req.Quota,
		RateMultiplier:              req.RateMultiplier,
		Concurrency:                 req.Concurrency,
		AllowClaudeFamily:           req.AllowClaudeFamily,
		AllowGPTFamily:              req.AllowGPTFamily,
		MessagesDispatchModelConfig: req.MessagesDispatchModelConfig,
		RateLimit5h:                 req.RateLimit5h,
		RateLimit1d:                 req.RateLimit1d,
		RateLimit7d:                 req.RateLimit7d,
		ResetQuota:                  req.ResetQuota != nil && *req.ResetQuota,
		ResetRateLimitUsage:         req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage,
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
	if req.Window7dStart != nil {
		if *req.Window7dStart == "" {
			input.ClearWindow7dStart = true
		} else {
			window7dStart, err := time.Parse(time.RFC3339, *req.Window7dStart)
			if err != nil {
				return service.AdminUpdateAPIKeyPolicyInput{}, false, err
			}
			input.Window7dStart = &window7dStart
		}
	}

	set := req.Status != nil ||
		req.Quota != nil ||
		req.RateMultiplier != nil ||
		req.ExpiresAt != nil ||
		req.ResetQuota != nil ||
		req.Concurrency != nil ||
		req.AllowClaudeFamily != nil ||
		req.AllowGPTFamily != nil ||
		req.MessagesDispatchModelConfig != nil ||
		req.RateLimit5h != nil ||
		req.RateLimit1d != nil ||
		req.RateLimit7d != nil ||
		req.Window7dStart != nil ||
		req.ResetRateLimitUsage != nil
	return input, set, nil
}
