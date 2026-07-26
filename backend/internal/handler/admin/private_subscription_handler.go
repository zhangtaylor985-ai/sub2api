package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type PrivateSubscriptionHandler struct {
	service *service.PrivateSubscriptionService
}

func NewPrivateSubscriptionHandler(
	privateSubscriptionService *service.PrivateSubscriptionService,
) *PrivateSubscriptionHandler {
	return &PrivateSubscriptionHandler{service: privateSubscriptionService}
}

type CreatePrivateSubscriptionRequest struct {
	Name             string `json:"name" binding:"required"`
	SubscriptionType string `json:"subscription_type" binding:"required"`
	AmountCents      int64  `json:"amount_cents"`
	ExpiresOn        string `json:"expires_on" binding:"required"`
}

type UpdatePrivateSubscriptionRequest struct {
	Name             *string `json:"name"`
	SubscriptionType *string `json:"subscription_type"`
	AmountCents      *int64  `json:"amount_cents"`
	ExpiresOn        *string `json:"expires_on"`
}

type PrivateSubscriptionResponse struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	SubscriptionType string     `json:"subscription_type"`
	AmountCents      int64      `json:"amount_cents"`
	ExpiresOn        string     `json:"expires_on"`
	Status           string     `json:"status"`
	DaysRemaining    int        `json:"days_remaining"`
	ReminderSent     bool       `json:"reminder_sent"`
	ReminderSentAt   *time.Time `json:"reminder_sent_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// List handles private customer subscription listing.
// GET /api/v1/admin/private-subscriptions
func (h *PrivateSubscriptionHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "expires_on"),
		SortOrder: c.DefaultQuery("sort_order", "asc"),
	}
	filters := service.PrivateSubscriptionListFilters{
		Search:           strings.TrimSpace(c.Query("search")),
		Status:           strings.TrimSpace(c.Query("status")),
		SubscriptionType: strings.TrimSpace(c.Query("subscription_type")),
	}

	items, pageResult, err := h.service.List(c.Request.Context(), params, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	today := timezone.Today()
	out := make([]PrivateSubscriptionResponse, 0, len(items))
	for i := range items {
		out = append(out, privateSubscriptionResponseFromService(&items[i], today))
	}
	response.Paginated(c, out, pageResult.Total, pageResult.Page, pageResult.PageSize)
}

// Summary handles aggregate counters for the private subscription dashboard.
// GET /api/v1/admin/private-subscriptions/summary
func (h *PrivateSubscriptionHandler) Summary(c *gin.Context) {
	summary, err := h.service.Summary(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, summary)
}

// GetByID handles one private customer subscription.
// GET /api/v1/admin/private-subscriptions/:id
func (h *PrivateSubscriptionHandler) GetByID(c *gin.Context) {
	id, ok := parsePrivateSubscriptionID(c)
	if !ok {
		return
	}

	item, err := h.service.GetByID(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, privateSubscriptionResponseFromService(item, timezone.Today()))
}

// Create handles creating a private customer subscription.
// POST /api/v1/admin/private-subscriptions
func (h *PrivateSubscriptionHandler) Create(c *gin.Context) {
	var request CreatePrivateSubscriptionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	item, err := h.service.Create(c.Request.Context(), &service.CreatePrivateSubscriptionInput{
		Name:             request.Name,
		SubscriptionType: request.SubscriptionType,
		AmountCents:      request.AmountCents,
		ExpiresOn:        request.ExpiresOn,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, privateSubscriptionResponseFromService(item, timezone.Today()))
}

// Update handles editing a private customer subscription.
// PUT /api/v1/admin/private-subscriptions/:id
func (h *PrivateSubscriptionHandler) Update(c *gin.Context) {
	id, ok := parsePrivateSubscriptionID(c)
	if !ok {
		return
	}

	var request UpdatePrivateSubscriptionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	item, err := h.service.Update(c.Request.Context(), id, &service.UpdatePrivateSubscriptionInput{
		Name:             request.Name,
		SubscriptionType: request.SubscriptionType,
		AmountCents:      request.AmountCents,
		ExpiresOn:        request.ExpiresOn,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, privateSubscriptionResponseFromService(item, timezone.Today()))
}

// Delete soft-deletes a private customer subscription.
// DELETE /api/v1/admin/private-subscriptions/:id
func (h *PrivateSubscriptionHandler) Delete(c *gin.Context) {
	id, ok := parsePrivateSubscriptionID(c)
	if !ok {
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Private subscription deleted successfully"})
}

func parsePrivateSubscriptionID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid private subscription ID")
		return 0, false
	}
	return id, true
}

func privateSubscriptionResponseFromService(
	item *service.PrivateSubscription,
	today time.Time,
) PrivateSubscriptionResponse {
	if item == nil {
		return PrivateSubscriptionResponse{}
	}
	return PrivateSubscriptionResponse{
		ID:               item.ID,
		Name:             item.Name,
		SubscriptionType: item.SubscriptionType,
		AmountCents:      item.AmountCents,
		ExpiresOn:        item.ExpiresOn.Format("2006-01-02"),
		Status:           item.StatusAt(today),
		DaysRemaining:    item.DaysRemainingAt(today),
		ReminderSent:     item.ReminderSentForCurrentExpiry(),
		ReminderSentAt:   item.ReminderSentAt,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
}
