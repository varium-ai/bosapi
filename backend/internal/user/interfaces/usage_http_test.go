package interfaces

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"bosapi/backend/internal/platform/httpapi"
	"bosapi/backend/internal/user/application"
	"bosapi/backend/internal/user/infrastructure"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Exercise the real handler, service and SQL repository together so the legacy
// user_id condition cannot silently break organization-owned Keys again.
func TestKeyUsageHTTPUsesOrganizationScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, batch := range []bool{false, true} {
		endpoint := "daily"
		keyCount := int64(1)
		if batch {
			endpoint, keyCount = "batch", 2
		}
		t.Run(endpoint, func(t *testing.T) {
			for _, test := range []struct {
				name           string
				userID         uint64
				organizationID uint64
				count          int64
				status         int
				failure        error
			}{
				{name: "same organization without user binding", userID: 3, organizationID: 2, count: keyCount, status: 200},
				{name: "another administrator in same organization", userID: 99, organizationID: 2, count: keyCount, status: 200},
				{name: "cross organization", userID: 3, organizationID: 9, status: 404},
				{name: "deleted key", userID: 3, organizationID: 2, status: 404},
				{name: "missing key", userID: 3, organizationID: 2, status: 404},
				{name: "missing organization", userID: 3, status: 404},
				{name: "unauthenticated", status: 401},
				{name: "ownership query failure", userID: 3, organizationID: 2, status: 500, failure: errors.New("PRIVATE_DATABASE_ERROR")},
			} {
				t.Run(test.name, func(t *testing.T) {
					database, mock, err := sqlmock.New()
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := mock.ExpectationsWereMet(); err != nil {
							t.Error(err)
						}
						mock.ExpectClose()
						if err := database.Close(); err != nil {
							t.Error(err)
						}
					})
					db, err := gorm.Open(mysql.New(mysql.Config{Conn: database, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
					if err != nil {
						t.Fatal(err)
					}
					if test.userID != 0 && test.organizationID != 0 {
						query := "SELECT count(*) FROM `api_keys` WHERE id = ? AND organization_id = ? AND status <> ?"
						args := []driver.Value{uint64(7), test.organizationID, "deleted"}
						if batch {
							query = "SELECT count(*) FROM `api_keys` WHERE id IN (?,?) AND organization_id = ? AND status <> ?"
							args = []driver.Value{uint64(7), uint64(8), test.organizationID, "deleted"}
						}
						expected := mock.ExpectQuery("^" + regexp.QuoteMeta(query) + "$").WithArgs(args...)
						if test.failure != nil {
							expected.WillReturnError(test.failure)
						} else {
							count := test.count
							if batch && test.name == "deleted key" {
								count = 1 // A batch containing one deleted Key must be rejected in full.
							}
							expected.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
						}
					}
					if test.status == http.StatusOK {
						if batch {
							mock.ExpectQuery("^SELECT .* FROM usage_logs_read AS usage_logs WHERE "+regexp.QuoteMeta("organization_id = ? AND organization_id > 0 AND api_key_id IN (?,?)")+" GROUP BY .* ORDER BY date ASC, api_key_id ASC LIMIT \\?$").
								WithArgs(test.organizationID, uint64(7), uint64(8), 10000).
								WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "date", "requests", "total_tokens", "cost_amount"}).AddRow(7, "2026-10-13", 1, 18, 0.01))
						} else {
							mock.ExpectQuery("^SELECT .* FROM usage_logs_read AS usage_logs WHERE "+regexp.QuoteMeta("(organization_id = ? AND organization_id > 0) AND api_key_id = ? AND created_at >= ?")+" GROUP BY .* ORDER BY date ASC$").
								WithArgs(test.organizationID, uint64(7), sqlmock.AnyArg()).
								WillReturnRows(sqlmock.NewRows([]string{"date", "requests", "total_tokens", "cost_amount"}).AddRow("2026-10-13", 1, 18, 0.01))
						}
					}

					router := gin.New()
					router.Use(func(ctx *gin.Context) {
						if test.userID != 0 {
							ctx.Set("identity", httpapi.Claims{UserID: test.userID, OrganizationID: test.organizationID, Role: "org_admin"})
						}
					})
					NewUsageHandler(application.NewUsageService(infrastructure.NewKeyRepository(db))).RegisterRoutes(router.Group("/api/v1"))
					request := httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/7/usage/daily?days=7", nil)
					if batch {
						request = httptest.NewRequest(http.MethodPost, "/api/v1/usage/dashboard/api-keys-usage", strings.NewReader(`{"api_key_ids":[7,8],"group_by":"day"}`))
						request.Header.Set("Content-Type", "application/json")
					}
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					if response.Code != test.status || strings.Contains(response.Body.String(), "PRIVATE_DATABASE_ERROR") {
						t.Fatalf("status=%d, want %d; body=%s", response.Code, test.status, response.Body.String())
					}
					if test.status == http.StatusOK {
						var result struct {
							Data struct {
								Items []struct {
									TotalTokens uint64 `json:"total_tokens"`
								} `json:"items"`
							} `json:"data"`
						}
						if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						if len(result.Data.Items) != 1 || result.Data.Items[0].TotalTokens != 18 {
							t.Fatalf("unexpected usage: %s", response.Body.String())
						}
					}
				})
			}
		})
	}
}
