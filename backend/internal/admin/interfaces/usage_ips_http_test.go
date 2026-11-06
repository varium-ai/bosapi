package interfaces

import (
	userdomain "bosapi/backend/internal/user/domain"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

type ipSnapshotRepository struct {
	domain.AdminUsageRepository
	filter domain.AdminUsageFilter
	calls  int
}

func (r *ipSnapshotRepository) GroupAdminUsageByIPSnapshot(_ context.Context, f domain.AdminUsageFilter, _, _ int) (domain.AdminUsageIPSnapshot, error) {
	r.calls++
	r.filter = f
	return domain.AdminUsageIPSnapshot{Items: []domain.AdminUsageIP{}, Snapshot: &domain.UsageSnapshotMetadata{Status: "generating"}}, nil
}
func TestAdminIPHandlerUsesSnapshotAndTenantScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &ipSnapshotRepository{}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("identity", httpapi.Claims{Role: "org_admin", OrganizationID: 7}) })
	NewAdminUsageHandler(application.NewAdminUsageService(r)).RegisterRoutes(router.Group(""))
	request := func(q string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/usage/ips?"+q, nil))
		return w
	}
	w := request("key_id=149&page=2&page_size=20&snapshot_id=version")
	var response struct{ Data domain.AdminUsageIPSnapshot }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response.Data.Snapshot == nil || response.Data.Snapshot.Status != "generating" || r.filter.OrganizationID != 7 || r.filter.APIKeyID != 149 || r.filter.SnapshotID != "version" || r.filter.From != nil {
		t.Fatalf("response=%s filter=%+v", w.Body, r.filter)
	}
	for _, q := range []string{"key_id=149&organization_id=8", "page_size=20", "key_id=149&page_size=101", "key_id=149&from=2026-11-02&to=2026-10-01"} {
		if w := request(q); w.Code != 400 || r.calls != 1 {
			t.Fatalf("q=%s response=%s calls=%d", q, w.Body, r.calls)
		}
	}
}

func TestIPWindowPresetsHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, portal := range []bool{false, true} {
		repo := &ipSnapshotRepository{}
		router := gin.New()
		if portal {
			handler := NewKeyPortalHandler(nil, nil, nil, nil, nil)
			handler.WithIPUsageService(application.NewAdminUsageService(repo))
			router.Use(func(c *gin.Context) { c.Set("key_portal_key", userdomain.APIKey{ID: 149, OrganizationID: 7}) })
			router.GET("/ips", handler.listIPs)
		} else {
			router.Use(func(c *gin.Context) { c.Set("identity", httpapi.Claims{Role: "org_admin", OrganizationID: 7}) })
			router.GET("/ips", NewAdminUsageHandler(application.NewAdminUsageService(repo)).byIP)
		}
		for _, minutes := range []string{"", "30", "60", "360", "720", "1440", "0", "-1", "31", "1441", "2880", "abc"} {
			q := "/ips?key_id=149&snapshot_id=version"
			if minutes != "" {
				q += "&window_minutes=" + minutes
			}
			before := repo.calls
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", q, nil))
			n, _ := strconv.Atoi(minutes)
			_, valid := domain.IPUsageWindow(n)
			valid = valid && (n > 0 || minutes == "")
			if !valid {
				if w.Code != 400 || repo.calls != before {
					t.Fatalf("portal=%v range=%s response=%s", portal, minutes, w.Body)
				}
				continue
			}
			if minutes == "" {
				n = 30
			}
			if w.Code != 200 || repo.filter.IPWindowMinutes != n || repo.filter.OrganizationID != 7 || repo.filter.APIKeyID != 149 || repo.filter.SnapshotID != "version" {
				t.Fatalf("portal=%v range=%s response=%s filter=%+v", portal, minutes, w.Body, repo.filter)
			}
		}
	}
}
