package infrastructure

import (
	"context"
	"database/sql/driver"
	"regexp"
	"strconv"
	"testing"
	"time"

	"bosapi/backend/internal/platform/usagetime"
	"bosapi/backend/internal/user/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func usageRepository(t *testing.T) (*KeyRepository, sqlmock.Sqlmock) {
	t.Helper()
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
	return NewKeyRepository(database), mock
}

func TestTrendUsageShanghaiDayBucketsKeepUTCFilter(t *testing.T) {
	repository, mock := usageRepository(t)
	from := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	filter := domain.UsageFilter{OrganizationID: 2, UserID: 3, APIKeyID: 7, From: &from, To: &to}
	day := "DATE_FORMAT(CONVERT_TZ(created_at, '+00:00', '+08:00'), '%Y-%m-%d')"
	mock.ExpectQuery("^SELECT "+regexp.QuoteMeta(day+" AS date,")+".*FROM usage_logs_read AS usage_logs WHERE .*created_at >= \\? AND created_at < \\? GROUP BY "+regexp.QuoteMeta(day)+" ORDER BY date ASC LIMIT \\?$").
		WithArgs(uint64(2), uint64(7), from, to, 366).
		WillReturnRows(sqlmock.NewRows([]string{"date", "requests", "errors", "input_tokens", "output_tokens", "total_tokens", "cost_amount"}).AddRow([]byte("2026-10-13"), 2, 0, 11, 7, 18, 0.01))
	items, err := repository.TrendUsage(context.Background(), filter)
	if err != nil || len(items) != 1 || items[0].Date != "2026-10-13" || items[0].TotalTokens != 18 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestDailyUsageByKeyIncludesTodayInShanghaiCalendarWindow(t *testing.T) {
	for _, days := range []int{1, 7, 30} {
		t.Run(strconv.Itoa(days)+" days", func(t *testing.T) {
			repository, mock := usageRepository(t)
			date := "DATE_FORMAT(CONVERT_TZ(created_at, '+00:00', '+08:00'), '%Y-%m-%d')"
			before := time.Now()
			mock.ExpectQuery("^SELECT "+regexp.QuoteMeta(date+" AS date,")+".*FROM usage_logs_read AS usage_logs WHERE .*created_at >= \\? GROUP BY "+regexp.QuoteMeta(date)+" ORDER BY date ASC$").
				WithArgs(uint64(2), uint64(7), shanghaiWindowStart{now: before, days: days}).
				WillReturnRows(sqlmock.NewRows([]string{"date", "requests", "errors", "input_tokens", "output_tokens", "total_tokens", "cost_amount"}))
			if _, err := repository.DailyUsageByKey(context.Background(), domain.UsageFilter{OrganizationID: 2, UserID: 3, APIKeyID: 7}, days); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type shanghaiWindowStart struct {
	now  time.Time
	days int
}

func (argument shanghaiWindowStart) Match(value driver.Value) bool {
	start, ok := value.(time.Time)
	if !ok || start.Location() != time.UTC {
		return false
	}
	// Allow a test that happens to run across Shanghai midnight.
	before := usagetime.DayStart(argument.now).AddDate(0, 0, 1-argument.days)
	after := usagetime.DayStart(time.Now()).AddDate(0, 0, 1-argument.days)
	return start.Equal(before) || start.Equal(after)
}

func TestUsageByKeysShanghaiDayBucketsKeepUTCFilter(t *testing.T) {
	repository, mock := usageRepository(t)
	from := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	filter := domain.UsageFilter{OrganizationID: 2, UserID: 3, From: &from, To: &to}
	date := "DATE_FORMAT(CONVERT_TZ(created_at, '+00:00', '+08:00'), '%Y-%m-%d')"
	mock.ExpectQuery("^SELECT api_key_id, "+regexp.QuoteMeta(date+" AS date,")+".*FROM usage_logs_read AS usage_logs WHERE .*created_at >= \\? AND created_at < \\? GROUP BY "+regexp.QuoteMeta("api_key_id, "+date)+" ORDER BY date ASC, api_key_id ASC LIMIT \\?$").
		WithArgs(uint64(2), uint64(7), uint64(8), from, to, 10000).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "date", "requests", "errors", "input_tokens", "output_tokens", "total_tokens", "cost_amount"}).AddRow(7, "2026-10-13", 2, 0, 11, 7, 18, 0.01))
	items, err := repository.UsageByKeys(context.Background(), filter, []uint64{7, 8})
	if err != nil || len(items) != 1 || items[0].Date != "2026-10-13" || items[0].APIKeyID != 7 || items[0].TotalTokens != 18 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestRecordGatewayUsagePersistsCacheBreakdown(t *testing.T) {
	repository, mock := usageRepository(t)
	record := GatewayUsageRecord{RequestID: "req-cache", UsageID: "usage-cache", OrganizationID: 2, APIKeyID: 7,
		InputTokens: 100, CacheReadTokens: 60, CacheWriteTokens: 20, OutputTokens: 10, TotalTokens: 110,
		CacheReadCostAmount: 0.000012, CacheWriteCostAmount: 0.00005, CostAmount: 0.000202,
		CreatedAt: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)}
	mock.ExpectQuery("^SELECT `id`,`user_id`,`organization_id` FROM `api_keys` WHERE id = \\? AND organization_id = \\? ORDER BY `api_keys`.`id` LIMIT \\?$").
		WithArgs(record.APIKeyID, record.OrganizationID, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id"}).AddRow(7, 2))
	mock.ExpectBegin()
	expectUsageInsert(mock, record, false, false, nil)
	mock.ExpectCommit()
	if err := repository.RecordGatewayUsage(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}
