package interfaces

import (
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/usagetime"
	userinfra "bosapi/backend/internal/user/infrastructure"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/application"
	"bosapi/backend/internal/admin/infrastructure"
	"bosapi/backend/internal/platform/httpapi"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestRateLimitReadOrganizationKeyScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		role       string
		query      string
		keyCount   int64
		queryError error
		wantStatus int
		wantBody   string
	}{
		{"matching key without configured limits", "super_admin", "organization_id=2&key_id=2", 1, nil, http.StatusOK, `"key_id":2`},
		{"key belongs to another organization", "super_admin", "organization_id=2&key_id=2", 0, nil, http.StatusNotFound, `"code":"not_found"`},
		{"missing or deleted key", "super_admin", "organization_id=2&key_id=999", 0, nil, http.StatusNotFound, `"code":"not_found"`},
		{"organization admin implicitly scoped", "org_admin", "key_id=2", 1, nil, http.StatusOK, `"organization_id":2`},
		{"organization admin cannot read foreign key", "org_admin", "key_id=2", 0, nil, http.StatusNotFound, `"code":"not_found"`},
		{"database failure remains internal error", "super_admin", "organization_id=2&key_id=2", 0, errors.New("database unavailable"), http.StatusInternalServerError, `"code":"internal_error"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			sqlDatabase, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDatabase.Close()
			database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"rate_limit.global", "rate_limit.organization.2"} {
				mock.ExpectQuery("SELECT .* FROM `settings` WHERE setting_key = \\? LIMIT \\?").
					WithArgs(key, 1).WillReturnRows(sqlmock.NewRows([]string{"setting_key", "value_text"}))
			}
			keyID := uint64(2)
			if strings.Contains(test.query, "key_id=999") {
				keyID = 999
			}
			keyQuery := mock.ExpectQuery("SELECT count\\(\\*\\) FROM `api_keys` WHERE id = \\? AND organization_id = \\? AND status <> \\?").
				WithArgs(keyID, uint64(2), "deleted")
			if test.queryError != nil {
				keyQuery.WillReturnError(test.queryError)
			} else {
				keyQuery.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(test.keyCount))
			}
			if test.keyCount > 0 && test.queryError == nil {
				mock.ExpectQuery("SELECT .* FROM `settings` WHERE setting_key = \\? LIMIT \\?").
					WithArgs("rate_limit.key.2", 1).WillReturnRows(sqlmock.NewRows([]string{"setting_key", "value_text"}))
			}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set("identity", httpapi.Claims{UserID: 3, OrganizationID: 2, Role: test.role})
			})
			service := application.NewRateLimitService(infrastructure.NewRateLimitRepository(database))
			NewRateLimitHandler(service).WithOrganizationUsage(func(context.Context, uint64, time.Time) (userinfra.OrganizationPeriodicUsage, error) {
				return userinfra.OrganizationPeriodicUsage{OrganizationID: 2, DailyTokens: 42}, nil
			}).RegisterRoutes(router.Group("/api/v1/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/rate-limits?"+test.query, nil))
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("status=%d body=%s, want status=%d containing %s", response.Code, response.Body.String(), test.wantStatus, test.wantBody)
			}
			if strings.Contains(response.Body.String(), "database unavailable") {
				t.Fatal("response leaked database error")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type httpRateLimitRepository struct {
	reads, writes                    int
	organizationID, keyID, updatedBy uint64
	update                           domain.RateLimitUpdate
	err                              error
}

func (r *httpRateLimitRepository) ReadRateLimits(_ context.Context, org, key uint64) (domain.RateLimitUpdate, error) {
	r.reads++
	r.organizationID = org
	r.keyID = key
	return r.update, r.err
}
func (r *httpRateLimitRepository) WriteRateLimits(_ context.Context, update domain.RateLimitUpdate, by uint64) error {
	r.writes++
	r.update = update
	r.updatedBy = by
	return r.err
}
func quotaHTTPRouter(handler *RateLimitHandler, claims *httpapi.Claims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if claims != nil {
		router.Use(func(c *gin.Context) { c.Set("identity", *claims) })
	}
	handler.RegisterRoutes(router.Group("/admin"))
	return router
}
func TestRateLimitAuthorizationBeforeParsing(t *testing.T) {
	for _, test := range []struct {
		name, method, query, body, role string
		org                             uint64
		status                          int
	}{
		{"anonymous malformed query", "GET", "?organization_id=no&key_id=no", "", "", 0, 401},
		{"anonymous malformed body", "PUT", "", "{", "", 0, 401},
		{"anonymous read", "GET", "", "", "", 0, 401},
		{"anonymous write", "PUT", "", "{}", "", 0, 401},
		{"member malformed query", "GET", "?organization_id=no", "", "member", 2, 403},
		{"member read", "GET", "?organization_id=2", "", "member", 2, 403},
		{"org admin without tenant", "GET", "", "", "org_admin", 0, 403},
		{"foreign organization", "GET", "?organization_id=3", "", "org_admin", 2, 404},
		{"org admin malformed write", "PUT", "", "{", "org_admin", 2, 403},
		{"org admin qps write", "PUT", "", `{"organizations":[{"organization_id":2,"qps":10}]}`, "org_admin", 2, 403},
		{"org admin quota write", "PUT", "", `{"organizations":[{"organization_id":2,"daily_quota_tokens":0}]}`, "org_admin", 2, 403},
		{"org admin key write", "PUT", "", `{"keys":[{"key_id":2,"qps":10}]}`, "org_admin", 2, 403},
		{"org admin global write", "PUT", "", `{"global":{"qps":10}}`, "org_admin", 2, 403},
		{"super invalid organization", "GET", "?organization_id=no", "", "super_admin", 0, 400},
		{"super invalid key", "GET", "?key_id=no", "", "super_admin", 0, 400},
		{"super invalid body", "PUT", "", "{", "super_admin", 0, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &httpRateLimitRepository{}
			handler := NewRateLimitHandler(application.NewRateLimitService(repository))
			var claims *httpapi.Claims
			if test.role != "" {
				claims = &httpapi.Claims{UserID: 7, Role: test.role, OrganizationID: test.org}
			}
			response := httptest.NewRecorder()
			quotaHTTPRouter(handler, claims).ServeHTTP(response, httptest.NewRequest(test.method, "/admin/rate-limits"+test.query, strings.NewReader(test.body)))
			if response.Code != test.status || repository.reads != 0 || repository.writes != 0 {
				t.Fatalf("status=%d body=%s reads=%d writes=%d", response.Code, response.Body, repository.reads, repository.writes)
			}
		})
	}
}
func TestRateLimitOrganizationUsageAndResets(t *testing.T) {
	for _, role := range []string{"super_admin", "org_admin"} {
		for _, key := range []string{"", "&key_id=9"} {
			t.Run(role+key, func(t *testing.T) {
				zero := int64(0)
				amount := 12.5
				repository := &httpRateLimitRepository{update: domain.RateLimitUpdate{Organizations: []domain.OrganizationRateLimit{{OrganizationID: 2, QPS: 10, DailyQuotaTokens: &zero, MonthlyQuotaAmount: &amount}}}}
				usage := userinfra.OrganizationPeriodicUsage{OrganizationID: 2, DailyTokens: 12, WeeklyTokens: 34, MonthlyTokens: 56, DailyAmount: 0.1, WeeklyAmount: 0.2, MonthlyAmount: 0.3}
				calls := 0
				var calledAt time.Time
				type contextKey struct{}
				requestContext := context.WithValue(context.Background(), contextKey{}, "request")
				handler := NewRateLimitHandler(application.NewRateLimitService(repository)).WithOrganizationUsage(func(ctx context.Context, id uint64, now time.Time) (userinfra.OrganizationPeriodicUsage, error) {
					calls++
					calledAt = now
					if id != 2 || ctx.Value(contextKey{}) != "request" {
						t.Fatalf("wrong usage scope/context: id=%d", id)
					}
					return usage, nil
				})
				response := httptest.NewRecorder()
				quotaHTTPRouter(handler, &httpapi.Claims{UserID: 7, Role: role, OrganizationID: 2}).ServeHTTP(response, httptest.NewRequest("GET", "/admin/rate-limits?organization_id=2"+key, nil).WithContext(requestContext))
				if response.Code != 200 || calls != 1 {
					t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				var body struct {
					Data struct {
						Organization struct {
							QPS    int64
							Quotas map[string]*float64
							Usage  userinfra.OrganizationPeriodicUsage
							Resets map[string]time.Time
						}
					}
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				org := body.Data.Organization
				if !reflect.DeepEqual(org.Usage, usage) || org.QPS != 10 || org.Quotas["daily_tokens"] == nil || *org.Quotas["daily_tokens"] != 0 || org.Quotas["weekly_tokens"] != nil || org.Quotas["monthly_amount"] == nil || *org.Quotas["monthly_amount"] != 12.5 {
					t.Fatalf("organization=%+v", org)
				}
				day, week, month := usagetime.PeriodStarts(calledAt)
				for period, want := range map[string]time.Time{"daily": day.AddDate(0, 0, 1), "weekly": week.AddDate(0, 0, 7), "monthly": month.In(usagetime.Location()).AddDate(0, 1, 0).UTC()} {
					if !org.Resets[period].Equal(want) {
						t.Fatalf("%s reset=%s want=%s", period, org.Resets[period], want)
					}
				}
			})
		}
	}
}
func TestRateLimitUsageFailuresAreNotZeroUsage(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			repository := &httpRateLimitRepository{update: domain.RateLimitUpdate{Organizations: []domain.OrganizationRateLimit{{OrganizationID: 2}}}}
			handler := NewRateLimitHandler(application.NewRateLimitService(repository))
			if configured {
				handler.WithOrganizationUsage(func(context.Context, uint64, time.Time) (userinfra.OrganizationPeriodicUsage, error) {
					return userinfra.OrganizationPeriodicUsage{}, errors.New("private ledger failure")
				})
			}
			response := httptest.NewRecorder()
			quotaHTTPRouter(handler, &httpapi.Claims{Role: "super_admin"}).ServeHTTP(response, httptest.NewRequest("GET", "/admin/rate-limits?organization_id=2", nil))
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"quota_accounting_unavailable"`) || strings.Contains(response.Body.String(), "private ledger") || strings.Contains(response.Body.String(), `"usage"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}
func TestRateLimitWritesAndInvalidation(t *testing.T) {
	body := `{"organizations":[{"organization_id":2,"qps":10,"daily_quota_tokens":0,"weekly_quota_tokens":20,"monthly_quota_tokens":30,"daily_quota_amount":0,"weekly_quota_amount":2.5,"monthly_quota_amount":3.5}]}`
	for _, test := range []struct {
		name, body    string
		err           error
		status, calls int
	}{
		{"valid", body, nil, 200, 1},
		{"clear", `{"organizations":[{"organization_id":2,"qps":0,"daily_quota_tokens":null,"weekly_quota_tokens":null,"monthly_quota_tokens":null,"daily_quota_amount":null,"weekly_quota_amount":null,"monthly_quota_amount":null}]}`, nil, 200, 1},
		{"invalid", `{"organizations":[{"organization_id":2,"daily_quota_tokens":-1}]}`, nil, 400, 0},
		{"missing", body, gorm.ErrRecordNotFound, 404, 0},
		{"failed", body, errors.New("private storage failure"), 500, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &httpRateLimitRepository{err: test.err}
			calls := 0
			response := httptest.NewRecorder()
			handler := NewRateLimitHandler(application.NewRateLimitService(repository)).WithQuotaCacheInvalidator(func(_ context.Context, update domain.RateLimitUpdate) {
				calls++
				if response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" {
					t.Fatal("success response started before cache invalidation")
				}
				if repository.writes != 1 {
					t.Fatal("cache invalidated before persistence")
				}
				if !reflect.DeepEqual(update, repository.update) {
					t.Fatal("invalidated wrong update")
				}
			})
			quotaHTTPRouter(handler, &httpapi.Claims{UserID: 7, Role: "super_admin"}).ServeHTTP(response, httptest.NewRequest("PUT", "/admin/rate-limits", strings.NewReader(test.body)))
			if response.Code != test.status || calls != test.calls || strings.Contains(response.Body.String(), "private storage") {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			if test.status == 200 {
				var want domain.RateLimitUpdate
				if err := json.Unmarshal([]byte(test.body), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(want, repository.update) || repository.updatedBy != 7 {
					t.Fatalf("persisted=%+v by=%d", repository.update, repository.updatedBy)
				}
			}
		})
	}
}
