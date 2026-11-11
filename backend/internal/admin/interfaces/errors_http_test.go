package interfaces

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

type errorWindowRepository struct {
	filters  []domain.AdminErrorFilter
	deadline time.Time
	err      error
}

func (repo *errorWindowRepository) ListAdminErrors(ctx context.Context, filter domain.AdminErrorFilter, limit int) ([]domain.AdminError, bool, error) {
	repo.filters = append(repo.filters, filter)
	repo.deadline, _ = ctx.Deadline()
	return []domain.AdminError{{ID: 7, Time: filter.To.Add(-time.Minute)}}, true, repo.err
}

func errorWindowRouter(repo *errorWindowRepository, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Set("identity", httpapi.Claims{Role: role, OrganizationID: 11})
	})
	NewAdminErrorHandler(application.NewAdminErrorService(repo)).RegisterRoutes(router.Group(""))
	return router
}

func TestAdminErrorsWindowDefaultsAndPresets(t *testing.T) {
	for _, minutes := range []int{0, 5, 15, 30, 60, 120, 360, 720, 1440} {
		t.Run(strconv.Itoa(minutes), func(t *testing.T) {
			repo := &errorWindowRepository{}
			router := errorWindowRouter(repo, "org_admin")
			query := "/errors?error_code=upstream_error&key_id=22&model=gpt-4o"
			if minutes != 0 {
				query += "&window_minutes=" + strconv.Itoa(minutes)
			}
			start := time.Now().UTC()
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", query, nil))
			if response.Code != 200 || len(repo.filters) != 1 {
				t.Fatalf("status=%d response=%s", response.Code, response.Body.String())
			}
			filter := repo.filters[0]
			if repo.deadline.IsZero() || repo.deadline.After(time.Now().Add(3*time.Second)) {
				t.Fatal("query must receive the server deadline")
			}
			want := minutes
			if want == 0 {
				want = 5
			}
			if filter.To.Sub(filter.From) != time.Duration(want)*time.Minute || filter.To.Before(start) || filter.To.After(time.Now()) || filter.OrganizationID != 11 || filter.APIKeyID != 22 || filter.ErrorCode != "upstream_error" || filter.Model != "gpt-4o" {
				t.Fatalf("unexpected filter: %+v", filter)
			}
		})
	}
}

func TestAdminErrorsRejectInvalidWindowBeforeQuery(t *testing.T) {
	for _, query := range []string{
		"window_minutes=", "window_minutes=0", "window_minutes=-5", "window_minutes=6",
		"window_minutes=1441", "window_minutes=2880", "window_minutes=abc", "window_minutes=5.5",
		"to=invalid", "to=0001-01-01T00:00:00Z", "cursor=invalid",
		"cursor=" + encodeErrorCursor(time.Now(), 1),
		"model=" + strings.Repeat("m", 129),
	} {
		t.Run(query, func(t *testing.T) {
			repo := &errorWindowRepository{}
			response := httptest.NewRecorder()
			errorWindowRouter(repo, "super_admin").ServeHTTP(response, httptest.NewRequest("GET", "/errors?"+query, nil))
			if response.Code != 400 || len(repo.filters) != 0 {
				t.Fatalf("status=%d queries=%d response=%s", response.Code, len(repo.filters), response.Body.String())
			}
		})
	}
}

func TestAdminErrorsCursorKeepsWindowAndRejectsChangedScope(t *testing.T) {
	repo := &errorWindowRepository{}
	router := errorWindowRouter(repo, "super_admin")
	params := url.Values{"window_minutes": {"120"}, "organization_id": {"11"}, "key_id": {"22"}, "error_code": {"upstream_error"}, "model": {"gpt-4o"}, "to": {"2026-11-03T12:00:00+08:00"}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/errors?"+params.Encode(), nil))
	var body struct {
		Data struct {
			Next string    `json:"next_cursor"`
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.Next == "" {
		t.Fatalf("unexpected first page: %s", response.Body.String())
	}
	params.Set("cursor", body.Data.Next)
	params.Del("to") // Existing callers can rely on the cursor to retain the end time.
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/errors?"+params.Encode(), nil))
	if response.Code != 200 || len(repo.filters) != 2 {
		t.Fatalf("unexpected next page: %s", response.Body.String())
	}
	first, next := repo.filters[0], repo.filters[1]
	if !next.From.Equal(first.From) || !next.To.Equal(first.To) || next.BeforeID != 7 || !next.Before.Equal(first.To.Add(-time.Minute)) || !body.Data.From.Equal(first.From) || !body.Data.To.Equal(first.To) {
		t.Fatalf("window changed: first=%+v next=%+v", first, next)
	}
	for key, value := range map[string]string{"window_minutes": "5", "organization_id": "12", "key_id": "23", "error_code": "invalid_request", "model": "gpt-4.1", "to": "2026-11-03T05:00:00Z"} {
		t.Run(key, func(t *testing.T) {
			changed, _ := url.ParseQuery(params.Encode())
			changed.Set(key, value)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", "/errors?"+changed.Encode(), nil))
			if response.Code != 400 || len(repo.filters) != 2 {
				t.Fatalf("changed filters accepted: %s", response.Body.String())
			}
		})
	}
}

func TestAdminErrorsRejectForgedOversizedCursorWindow(t *testing.T) {
	end := time.Now().UTC()
	filter := domain.AdminErrorFilter{From: end.Add(-48 * time.Hour), To: end}
	cursor := encodeAdminErrorCursor(filter, end.Add(-time.Minute), 7)
	repo := &errorWindowRepository{}
	response := httptest.NewRecorder()
	errorWindowRouter(repo, "super_admin").ServeHTTP(response, httptest.NewRequest("GET", "/errors?window_minutes=1440&cursor="+cursor, nil))
	if response.Code != 400 || len(repo.filters) != 0 {
		t.Fatalf("oversized window accepted: %s", response.Body.String())
	}
}

func TestAdminErrorsTimeoutDoesNotReturnEmptySuccess(t *testing.T) {
	repo := &errorWindowRepository{err: context.DeadlineExceeded}
	response := httptest.NewRecorder()
	errorWindowRouter(repo, "super_admin").ServeHTTP(response, httptest.NewRequest("GET", "/errors", nil))
	if response.Code != 504 {
		t.Fatalf("status=%d response=%s", response.Code, response.Body.String())
	}
}
