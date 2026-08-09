package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBusinessHandlerValidatesManagementInputs(t *testing.T) {
	router, repo := setupBusinessHandlerTestRouter()

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPut, path: "/api/v1/admin/business/costs/not-an-id", body: `{}`},
		{method: http.MethodPost, path: "/api/v1/admin/business/costs", body: `{"name":"bad","cost_class":"direct","category":"server","amount_minor":-1,"currency":"CNY","billing_cycle":"monthly","starts_on":"2026-08-01","active":true}`},
		{method: http.MethodPut, path: "/api/v1/admin/business/exchange-rates/2026-13", body: `{"currency":"USD","rate_scaled":6750000,"source":"manual"}`},
		{method: http.MethodPut, path: "/api/v1/admin/business/exchange-rates/2026-08", body: `{"currency":"CNY","rate_scaled":6750000,"source":"manual"}`},
		{method: http.MethodPut, path: "/api/v1/admin/business/api-key-configs/0", body: `{"revenue_excluded":true}`},
		{method: http.MethodPost, path: "/api/v1/admin/business/snapshots/2026-07/close", body: `{}`},
		{method: http.MethodPost, path: "/api/v1/admin/business/snapshots/2026-07/close", body: `{"data_quality":"forecast"}`},
		{method: http.MethodPost, path: "/api/v1/admin/business/snapshots/2026-07/close", body: `{"data_quality":"estimated"}`},
	}

	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
		})
	}
	require.Zero(t, repo.createCostCalls)
	require.Zero(t, repo.closeCalls)
}

func TestBusinessHandlerReturnsCurrentReportAndCreatesValidCost(t *testing.T) {
	router, repo := setupBusinessHandlerTestRouter()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/business/dashboard/current", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "raw-api-key")
	require.NotContains(t, recorder.Body.String(), "oauth-token")

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/business/costs",
		bytes.NewBufferString(`{"name":"Server","cost_class":"operating","category":"server","amount_minor":10000,"currency":"CNY","billing_cycle":"monthly","starts_on":"2026-08-01","active":true}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, repo.createCostCalls)
}

func setupBusinessHandlerTestRouter() (*gin.Engine, *adminBusinessRepositoryStub) {
	gin.SetMode(gin.TestMode)
	repo := &adminBusinessRepositoryStub{}
	businessHandler := NewBusinessHandler(service.NewBusinessService(repo))
	router := gin.New()
	group := router.Group("/api/v1/admin/business")
	group.GET("/dashboard/current", businessHandler.Current)
	group.POST("/costs", businessHandler.CreateCost)
	group.PUT("/costs/:id", businessHandler.UpdateCost)
	group.PUT("/exchange-rates/:month", businessHandler.UpsertExchangeRate)
	group.PUT("/api-key-configs/:api_key_id", businessHandler.UpsertAPIKeyConfig)
	group.POST("/snapshots/:month/close", businessHandler.CloseMonth)
	return router, repo
}

type adminBusinessRepositoryStub struct {
	createCostCalls int
	closeCalls      int
}

func (r *adminBusinessRepositoryStub) LoadSources(context.Context, time.Time, time.Time) (*service.BusinessSourceBundle, error) {
	return &service.BusinessSourceBundle{}, nil
}

func (r *adminBusinessRepositoryStub) ListSnapshots(context.Context, time.Time) ([]service.BusinessHistoryPoint, error) {
	return nil, nil
}

func (r *adminBusinessRepositoryStub) GetSnapshot(context.Context, time.Time) (*service.BusinessReport, error) {
	return nil, service.ErrBusinessSnapshotNotFound
}

func (r *adminBusinessRepositoryStub) CloseSnapshot(_ context.Context, input service.BusinessSnapshotWrite) (*service.BusinessReport, bool, error) {
	r.closeCalls++
	return input.Report, true, nil
}

func (r *adminBusinessRepositoryStub) ListCosts(context.Context) ([]service.BusinessCostItem, error) {
	return nil, nil
}

func (r *adminBusinessRepositoryStub) CreateCost(_ context.Context, cost *service.BusinessCostItem) error {
	r.createCostCalls++
	cost.ID = int64(r.createCostCalls)
	return nil
}

func (r *adminBusinessRepositoryStub) UpdateCost(context.Context, *service.BusinessCostItem) error {
	return nil
}

func (r *adminBusinessRepositoryStub) DeleteCost(context.Context, int64) error { return nil }

func (r *adminBusinessRepositoryStub) ListPricingRules(context.Context) ([]service.BusinessPricingRule, error) {
	return nil, nil
}

func (r *adminBusinessRepositoryStub) UpsertPricingRule(context.Context, *service.BusinessPricingRule) error {
	return nil
}

func (r *adminBusinessRepositoryStub) ListExchangeRates(context.Context, time.Time) ([]service.BusinessExchangeRate, error) {
	return nil, nil
}

func (r *adminBusinessRepositoryStub) UpsertExchangeRate(context.Context, *service.BusinessExchangeRate) error {
	return nil
}

func (r *adminBusinessRepositoryStub) UpsertAPIKeyConfig(context.Context, *service.BusinessAPIKeyConfig) error {
	return nil
}

func (r *adminBusinessRepositoryStub) ListReferences(context.Context) (*service.BusinessReferenceData, error) {
	return &service.BusinessReferenceData{}, nil
}

func (r *adminBusinessRepositoryStub) AccountExists(context.Context, int64) (bool, error) {
	return false, nil
}

func (r *adminBusinessRepositoryStub) GroupExists(context.Context, int64) (bool, error) {
	return false, nil
}

func (r *adminBusinessRepositoryStub) APIKeyExists(context.Context, int64) (bool, error) {
	return false, nil
}

func (r *adminBusinessRepositoryStub) PrivateSubscriptionExists(context.Context, int64) (bool, error) {
	return false, nil
}

func (r *adminBusinessRepositoryStub) InitializeDefaults(context.Context, service.BusinessDefaultInitialization) (*service.BusinessInitializationResult, error) {
	return &service.BusinessInitializationResult{}, nil
}
