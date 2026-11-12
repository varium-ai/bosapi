package infrastructure

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
)

func TestGatewayQuotaRetryRecordRoundTrip(t *testing.T) {
	firstToken := uint32(123)
	original := GatewayUsageRecord{
		RequestID: "req_retry", UsageID: "usage_retry", OrganizationID: 9, APIKeyID: 7,
		Model: "model", Endpoint: "chat/completions", StatusCode: 502, ErrorCode: "upstream_error",
		InputTokens: 11, CacheReadTokens: 2, CacheWriteTokens: 3, CacheReadCostAmount: 0.000001,
		CacheWriteCostAmount: 0.000002, OutputTokens: 13, TotalTokens: 29, CostAmount: 0.00012345,
		ClientIP: []byte{192, 0, 2, 7}, IsStream: true, DurationMillis: 456, FirstTokenMillis: &firstToken,
		CreatedAt: time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC),
	}
	got := newGatewayQuotaRetryRecord(original).gatewayUsageRecord()
	if got.RequestID != original.RequestID || got.UsageID != original.UsageID || got.OrganizationID != original.OrganizationID || got.APIKeyID != original.APIKeyID {
		t.Fatalf("identity changed: got=%+v want=%+v", got, original)
	}
	if got.Model != original.Model || got.Endpoint != original.Endpoint || got.StatusCode != original.StatusCode || got.ErrorCode != original.ErrorCode {
		t.Fatalf("request metadata changed: got=%+v want=%+v", got, original)
	}
	if got.InputTokens != original.InputTokens || got.CacheReadTokens != original.CacheReadTokens || got.CacheWriteTokens != original.CacheWriteTokens || got.OutputTokens != original.OutputTokens || got.TotalTokens != original.TotalTokens {
		t.Fatalf("token metrics changed: got=%+v want=%+v", got, original)
	}
	if got.CacheReadCostAmount != original.CacheReadCostAmount || got.CacheWriteCostAmount != original.CacheWriteCostAmount || got.CostAmount != original.CostAmount {
		t.Fatalf("cost metrics changed: got=%+v want=%+v", got, original)
	}
	if !bytes.Equal(got.ClientIP, original.ClientIP) || !got.CreatedAt.Equal(original.CreatedAt) || got.IsStream != original.IsStream || got.DurationMillis != original.DurationMillis || got.FirstTokenMillis == nil || *got.FirstTokenMillis != *original.FirstTokenMillis {
		t.Fatalf("transport metadata changed: got=%+v want=%+v", got, original)
	}
}

func TestGatewayQuotaRetryInsertUsesMigratedColumns(t *testing.T) {
	repository, mock := quotaRepository(t)
	record := GatewayUsageRecord{RequestID: "req", UsageID: "usage", OrganizationID: 2, APIKeyID: 7, DurationMillis: 1234, CreatedAt: time.Now().UTC()}
	retry := newGatewayQuotaRetryRecord(record)
	statement := repository.database.Session(&gorm.Session{DryRun: true, SkipDefaultTransaction: true}).Create(&retry).Statement.SQL.String()
	if strings.Contains(statement, "duration_millis") || !strings.Contains(statement, "`duration_ms`") {
		t.Fatalf("invalid column mapping: %s", statement)
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `gateway_quota_retries` .*`duration_ms`.*ON DUPLICATE KEY UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := repository.SaveGatewayQuotaRetry(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT .* FROM `gateway_quota_retries` WHERE id > \\? ORDER BY id ASC LIMIT \\?").WithArgs(uint64(256), 256).
		WillReturnRows(sqlmock.NewRows([]string{"id", "request_id", "usage_id", "organization_id", "api_key_id", "duration_ms", "created_at"}).AddRow(257, "req", "usage", 2, 7, 1234, record.CreatedAt))
	rows, cursor, err := repository.ScanGatewayQuotaRetries(context.Background(), 256, 256)
	if err != nil || len(rows) != 1 || cursor != 257 || rows[0].DurationMillis != 1234 {
		t.Fatalf("rows=%+v cursor=%d err=%v", rows, cursor, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
