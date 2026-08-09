package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type BusinessHandler struct {
	service *service.BusinessService
}

func NewBusinessHandler(businessService *service.BusinessService) *BusinessHandler {
	return &BusinessHandler{service: businessService}
}

func (h *BusinessHandler) Current(c *gin.Context) {
	report, err := h.service.Current(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}

func (h *BusinessHandler) History(c *gin.Context) {
	points, err := h.service.History(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, points)
}

func (h *BusinessHandler) Month(c *gin.Context) {
	report, err := h.service.Month(c.Request.Context(), c.Param("month"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}

func (h *BusinessHandler) ListCosts(c *gin.Context) {
	items, err := h.service.ListCosts(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *BusinessHandler) CreateCost(c *gin.Context) {
	var request service.CreateBusinessCostInput
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.service.CreateCost(c.Request.Context(), request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, item)
}

func (h *BusinessHandler) UpdateCost(c *gin.Context) {
	id, ok := parseBusinessID(c, "id", "cost")
	if !ok {
		return
	}
	var request service.UpdateBusinessCostInput
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.service.UpdateCost(c.Request.Context(), id, request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *BusinessHandler) DeleteCost(c *gin.Context) {
	id, ok := parseBusinessID(c, "id", "cost")
	if !ok {
		return
	}
	if err := h.service.DeleteCost(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Business cost deleted successfully"})
}

func (h *BusinessHandler) ListPricingRules(c *gin.Context) {
	items, err := h.service.ListPricingRules(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *BusinessHandler) UpsertPricingRule(c *gin.Context) {
	var request service.UpsertBusinessPricingRuleInput
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.service.UpsertPricingRule(c.Request.Context(), request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *BusinessHandler) ListExchangeRates(c *gin.Context) {
	items, err := h.service.ListExchangeRates(c.Request.Context(), c.Param("month"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *BusinessHandler) UpsertExchangeRate(c *gin.Context) {
	var request service.UpsertBusinessExchangeRateInput
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.service.UpsertExchangeRate(c.Request.Context(), c.Param("month"), request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *BusinessHandler) RefreshExchangeRate(c *gin.Context) {
	result, err := h.service.RefreshCurrentExchangeRate(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *BusinessHandler) GetAPIKeyConfig(c *gin.Context) {
	id, ok := parseBusinessID(c, "api_key_id", "API key")
	if !ok {
		return
	}
	references, err := h.service.References(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for i := range references.APIKeys {
		if references.APIKeys[i].ID == id {
			response.Success(c, references.APIKeys[i])
			return
		}
	}
	response.NotFound(c, "API key not found")
}

func (h *BusinessHandler) UpsertAPIKeyConfig(c *gin.Context) {
	id, ok := parseBusinessID(c, "api_key_id", "API key")
	if !ok {
		return
	}
	var request service.UpsertBusinessAPIKeyConfigInput
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := h.service.UpsertAPIKeyConfig(c.Request.Context(), id, request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *BusinessHandler) References(c *gin.Context) {
	items, err := h.service.References(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *BusinessHandler) Reconciliation(c *gin.Context) {
	result, err := h.service.Reconciliation(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *BusinessHandler) InitializeDefaults(c *gin.Context) {
	result, err := h.service.InitializeDefaults(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

type closeBusinessMonthRequest struct {
	DataQuality string  `json:"data_quality" binding:"required"`
	Notes       *string `json:"notes"`
}

func (h *BusinessHandler) CloseMonth(c *gin.Context) {
	var request closeBusinessMonthRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	adminID := getAdminIDFromContext(c)
	var closedBy *int64
	if adminID > 0 {
		closedBy = &adminID
	}
	report, created, err := h.service.CloseMonth(c.Request.Context(), service.CloseBusinessMonthInput{
		Month:       strings.TrimSpace(c.Param("month")),
		DataQuality: request.DataQuality,
		Notes:       request.Notes,
		ClosedBy:    closedBy,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"created": created, "snapshot": report})
}

func parseBusinessID(c *gin.Context, param, label string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param(param)), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid "+label+" ID")
		return 0, false
	}
	return id, true
}
