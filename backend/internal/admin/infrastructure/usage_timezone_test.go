package infrastructure

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAdminUsageShanghaiCalendarBucketsKeepUTCFilter(t *testing.T) {
	localTime := "CONVERT_TZ(created_at, '+00:00', '+08:00')"
	for _, test := range []struct {
		period   string
		grouping string
		label    string
	}{
		{period: "total", grouping: "'total'", label: "total"},
		{period: "daily", grouping: "DATE_FORMAT(" + localTime + ", '%Y-%m-%d')", label: "2026-10-01"},
		{period: "weekly", grouping: "DATE_FORMAT(DATE_SUB(DATE(" + localTime + "), INTERVAL WEEKDAY(" + localTime + ") DAY), '%Y-%m-%d')", label: "2026-09-28"},
		{period: "monthly", grouping: "DATE_FORMAT(" + localTime + ", '%Y-%m')", label: "2026-10"},
	} {
		t.Run(test.period, func(t *testing.T) {
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
			from := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
			to := from.AddDate(0, 0, 1)
			query := "^SELECT " + regexp.QuoteMeta(test.grouping+" AS period,") + ".*FROM usage_logs_read AS usage_logs WHERE usage_logs.organization_id = \\? AND usage_logs.created_at >= \\? AND usage_logs.created_at < \\?"
			args := []driver.Value{uint64(2), from, to}
			if test.period != "total" {
				query += " GROUP BY " + regexp.QuoteMeta(test.grouping) + " ORDER BY period ASC LIMIT \\?"
				args = append(args, 1000)
			}
			mock.ExpectQuery(query + "$").WithArgs(args...).WillReturnRows(sqlmock.NewRows([]string{"period", "requests", "errors", "input_tokens", "output_tokens", "total_tokens", "cost_amount"}).AddRow(test.label, 2, 0, 11, 7, 18, 0.01))
			items, err := NewAdminUsageRepository(database).AggregateAdminUsage(context.Background(), domain.AdminUsageFilter{OrganizationID: 2, From: &from, To: &to}, test.period)
			if err != nil || len(items) != 1 || items[0].Period != test.label || items[0].TotalTokens != 18 {
				t.Fatalf("items=%+v err=%v", items, err)
			}
		})
	}
}
