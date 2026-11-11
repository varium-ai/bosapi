package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	authinterfaces "bosapi/backend/internal/auth/interfaces"
	"bosapi/backend/internal/platform/httpapi"
)

type userUsageHTTPRepository struct {
	domain.AdminUsageRepository
	filter  domain.AdminOrganizationUsageFilter
	calls   int
	failure error
}

func (repo *userUsageHTTPRepository) DailyAdminOrganizationUsage(_ context.Context, filter domain.AdminOrganizationUsageFilter, page, size int) (domain.AdminOrganizationUsageDashboard, error) {
	repo.calls++
	repo.filter = filter
	return domain.AdminOrganizationUsageDashboard{
		Items:            []domain.AdminOrganizationUsagePoint{{AdminUsagePoint: domain.AdminUsagePoint{Period: "2026-10-01", InputTokens: 11, OutputTokens: 7, TotalTokens: 18}, OrganizationID: 8, OrganizationName: "Engineering", KeyCount: 2}},
		Total:            5,
		Summary:          domain.AdminUsagePoint{TotalTokens: 180},
		TopOrganizations: []domain.AdminOrganizationTokenRank{{OrganizationID: 8, OrganizationName: "Engineering", InputTokens: 110, OutputTokens: 70, TotalTokens: 180}},
		TopKeys:          []domain.AdminKeyTokenRank{{APIKeyID: 42, APIKeyName: "Development", OrganizationName: "Engineering", OrganizationCount: 1, InputTokens: 100, OutputTokens: 60, TotalTokens: 160}, {APIKeyID: 43, APIKeyName: "Development", OrganizationName: "Engineering", OrganizationCount: 1, InputTokens: 10, OutputTokens: 10, TotalTokens: 20}},
		TopModels:        []domain.AdminModelTokenRank{{Model: "popular-model", InputTokens: 90, OutputTokens: 60, TotalTokens: 150}, {Model: "other-model", InputTokens: 20, OutputTokens: 10, TotalTokens: 30}},
	}, repo.failure
}

func TestDailyUserUsageHTTP(t *testing.T) {
	for _, test := range []struct {
		name, query, role string
		org               uint64
		status            int
		failure           error
	}{
		{name: "super admin", query: "from=2026-10-01&to=2026-10-01&page=2&page_size=1&search=alice", role: "super_admin", status: 200},
		{name: "tenant scoped", query: "from=2026-10-01&to=2026-10-01", role: "org_admin", org: 9, status: 200},
		{name: "model filtered", query: "from=2026-10-01&to=2026-10-01&model=popular-model", role: "org_admin", org: 9, status: 200},
		{name: "refresh stays tenant scoped", query: "from=2026-10-01&to=2026-10-01&refresh=true", role: "org_admin", org: 9, status: 200},
		{name: "cross tenant", query: "from=2026-10-01&to=2026-10-01&organization_id=10", role: "org_admin", org: 9, status: 400},
		{name: "tenant without organization", query: "from=2026-10-01&to=2026-10-01", role: "org_admin", status: 400},
		{name: "user denied", role: "user", status: 403},
		{name: "anonymous denied", status: 401},
		{name: "missing dates", role: "super_admin", status: 400},
		{name: "malformed date", query: "from=2026-02-30&to=2026-10-01", role: "super_admin", status: 400},
		{name: "reversed dates", query: "from=2026-10-02&to=2026-10-01", role: "super_admin", status: 400},
		{name: "oversized range", query: "from=2024-01-01&to=2026-10-01", role: "super_admin", status: 400},
		{name: "invalid pagination", query: "from=2026-10-01&to=2026-10-01&page=0", role: "super_admin", status: 400},
		{name: "oversized page", query: "from=2026-10-01&to=2026-10-01&page_size=101", role: "super_admin", status: 400},
		{name: "invalid key", query: "from=2026-10-01&to=2026-10-01&key_id=x", role: "super_admin", status: 400},
		{name: "injected date", query: "from=" + url.QueryEscape("2026-10-01'; DROP TABLE users; --") + "&to=2026-10-01", role: "super_admin", status: 400},
		{name: "long search", query: "from=2026-10-01&to=2026-10-01&search=" + strings.Repeat("s", 321), role: "super_admin", status: 400},
		{name: "database failure", query: "from=2026-10-01&to=2026-10-01", role: "super_admin", status: 500, failure: errors.New("PRIVATE_DATABASE_ERROR")},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			repo := &userUsageHTTPRepository{failure: test.failure}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				if test.role != "" {
					ctx.Set("identity", httpapi.Claims{UserID: 1, Role: test.role, OrganizationID: test.org})
				}
			})
			NewAdminUsageHandler(application.NewAdminUsageService(repo)).RegisterRoutes(router.Group("", authinterfaces.RequireRoles("super_admin", "org_admin")))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest("GET", "/usage/organizations/daily?"+test.query, nil))
			if recorder.Code != test.status || strings.Contains(recorder.Body.String(), "PRIVATE") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.status >= 400 && test.status < 500 && repo.calls != 0 {
				t.Fatal("rejected request reached repository")
			}
			if test.status == 200 {
				expectedStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
				if !repo.filter.From.Equal(expectedStart) || !repo.filter.To.Equal(expectedStart.Add(24*time.Hour)) || repo.filter.OrganizationID != test.org {
					t.Fatalf("filter=%+v", repo.filter)
				}
				query, _ := url.ParseQuery(test.query)
				if repo.filter.Refresh != (query.Get("refresh") == "true") {
					t.Fatalf("refresh filter=%v", repo.filter.Refresh)
				}
				if repo.filter.Model != query.Get("model") {
					t.Fatalf("model filter=%q", repo.filter.Model)
				}
				var result struct {
					Data domain.AdminOrganizationUsageDashboard
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Data.Total != 5 || result.Data.Summary.TotalTokens != 180 || result.Data.Items[0].KeyCount != 2 || result.Data.Items[0].InputTokens != 11 || len(result.Data.TopOrganizations) != 1 || result.Data.TopOrganizations[0].TotalTokens != 180 || len(result.Data.TopKeys) != 2 || result.Data.TopKeys[0].APIKeyID != 42 || result.Data.TopKeys[1].APIKeyID != 43 {
					t.Fatalf("result=%+v", result)
				}
				if len(result.Data.TopModels) != 2 || result.Data.TopModels[0].Model != "popular-model" || result.Data.TopModels[0].TotalTokens != 150 || result.Data.TopModels[1].TotalTokens != 30 {
					t.Fatalf("model ranking=%+v", result.Data.TopModels)
				}
				if result.Data.TopKeys[0].OrganizationName != "Engineering" || result.Data.TopKeys[0].OrganizationCount != 1 {
					t.Fatalf("key organization=%+v", result.Data.TopKeys[0])
				}
			}
		})
	}
}
