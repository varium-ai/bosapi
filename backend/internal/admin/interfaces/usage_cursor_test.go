package interfaces

import (
	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
	"time"
)

type cursorRepository struct {
	domain.AdminUsageRepository
	filter domain.AdminUsageFilter
	calls  int
}

func (r *cursorRepository) ListAdminUsage(_ context.Context, f domain.AdminUsageFilter, _, _ int) ([]domain.AdminUsageRecord, int64, error) {
	r.filter = f
	r.calls++
	return []domain.AdminUsageRecord{{ID: 3, CreatedAt: *f.From}, {ID: 2, CreatedAt: *f.From}, {ID: 1, CreatedAt: *f.From}}, 3, nil
}
func TestUsageCursorAndDefaultDate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &cursorRepository{}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("identity", httpapi.Claims{Role: "org_admin", OrganizationID: 7}) })
	NewAdminUsageHandler(application.NewAdminUsageService(r)).RegisterRoutes(router.Group(""))
	request := func(q string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/usage?"+q, nil))
		return w
	}
	first := request("page_size=2")
	if first.Code != 200 || !r.filter.SkipTotal || r.filter.OrganizationID != 7 || r.filter.To.Sub(*r.filter.From) != 24*time.Hour {
		t.Fatalf("response=%s filter=%+v", first.Body, r.filter)
	}
	var response struct {
		Data struct {
			NextCursor string `json:"next_cursor"`
			HasMore    bool   `json:"has_more"`
			Items      []domain.AdminUsageRecord
			Total      *int64
		}
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Data.HasMore || len(response.Data.Items) != 2 || response.Data.Total != nil || response.Data.NextCursor == "" {
		t.Fatalf("response=%s", first.Body)
	}
	if w := request("page_size=2&cursor=" + response.Data.NextCursor); w.Code != 200 || r.filter.BeforeID != 2 {
		t.Fatalf("cursor=%s filter=%+v", w.Body, r.filter)
	}
	if w := request("page=1001&page_size=20&cursor=" + response.Data.NextCursor); w.Code != 200 || r.filter.BeforeID != 2 {
		t.Fatalf("deep cursor page rejected=%s", w.Body)
	}
	if w := request("page=1001&page_size=20"); w.Code != 400 {
		t.Fatalf("unbounded offset accepted=%s", w.Body)
	}
	calls := r.calls
	if w := request("page_size=2&model=changed&cursor=" + response.Data.NextCursor); w.Code != 400 || r.calls != calls {
		t.Fatalf("scope bypass=%s", w.Body)
	}
	if w := request("include_total=true&page_size=2"); w.Code != 200 || r.filter.SkipTotal {
		t.Fatalf("legacy=%s", w.Body)
	}
	if w := request("from=2026-01-01"); w.Code != 400 {
		t.Fatalf("unbounded=%s", w.Body)
	}
}
