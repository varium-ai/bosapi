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
