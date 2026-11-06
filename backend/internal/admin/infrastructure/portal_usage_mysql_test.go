package infrastructure

import (
	"context"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/usagerollup"
)

func TestPortalUsageMySQLSnapshotRetainsHistoricalTotalsAndPendingTail(t *testing.T) {
	db := newAdminDashboardMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	if err := db.Exec(insertRollupTestLog, 1, time.Now().AddDate(0, 0, -60)).Error; err != nil {
		t.Fatal(err)
	}
	if count, err := usagerollup.New(db).Batch(ctx, 100); err != nil || count != 1 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	if err := db.Exec("DELETE FROM usage_logs WHERE rollup_applied = 1").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(insertRollupTestLog, 2, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO usage_logs (organization_id, api_key_id, total_tokens, cost_amount, created_at)
	 VALUES (10, 7, 1000, 1, UTC_TIMESTAMP()), (9, 8, 1000, 1, UTC_TIMESTAMP())`).Error; err != nil {
		t.Fatal(err)
	}
	repository := NewSharedPortalUsageRepository(db, nil)
	defer repository.Close()
	filter := domain.AdminUsageFilter{OrganizationID: 9, APIKeyID: 7}
	first, err := repository.AggregateAdminUsageSnapshot(ctx, filter, "total")
	if err != nil || len(first.Items) != 1 || first.Items[0].Requests != 2 || first.Items[0].TotalTokens != 6 || first.Items[0].CostAmount != 0.00000002 || first.Snapshot == nil || first.Snapshot.Status != "ready" {
		t.Fatalf("snapshot=%+v error=%v", first, err)
	}
	if err := db.Exec(insertRollupTestLog, 10, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	second, err := repository.AggregateAdminUsageSnapshot(ctx, filter, "total")
	if err != nil || second.Items[0].Requests != 2 || second.Snapshot.ID != first.Snapshot.ID {
		t.Fatalf("cache=%+v error=%v", second, err)
	}
	if lifetime := first.Snapshot.ValidUntil.Sub(first.Snapshot.GeneratedAt); lifetime > 5*time.Minute || lifetime <= 0 {
		t.Fatalf("cache lifetime=%s", lifetime)
	}
	filter.OrganizationID = 10
	other, err := repository.AggregateAdminUsageSnapshot(ctx, filter, "total")
	if err != nil || len(other.Items) != 1 || other.Items[0].Requests != 1 || other.Items[0].TotalTokens != 1000 {
		t.Fatalf("scope=%+v error=%v", other, err)
	}
}
