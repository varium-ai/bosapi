package interfaces

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/platform/httpapi"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestSetupStatusScopesEvidenceAndDoesNotCallUpstream(t *testing.T) {
	for _, item := range []struct {
		name, role, id       string
		org                  uint64
		status               int
		active, key, success bool
	}{
		{name: "anonymous", id: "7", status: 401},
		{name: "other tenant", role: "org_admin", org: 8, id: "7", status: 403},
		{name: "key viewer", role: "key_viewer", org: 7, id: "7", status: 403},
		{name: "invalid id", role: "super_admin", id: "x", status: 400},
		{name: "disabled organization", role: "org_admin", org: 7, id: "7", status: 200},
		{name: "fresh organization", role: "org_admin", org: 7, id: "7", active: true, status: 200},
		{name: "key without call", role: "super_admin", id: "7", active: true, key: true, status: 200},
		{name: "successful call", role: "super_admin", id: "7", active: true, key: true, success: true, status: 200},
	} {
		t.Run(item.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if item.status == 200 {
				state := "disabled"
				if item.active {
					state = "active"
				}
				mock.ExpectQuery("SELECT .* FROM `organizations`").WithArgs(uint64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "status", "updated_at"}).AddRow(7, state, now))
				if item.active {
					mock.ExpectQuery("SELECT a.id, a.updated_at, a.verified_at FROM accounts AS a").WillReturnRows(sqlmock.NewRows([]string{"id", "updated_at", "verified_at", "private"}))
					mock.ExpectBegin()
					candidates := sqlmock.NewRows([]string{"id", "updated_at", "has_periodic_quota"})
					if item.key {
						candidates.AddRow(10, now, false)
					}
					mock.ExpectQuery("SELECT k.id, k.updated_at,.*FROM api_keys AS k.*EXISTS.*allowed_models").WillReturnRows(candidates)
					if item.key {
						rows := sqlmock.NewRows([]string{"created_at"})
						if item.success {
							rows.AddRow(now)
						}
						mock.ExpectQuery("SELECT created_at FROM.*usage_logs AS u FORCE INDEX.*pm.updated_at.*usage_logs_v2 AS u FORCE INDEX.*LIMIT 1").WillReturnRows(rows)
					}
					mock.ExpectCommit()
				}
			}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				if item.role != "" {
					ctx.Set("identity", httpapi.Claims{Role: item.role, OrganizationID: item.org})
				}
				ctx.Next()
			})
			NewSetupHandler(db).RegisterRoutes(router.Group("/admin"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", "/admin/organizations/"+item.id+"/setup-status", nil))
			if response.Code != item.status {
				t.Fatalf("%d: %s", response.Code, response.Body.String())
			}
			if item.status == 200 {
				if strings.Contains(response.Body.String(), `"has_usable_key":true`) != item.key {
					t.Fatalf("wrong key status: %s", response.Body.String())
				}
				if strings.Contains(response.Body.String(), `"successful_call_at":null`) == item.success {
					t.Fatalf("wrong call evidence: %s", response.Body.String())
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetupStatusSkipsExhaustedBudgetAndChecksLaterKeys(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT k.id, k.updated_at").WillReturnRows(sqlmock.NewRows([]string{"id", "updated_at", "has_periodic_quota"}).
		AddRow(10, now, true).AddRow(11, now, false).AddRow(12, now, true))
	mock.ExpectQuery("SELECT .* AS usable FROM api_keys AS k CROSS JOIN").WillReturnRows(sqlmock.NewRows([]string{"usable"}).AddRow(false))
	mock.ExpectQuery("SELECT created_at FROM").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
	mock.ExpectQuery("SELECT .* AS usable FROM api_keys AS k CROSS JOIN").WillReturnRows(sqlmock.NewRows([]string{"usable"}).AddRow(true))
	mock.ExpectQuery("SELECT created_at FROM").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now.Add(time.Hour)))
	mock.ExpectCommit()
	usable, latest, err := loadSetupKeyStatus(db, 7, now, now, false)
	if err != nil || !usable || latest == nil || !latest.Equal(now.Add(time.Hour)) {
		t.Fatalf("usable=%v latest=%v error=%v", usable, latest, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSetupStatusPropagatesReadFailures(t *testing.T) {
	for _, stage := range []string{"candidates", "quota", "evidence"} {
		t.Run(stage, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("database unavailable")
			now := time.Now().UTC()
			mock.ExpectBegin()
			candidates := mock.ExpectQuery("SELECT k.id, k.updated_at")
			if stage == "candidates" {
				candidates.WillReturnError(failure)
			} else {
				candidates.WillReturnRows(sqlmock.NewRows([]string{"id", "updated_at", "has_periodic_quota"}).AddRow(10, now, stage == "quota"))
				if stage == "quota" {
					mock.ExpectQuery("SELECT .* AS usable FROM api_keys AS k CROSS JOIN").WillReturnError(failure)
				} else {
					mock.ExpectQuery("SELECT created_at FROM").WillReturnError(failure)
				}
			}
			mock.ExpectRollback()
			if _, _, err := loadSetupKeyStatus(db, 7, now, now, false); !errors.Is(err, failure) {
				t.Fatalf("read failure was hidden: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
