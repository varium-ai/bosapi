package infrastructure

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This optional test needs a MySQL connection, but reads only a derived constant
// table. It does not create tables or modify records in the configured database.
func TestAdminUsageMySQLShanghaiCalendarBoundaries(t *testing.T) {
	dsn := os.Getenv("BOS_USAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("set BOS_USAGE_TEST_DSN to verify Shanghai calendar boundaries against MySQL")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	transaction := database.WithContext(ctx).Begin(&sql.TxOptions{ReadOnly: true})
	if transaction.Error != nil {
		t.Fatal(transaction.Error)
	}
	defer func() {
		if err := transaction.Rollback().Error; err != nil {
			t.Error(err)
		}
	}()

	timestamps := []any{
		"2026-09-30 15:59:59", "2026-09-30 16:00:00",
		"2026-10-11 15:59:59", "2026-10-11 16:00:00",
		"2026-10-12 15:59:59", "2026-10-12 16:00:00",
	}
	rows := make([]string, len(timestamps))
	for index := range rows {
		rows[index] = "SELECT CAST(? AS DATETIME) AS created_at, 2 AS organization_id, 7 AS api_key_id, 'model' AS model, 200 AS status_code, 1 AS input_tokens, 2 AS output_tokens, 3 AS total_tokens, 0.01 AS cost_amount"
	}
	fixture := transaction.Table("("+strings.Join(rows, " UNION ALL ")+") AS usage_logs", timestamps...)
	repository := NewAdminUsageRepository(fixture)
	for _, test := range []struct {
		name     string
		period   string
		boundary time.Time
		labels   []string
	}{
		{name: "midnight", period: "daily", boundary: time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC), labels: []string{"2026-10-12", "2026-10-13"}},
		{name: "Monday", period: "weekly", boundary: time.Date(2026, 10, 11, 16, 0, 0, 0, time.UTC), labels: []string{"2026-10-05", "2026-10-12"}},
		{name: "month start", period: "monthly", boundary: time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC), labels: []string{"2026-09", "2026-10"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			from, to := test.boundary.Add(-time.Second), test.boundary.Add(time.Second)
			items, err := repository.AggregateAdminUsage(ctx, domain.AdminUsageFilter{OrganizationID: 2, From: &from, To: &to}, test.period)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != len(test.labels) {
				t.Fatalf("items=%+v, expected labels=%v", items, test.labels)
			}
			for index, label := range test.labels {
				if items[index].Period != label || items[index].Requests != 1 || items[index].TotalTokens != 3 {
					t.Fatalf("item=%+v, expected one request in %s", items[index], label)
				}
			}
		})
	}
}
