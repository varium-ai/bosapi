package interfaces

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

type requestLogRepositoryStub struct {
	filter domain.RequestLogFilter
	called bool
}

func (repository *requestLogRepositoryStub) AppendRequestLog(context.Context, domain.RequestLog) error {
	return nil
}

func (repository *requestLogRepositoryStub) ListRequestLogs(_ context.Context, filter domain.RequestLogFilter, _, _ int) ([]domain.RequestLog, bool, error) {
	repository.filter = filter
	repository.called = true
	return []domain.RequestLog{{RequestID: filter.RequestID, OrganizationID: filter.OrganizationID}}, false, nil
}

func TestRequestLogsEnforceAdminRoleAndOrganizationScope(t *testing.T) {
	for _, test := range []struct {
		name           string
		claims         httpapi.Claims
		query          string
		wantStatus     int
		wantOrgID      uint64
		wantRepository bool
	}{
		{name: "organization admin is scoped", claims: httpapi.Claims{Role: "org_admin", OrganizationID: 27}, query: "request_id=req_abc-123", wantStatus: http.StatusOK, wantOrgID: 27, wantRepository: true},
		{name: "super admin can search all organizations", claims: httpapi.Claims{Role: "super_admin"}, query: "request_id=req_abc-123", wantStatus: http.StatusOK, wantRepository: true},
		{name: "key portal role is denied", claims: httpapi.Claims{Role: "key_viewer", OrganizationID: 27}, query: "request_id=req_abc-123", wantStatus: http.StatusForbidden},
		{name: "invalid request ID is rejected", claims: httpapi.Claims{Role: "org_admin", OrganizationID: 27}, query: "request_id=bad%0Avalue", wantStatus: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			repository := &requestLogRepositoryStub{}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", test.claims)
				ctx.Next()
			})
			NewRequestLogHandler(application.NewRequestLogService(repository)).RegisterRoutes(router.Group(""))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/request-logs?"+test.query, nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if repository.called != test.wantRepository {
				t.Fatalf("repository called=%t, want %t", repository.called, test.wantRepository)
			}
			if test.wantRepository && repository.filter.OrganizationID != test.wantOrgID {
				t.Fatalf("organization filter=%d, want %d", repository.filter.OrganizationID, test.wantOrgID)
			}
		})
	}
}
