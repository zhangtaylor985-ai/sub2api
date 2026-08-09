package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	rootHandler "github.com/Wei-Shaw/sub2api/internal/handler"
	adminHandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBusinessRoutesStayBehindAdminMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &routeBusinessRepositoryStub{}
	handlers := &rootHandler.Handlers{Admin: &rootHandler.AdminHandlers{
		Business: adminHandler.NewBusinessHandler(service.NewBusinessService(repo)),
	}}
	router := gin.New()
	v1 := router.Group("/api/v1")
	admin := v1.Group("/admin")
	admin.Use(func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	})
	registerBusinessRoutes(admin, handlers)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/business/dashboard/current", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, repo.loadCalls, "unauthorized requests must not reach the business service")
}

type routeBusinessRepositoryStub struct {
	loadCalls int
}

func (r *routeBusinessRepositoryStub) LoadSources(context.Context, time.Time, time.Time) (*service.BusinessSourceBundle, error) {
	r.loadCalls++
	return &service.BusinessSourceBundle{}, nil
}

func (r *routeBusinessRepositoryStub) ListSnapshots(context.Context, time.Time) ([]service.BusinessHistoryPoint, error) {
	return nil, nil
}

func (r *routeBusinessRepositoryStub) GetSnapshot(context.Context, time.Time) (*service.BusinessReport, error) {
	return nil, service.ErrBusinessSnapshotNotFound
}

func (r *routeBusinessRepositoryStub) CloseSnapshot(_ context.Context, input service.BusinessSnapshotWrite) (*service.BusinessReport, bool, error) {
	return input.Report, true, nil
}
