package infrastructure

import (
	"context"
	"testing"

	"bosapi/backend/migrations"
)

func TestGatewayQuotaRetryMySQLRestartAndIdempotency(t *testing.T) {
	db, repository, now := partitionQuotaDatabase(t)
	script, err := migrations.Files.ReadFile("029_gateway_quota_retries.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(script)).Error; err != nil {
		t.Fatal(err)
	}
	record := partitionUsage(now, "durable-retry")
	record.DurationMillis = 1234
	ctx := context.Background()
	for range 2 {
		if err := repository.SaveGatewayQuotaRetry(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	// Recreate the repository: recovery reads durable SQL, not local state.
	restarted := NewKeyRepository(db)
	rows, cursor, err := restarted.ScanGatewayQuotaRetries(ctx, 0, 256)
	if err != nil || len(rows) != 1 || cursor == 0 {
		t.Fatalf("scan=%+v cursor=%d err=%v", rows, cursor, err)
	}
	if rows[0].DurationMillis != record.DurationMillis || !rows[0].CreatedAt.Equal(record.CreatedAt) {
		t.Fatalf("metadata changed: %+v", rows[0])
	}
	for attempt := range 2 {
		inserted, err := restarted.RecordGatewayUsageAndQuota(ctx, rows[0])
		if err != nil || inserted != (attempt == 0) {
			t.Fatalf("attempt=%d inserted=%v err=%v", attempt, inserted, err)
		}
	}
	if err := restarted.DeleteGatewayQuotaRetry(ctx, record.RequestID, record.UsageID); err != nil {
		t.Fatal(err)
	}
	rows, err = restarted.ListGatewayQuotaRetries(ctx, record.OrganizationID, 256)
	if err != nil || len(rows) != 0 {
		t.Fatalf("outbox not drained: %+v %v", rows, err)
	}
	var tokens uint64
	if err := db.Raw("SELECT quota_used_tokens FROM api_keys WHERE id = 7").Scan(&tokens).Error; err != nil || tokens != record.TotalTokens {
		t.Fatalf("tokens=%d err=%v", tokens, err)
	}
}

func TestGatewayQuotaRetryMySQLDeletedKey(t *testing.T) {
	for _, settled := range []bool{false, true} {
		name := "deleted before settlement"
		if settled {
			name = "deleted after settlement"
		}
		t.Run(name, func(t *testing.T) {
			db, repository, now := partitionQuotaDatabase(t)
			script, err := migrations.Files.ReadFile("029_gateway_quota_retries.sql")
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(string(script)).Error; err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			record := partitionUsage(now, "deleted-key-retry")
			if settled {
				if inserted, err := repository.RecordGatewayUsageAndQuota(ctx, record); err != nil || !inserted {
					t.Fatalf("initial settlement: inserted=%v err=%v", inserted, err)
				}
			}
			if err := repository.SaveGatewayQuotaRetry(ctx, record); err != nil {
				t.Fatal(err)
			}
			if err := repository.DeleteOwned(ctx, record.APIKeyID, 0, record.OrganizationID); err != nil {
				t.Fatal(err)
			}
			restarted := NewKeyRepository(db)
			rows, err := restarted.ListGatewayQuotaRetries(ctx, record.OrganizationID, 256)
			if err != nil || len(rows) != 1 {
				t.Fatalf("pending=%+v err=%v", rows, err)
			}
			for attempt := range 2 {
				inserted, err := restarted.RecordGatewayUsageAndQuota(ctx, rows[0])
				if err != nil || inserted != (!settled && attempt == 0) {
					t.Fatalf("attempt=%d inserted=%v err=%v", attempt, inserted, err)
				}
			}
			if err := restarted.DeleteGatewayQuotaRetry(ctx, record.RequestID, record.UsageID); err != nil {
				t.Fatal(err)
			}
			rows, err = restarted.ListGatewayQuotaRetries(ctx, record.OrganizationID, 256)
			if err != nil || len(rows) != 0 {
				t.Fatalf("outbox not drained: %+v %v", rows, err)
			}
			usage, err := FindOrganizationPeriodicUsage(ctx, db, record.OrganizationID, now)
			if err != nil || usage.DailyTokens != int64(record.TotalTokens) || usage.DailyAmount != 0.12345679 {
				t.Fatalf("organization usage=%+v err=%v", usage, err)
			}
			var receipts int64
			if err := db.Table("usage_log_receipts").Count(&receipts).Error; err != nil || receipts != 1 {
				t.Fatalf("receipts=%d err=%v", receipts, err)
			}
		})
	}
}
