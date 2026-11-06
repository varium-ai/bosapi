package infrastructure

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
)

func TestAdminUsageFirstTokenTimingPreservesMissingAndZero(t *testing.T) {
	for _, test := range []struct {
		name      string
		value     any
		jsonValue string
	}{
		{name: "historical missing", jsonValue: "null"},
		{name: "zero", value: uint32(0), jsonValue: "0"},
		{name: "measured", value: uint32(123), jsonValue: "123"},
		{name: "maximum", value: ^uint32(0), jsonValue: "4294967295"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sqlDatabase, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Error(err)
				}
				mock.ExpectClose()
				if err := sqlDatabase.Close(); err != nil {
					t.Error(err)
				}
			})
			database, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDatabase, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			mock.ExpectQuery("^SELECT count\\(\\*\\) FROM usage_logs_read AS usage_logs WHERE usage_logs.organization_id = \\?$").
				WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			mock.ExpectQuery("^SELECT .*usage_logs.first_token_ms.*FROM usage_logs_read AS usage_logs.* WHERE usage_logs.organization_id = \\? ORDER BY usage_logs.created_at DESC, usage_logs.id DESC LIMIT \\?$").
				WithArgs(uint64(2), 20).
				WillReturnRows(sqlmock.NewRows([]string{"id", "first_token_ms"}).AddRow(9, test.value))
			items, total, err := NewAdminUsageRepository(database).ListAdminUsage(ctx, domain.AdminUsageFilter{OrganizationID: 2}, 1, 20)
			if err != nil || total != 1 || len(items) != 1 {
				t.Fatalf("items=%+v total=%d err=%v", items, total, err)
			}
			encoded, err := json.Marshal(items[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"first_token_ms":`+test.jsonValue) {
				t.Fatalf("first token JSON = %s, want %s", encoded, test.jsonValue)
			}
		})
	}
}
