package infrastructure

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

type usageCacheSourceStub struct {
	domain.AdminUsageRepository
	calls atomic.Int32
}

func (s *usageCacheSourceStub) DailyAdminOrganizationUsage(_ context.Context, f domain.AdminOrganizationUsageFilter, _, size int) (domain.AdminOrganizationUsageDashboard, error) {
	s.calls.Add(1)
	items := make([]domain.AdminOrganizationUsagePoint, 12)
	for i := range items {
		items[i] = domain.AdminOrganizationUsagePoint{AdminUsagePoint: domain.AdminUsagePoint{TotalTokens: uint64(i + 1)}, OrganizationID: f.OrganizationID}
	}
	return domain.AdminOrganizationUsageDashboard{Items: items, Total: 12, Summary: domain.AdminUsagePoint{TotalTokens: 78, CostAmount: 0.00000003}, TopKeys: []domain.AdminKeyTokenRank{{APIKeyID: f.APIKeyID, TotalTokens: 78}}}, nil
}
func (s *usageCacheSourceStub) AggregateAdminUsage(_ context.Context, f domain.AdminUsageFilter, p string) ([]domain.AdminUsagePoint, error) {
	s.calls.Add(1)
	return []domain.AdminUsagePoint{{Period: p, Requests: f.OrganizationID}}, nil
}
func TestUsageSnapshotPagesShareOneImmutableResult(t *testing.T) {
	source := &usageCacheSourceStub{}
	r := newCachedAdminUsageRepository(source)
	defer r.Close()
	f := domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{OrganizationID: 9}}
	first, err := r.DailyAdminOrganizationUsage(context.Background(), f, 1, 2)
	if err != nil || len(first.Items) != 2 || first.Total != 12 || first.Summary.CostAmount != 0.00000003 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	id := first.Snapshot.ID
	first.Items[0].TotalTokens = 999
	first.TopKeys[0].TotalTokens = 999
	f.SnapshotID = id
	second, err := r.DailyAdminOrganizationUsage(context.Background(), f, 2, 2)
	if err != nil || second.Snapshot.ID != id || second.Items[0].TotalTokens != 3 || second.TopKeys[0].TotalTokens != 78 || source.calls.Load() != 1 {
		t.Fatalf("second=%+v err=%v calls=%d", second, err, source.calls.Load())
	}
	end, _ := r.DailyAdminOrganizationUsage(context.Background(), f, 100, 2)
	if len(end.Items) != 0 || end.Total != 12 {
		t.Fatalf("end=%+v", end)
	}
	f.SnapshotID = "old-version"
	replaced, _ := r.DailyAdminOrganizationUsage(context.Background(), f, 2, 2)
	if replaced.Snapshot.Status != "replaced" || len(replaced.Items) != 0 {
		t.Fatalf("replaced=%+v", replaced)
	}
}
func TestUsageSnapshotScopeIsolation(t *testing.T) {
	from := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	base := domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}}
	changes := []func(*domain.AdminOrganizationUsageFilter){
		func(f *domain.AdminOrganizationUsageFilter) { f.OrganizationID = 9 }, func(f *domain.AdminOrganizationUsageFilter) { f.APIKeyID = 7 }, func(f *domain.AdminOrganizationUsageFilter) { f.Model = "m" }, func(f *domain.AdminOrganizationUsageFilter) { f.ClientIP = "192.0.2.1" }, func(f *domain.AdminOrganizationUsageFilter) { f.Search = "Team" }, func(f *domain.AdminOrganizationUsageFilter) { v := from.Add(time.Hour); f.From = &v }, func(f *domain.AdminOrganizationUsageFilter) { v := to.Add(time.Hour); f.To = &v },
	}
	seen := map[string]bool{usageSnapshotKey(base.AdminUsageFilter, "", "dashboard"): true}
	for _, change := range changes {
		f := base
		change(&f)
		key := usageSnapshotKey(f.AdminUsageFilter, f.Search, "dashboard")
		if seen[key] {
			t.Fatalf("collision %+v", f)
		}
		seen[key] = true
	}
	f := base
	v := from.In(time.FixedZone("UTC+8", 8*3600))
	f.From = &v
	f.Refresh = true
	f.SnapshotID = "page-version"
	if usageSnapshotKey(f.AdminUsageFilter, "", "dashboard") != usageSnapshotKey(base.AdminUsageFilter, "", "dashboard") {
		t.Fatal("equal instants or refresh changed cache key")
	}
}
func TestUsageAggregateSnapshotMetadata(t *testing.T) {
	source := &usageCacheSourceStub{}
	r := newCachedAdminUsageRepository(source)
	defer r.Close()
	for range 2 {
		v, err := r.AggregateAdminUsageSnapshot(context.Background(), domain.AdminUsageFilter{OrganizationID: 9}, "daily")
		if err != nil || v.Snapshot.Status != "ready" || len(v.Items) != 1 || v.Items[0].Requests != 9 {
			t.Fatalf("result=%+v error=%v", v, err)
		}
	}
	if source.calls.Load() != 1 {
		t.Fatalf("calls=%d", source.calls.Load())
	}
}
