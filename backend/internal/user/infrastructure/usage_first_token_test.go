package infrastructure

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"bosapi/backend/internal/user/domain"
)

func firstTokenMillis(value uint32) *uint32 { return &value }

func TestUsageFirstTokenTimingReadPreservesMissingAndZero(t *testing.T) {
	for _, test := range []struct {
		name      string
		value     *uint32
		jsonValue string
	}{
		{name: "historical missing", jsonValue: "null"},
		{name: "zero", value: firstTokenMillis(0), jsonValue: "0"},
		{name: "measured", value: firstTokenMillis(123), jsonValue: "123"},
		{name: "maximum", value: firstTokenMillis(^uint32(0)), jsonValue: "4294967295"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, mock := usageRepository(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			filter := domain.UsageFilter{OrganizationID: 2, APIKeyID: 7, Model: "'; DROP TABLE usage_logs; --"}
			where := " WHERE \\(organization_id = \\? AND organization_id > 0\\) AND api_key_id = \\? AND model = \\?"
			mock.ExpectQuery("^SELECT count\\(\\*\\) FROM usage_logs_read AS usage_logs"+where+"$").
				WithArgs(uint64(2), uint64(7), filter.Model).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			mock.ExpectQuery("^SELECT \\* FROM usage_logs_read AS usage_logs"+where+" ORDER BY created_at DESC, id DESC LIMIT \\?$").
				WithArgs(uint64(2), uint64(7), filter.Model, 20).
				WillReturnRows(sqlmock.NewRows([]string{"id", "first_token_ms"}).AddRow(9, test.value))
			items, total, err := repository.ListUsage(ctx, filter, 1, 20)
			if err != nil || total != 1 || len(items) != 1 {
				t.Fatalf("items=%+v total=%d err=%v", items, total, err)
			}
			assertFirstTokenJSON(t, items[0], test.jsonValue)
			mock.ExpectQuery("^SELECT \\* FROM usage_logs_read AS usage_logs"+where+" AND id = \\? ORDER BY `usage_logs`.`id` LIMIT \\?$").
				WithArgs(uint64(2), uint64(7), filter.Model, uint64(9), 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", "first_token_ms"}).AddRow(9, test.value))
			record, err := repository.FindUsage(ctx, filter, 9)
			if err != nil {
				t.Fatalf("find usage: %v", err)
			}
			assertFirstTokenJSON(t, record, test.jsonValue)
		})
	}
}

func assertFirstTokenJSON(t *testing.T, record domain.UsageRecord, want string) {
	t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"first_token_ms":`+want) {
		t.Fatalf("first token JSON = %s, want %s", encoded, want)
	}
}

func TestRecordGatewayUsagePersistsFirstTokenTiming(t *testing.T) {
	for _, test := range []struct {
		name  string
		value *uint32
	}{
		{name: "unavailable"},
		{name: "zero", value: firstTokenMillis(0)},
		{name: "measured", value: firstTokenMillis(123)},
		{name: "maximum", value: firstTokenMillis(^uint32(0))},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, mock := usageRepository(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			record := GatewayUsageRecord{RequestID: "req_timing", UsageID: "usage_timing", OrganizationID: 2, APIKeyID: 7, FirstTokenMillis: test.value, CreatedAt: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)}
			mock.ExpectQuery("^SELECT `id`,`user_id`,`organization_id` FROM `api_keys` WHERE id = \\? AND organization_id = \\? ORDER BY `api_keys`.`id` LIMIT \\?$").
				WithArgs(record.APIKeyID, record.OrganizationID, 1).
				WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id"}).AddRow(7, 2))
			mock.ExpectBegin()
			expectUsageInsert(mock, record, false, false, nil)
			mock.ExpectCommit()
			if err := repository.RecordGatewayUsage(ctx, record); err != nil {
				t.Fatalf("record gateway usage: %v", err)
			}
		})
	}
}
