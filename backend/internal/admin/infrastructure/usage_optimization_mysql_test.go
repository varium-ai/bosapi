package infrastructure

import (
	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/usagerollup"
	"bosapi/backend/internal/platform/usagetime"
	"context"
	"testing"
	"time"
)

func TestAdminUsageFastPagesAndSnapshotMySQL(t *testing.T) {
	db := newAdminDashboardMySQL(t)
	ctx := context.Background()
	day := usagetime.DayStart(time.Now()).UTC()
	end := day.Add(24 * time.Hour)
	for _, q := range []string{"INSERT INTO organizations VALUES (1,'Historical'),(2,'Current')", "INSERT INTO api_keys VALUES (7,'Moved',2)"} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 6; i++ {
		if err := db.Exec("INSERT INTO usage_logs(id,organization_id,api_key_id,total_tokens,cost_amount,created_at) VALUES (?,1,7,10,0.00000003,?)", i, day).Error; err != nil {
			t.Fatal(err)
		}
	}
	// A completed mover transaction leaves each ID in exactly one source.
	if err := db.Exec("INSERT INTO usage_logs_v2 SELECT * FROM usage_logs WHERE id IN (2,4,6); DELETE FROM usage_logs WHERE id IN (2,4,6)").Error; err != nil {
		t.Fatal(err)
	}
	r := NewAdminUsageRepository(db)
	f := domain.AdminUsageFilter{OrganizationID: 1, From: &day, To: &end, SkipTotal: true}
	items, total, err := r.ListAdminUsage(ctx, f, 1, 2)
	if err != nil || total != -1 || len(items) != 3 || items[0].ID != 6 || items[1].ID != 5 || items[2].ID != 4 {
		t.Fatalf("first=%+v total=%d err=%v", items, total, err)
	}
	f.Before = &items[1].CreatedAt
	f.BeforeID = items[1].ID
	next, _, err := r.ListAdminUsage(ctx, f, 1, 2)
	if err != nil || len(next) != 3 || next[0].ID != 4 || next[1].ID != 3 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	f.Before = nil
	f.BeforeID = 0
	offset, _, err := r.ListAdminUsage(ctx, f, 2, 2)
	if err != nil || len(offset) != 3 || offset[0].ID != 4 {
		t.Fatalf("offset=%+v err=%v", offset, err)
	}
	if _, err := usagerollup.New(db).Batch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	cached := NewCachedAdminUsageRepository(db)
	defer cached.Close()
	dashboard, err := cached.DailyAdminOrganizationUsage(ctx, domain.AdminOrganizationUsageFilter{AdminUsageFilter: f}, 1, 20)
	if err != nil || dashboard.Snapshot.Status != "ready" || dashboard.Summary.Requests != 6 || len(dashboard.TopKeys) != 1 || dashboard.TopKeys[0].OrganizationName != "Historical" {
		t.Fatalf("dashboard=%+v err=%v", dashboard, err)
	}
}

func TestDisplaySnapshotWithholdsUnprocessedHistoryMySQL(t *testing.T) {
	db := newAdminDashboardMySQL(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-time.Hour)
	if err := db.Exec("INSERT INTO usage_logs(organization_id,api_key_id,total_tokens,cost_amount,created_at) VALUES (1,7,10,0.00000003,?)", old).Error; err != nil {
		t.Fatal(err)
	}
	r := NewCachedAdminUsageRepository(db)
	value, err := r.AggregateAdminUsageSnapshot(ctx, domain.AdminUsageFilter{}, "total")
	r.Close()
	if err != nil || value.Snapshot.Status != "expired" || len(value.Items) != 0 {
		t.Fatalf("unprocessed history was presented as fresh: %+v %v", value, err)
	}
	if _, err := usagerollup.New(db).Batch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	fresh := NewCachedAdminUsageRepository(db)
	defer fresh.Close()
	value, err = fresh.AggregateAdminUsageSnapshot(ctx, domain.AdminUsageFilter{}, "total")
	if err != nil || value.Snapshot.Status != "ready" || len(value.Items) != 1 || value.Items[0].Requests != 1 {
		t.Fatalf("materialized history unavailable: %+v %v", value, err)
	}
}
