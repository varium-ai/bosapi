package interfaces

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
			NewRateLimitHandler(service).RegisterRoutes(router.Group("/api/v1/admin"))
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
