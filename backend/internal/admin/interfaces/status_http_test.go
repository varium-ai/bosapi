package interfaces

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authinterfaces "bosapi/backend/internal/auth/interfaces"
	"bosapi/backend/internal/platform/httpapi"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestStatusRetainsDiagnosticsWhenMySQLUnavailableAndEnforcesRole(t *testing.T) {
	t.Setenv("BOS_MONITOR_NODE_DIR", "")
	t.Setenv("BOS_MONITOR_TLS_TARGETS", "")
	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectPing().WillReturnError(errors.New("private database error"))
	handler := NewStatusHandler(db, time.Now())
	defer handler.Monitor.Close()
	deadline := time.Now().Add(time.Second)
	for handler.Monitor.Snapshot().At.IsZero() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for _, role := range []string{"org_admin", "key_viewer", "super_admin"} {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("identity", httpapi.Claims{UserID: 1, Role: role}) })
		handler.RegisterRoutes(router.Group("/admin", authinterfaces.RequireRoles("super_admin")))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/admin/status", nil))
		expected := 403
		if role == "super_admin" {
			expected = 503
		}
		if response.Code != expected {
			t.Fatalf("%s: %d %s", role, response.Code, response.Body)
		}
		if strings.Contains(response.Body.String(), "private database error") {
			t.Fatal("leaked backend error")
		}
		if role == "super_admin" && (!strings.Contains(response.Body.String(), `"history"`) || response.Header().Get("Cache-Control") != "no-store") {
			t.Fatal(response.Body)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
