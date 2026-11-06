package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func quotaRepository(t *testing.T) (*KeyRepository, sqlmock.Sqlmock) {
	t.Helper()
	sqlDatabase, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
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

func TestRecordGatewayUsageAndQuotaTransaction(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, test := range []struct {
		name        string
		keyError    bool
		insertError bool
		updateError bool
		duplicate   bool
		moved       bool
		cost        float64
		firstToken  *uint32
	}{
		{name: "charge once"},
		{name: "first token timing retained", firstToken: firstTokenMillis(123)},
		{name: "zero first token timing retained", firstToken: firstTokenMillis(0)},
		{name: "maximum first token timing retained", firstToken: firstTokenMillis(^uint32(0))},
		{name: "charge ledger amount rounded down", cost: 0.123456784},
		{name: "charge ledger amount rounded up", cost: 0.123456789},
		{name: "charge ledger amount below decimal precision", cost: 0.000000004},
		{name: "authenticated organization snapshot retained", moved: true},
		{name: "duplicate does not charge", duplicate: true},
		{name: "key failure rolls back", keyError: true},
		{name: "log failure rolls back", insertError: true},
		{name: "quota failure rolls back", updateError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, mock := quotaRepository(t)
			record := GatewayUsageRecord{RequestID: "req_quota", UsageID: "usage_quota", APIKeyID: 7, OrganizationID: 2, TotalTokens: 277, CostAmount: 0.001, CreatedAt: time.Now().UTC()}
			record.FirstTokenMillis = test.firstToken
			if test.cost != 0 {
				record.CostAmount = test.cost
			}
			mock.ExpectBegin()
			query := mock.ExpectQuery("SELECT .* FROM `api_keys` WHERE id = \\? ORDER BY").WithArgs(uint64(7), 1)
			if test.keyError {
				query.WillReturnError(failure)
			} else {
				organizationID := 2
				if test.moved {
					organizationID = 20
				}
				query.WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "organization_id"}).AddRow(7, 3, organizationID))
				expectQuotaTransaction(mock, record, test.insertError, test.updateError, test.duplicate, failure)
			}
			wantError := test.keyError || test.insertError || test.updateError
			if wantError {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			inserted, err := repository.RecordGatewayUsageAndQuota(context.Background(), record)
			if wantError && !errors.Is(err, failure) || !wantError && err != nil {
				t.Fatalf("inserted=%v err=%v", inserted, err)
			}
			if !wantError && inserted == test.duplicate {
				t.Fatalf("inserted=%v duplicate=%v", inserted, test.duplicate)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func expectQuotaTransaction(mock sqlmock.Sqlmock, record GatewayUsageRecord, insertError, updateError, duplicate bool, failure error) {
	if !expectUsageInsert(mock, record, insertError, duplicate, failure) {
		return
	}

	// Legacy deployments allow NULL counters; ordinary addition would leave
	// those keys permanently uncharged and permit them to bypass quota checks.
	update := mock.ExpectExec("UPDATE `api_keys` SET .*quota_used_amount.*COALESCE\\(quota_used_amount, 0\\) \\+ \\(SELECT cost_amount FROM `usage_log_receipts` WHERE request_id = \\? AND usage_id = \\?\\).*quota_used_tokens.*COALESCE\\(quota_used_tokens, 0\\)").
		WithArgs(record.CreatedAt, record.RequestID, record.UsageID, record.TotalTokens, record.APIKeyID)
	if updateError {
		update.WillReturnError(failure)
		return
	}
	update.WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectUsageInsert(mock sqlmock.Sqlmock, record GatewayUsageRecord, insertError, duplicate bool, failure error) bool {
	mock.ExpectQuery(`SELECT retained_from, partition_writes, legacy_high_watermark FROM usage_log_retention WHERE id = 1 FOR SHARE`).
		WillReturnRows(sqlmock.NewRows([]string{"retained_from", "partition_writes", "legacy_high_watermark"}).AddRow(record.CreatedAt.AddDate(0, 0, -30), true, 0))
	receipt := mock.ExpectExec(`INSERT INTO usage_log_receipts`).WithArgs(record.RequestID, record.UsageID, record.CostAmount)
	if duplicate {
		receipt.WillReturnError(&drivermysql.MySQLError{Number: 1062, Message: "duplicate receipt"})
		return false
	}
	receipt.WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`SELECT LAST_INSERT_ID\(\)`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM usage_logs WHERE request_id = \? AND usage_id = \?\)`).
		WithArgs(record.RequestID, record.UsageID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	insert := mock.ExpectExec("INSERT INTO `usage_logs_v2`").WithArgs(
		record.RequestID, record.UsageID, nil, record.OrganizationID, record.APIKeyID,
		record.Model, nil, record.Endpoint, record.StatusCode, record.ErrorCode, record.InputTokens,
		record.CacheReadTokens, record.CacheWriteTokens, record.CacheReadCostAmount, record.CacheWriteCostAmount,
		record.OutputTokens, record.TotalTokens, record.CostAmount, sqlmock.AnyArg(), record.IsStream,
		record.DurationMillis, record.FirstTokenMillis, record.CreatedAt, uint64(1),
	)
	if insertError {
		insert.WillReturnError(failure)
		return false
	}
	insert.WillReturnResult(sqlmock.NewResult(1, 1))
	return true
}

func TestRecordGatewayUsageAndQuotaRejectsInvalidRecords(t *testing.T) {
	for _, repository := range []*KeyRepository{nil, NewKeyRepository(nil)} {
		if _, err := repository.RecordGatewayUsageAndQuota(context.Background(), GatewayUsageRecord{}); err == nil {
			t.Fatal("invalid repository accepted")
		}
	}
	repository, mock := quotaRepository(t)
	for _, record := range []GatewayUsageRecord{{RequestID: "req_missing_key"}, {APIKeyID: 7}} {
		if _, err := repository.RecordGatewayUsageAndQuota(context.Background(), record); err == nil {
			t.Fatal("invalid record accepted")
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
