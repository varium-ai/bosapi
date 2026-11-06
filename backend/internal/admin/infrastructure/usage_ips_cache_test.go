package infrastructure

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

type ipCacheSource struct {
	domain.AdminUsageRepository
	calls   atomic.Int32
	total   int64
	fail    bool
	release chan struct{}
	filter  domain.AdminUsageFilter
}

func (s *ipCacheSource) GroupAdminUsageByIP(ctx context.Context, f domain.AdminUsageFilter, page, size int) ([]domain.AdminUsageIP, int64, error) {
	s.calls.Add(1)
	s.filter = f
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	if s.fail {
		return nil, 0, errors.New("database unavailable")
	}
	return []domain.AdminUsageIP{{ClientIP: "192.0.2.1", Requests: f.OrganizationID}, {ClientIP: "2001:db8::1", Requests: f.APIKeyID}}, s.total, nil
}
func TestIPSnapshotPagesAndTenantIsolation(t *testing.T) {
	source := &ipCacheSource{total: 2}
	r := newCachedAdminUsageRepository(source)
	defer r.Close()
	ctx := context.Background()
	f := domain.AdminUsageFilter{APIKeyID: 149, OrganizationID: 1}
	first, err := r.GroupAdminUsageByIPSnapshot(ctx, f, 1, 1)
	if err != nil || first.Total != 2 || len(first.Items) != 1 || first.Items[0].Requests != 1 || first.Snapshot.Status != "ready" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if source.filter.From == nil || source.filter.To == nil || source.filter.To.Sub(*source.filter.From) != 30*time.Minute || time.Since(*source.filter.To) > time.Second {
		t.Fatalf("snapshot did not load a current 30-minute window: %+v", source.filter)
	}
	f.SnapshotID = first.Snapshot.ID
	first.Items[0].Requests = 999
	second, err := r.GroupAdminUsageByIPSnapshot(ctx, f, 2, 1)
	if err != nil || second.Items[0].Requests != 149 || second.Snapshot.ID != f.SnapshotID || source.calls.Load() != 1 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	again, _ := r.GroupAdminUsageByIPSnapshot(ctx, f, 1, 1)
	if again.Items[0].Requests != 1 {
		t.Fatal("caller mutated cache")
	}
	end, _ := r.GroupAdminUsageByIPSnapshot(ctx, f, 3, 1)
	if len(end.Items) != 0 || end.Total != 2 {
		t.Fatalf("end=%+v", end)
	}
	huge, err := r.GroupAdminUsageByIPSnapshot(ctx, f, int(^uint(0)>>1), 100)
	if err != nil || len(huge.Items) != 0 || huge.Total != 2 {
		t.Fatalf("huge page=%+v err=%v", huge, err)
	}
	f.SnapshotID = "old"
	replaced, _ := r.GroupAdminUsageByIPSnapshot(ctx, f, 2, 1)
	if replaced.Snapshot.Status != "replaced" || len(replaced.Items) != 0 {
		t.Fatalf("replaced=%+v", replaced)
	}
	f.SnapshotID = ""
	f.OrganizationID = 2
	other, err := r.GroupAdminUsageByIPSnapshot(ctx, f, 1, 1)
	if err != nil || other.Items[0].Requests != 2 || source.calls.Load() != 2 {
		t.Fatalf("other=%+v err=%v", other, err)
	}
}
func TestIPSnapshotFailureAndOversizeAreNotEmptySuccess(t *testing.T) {
	for _, source := range []*ipCacheSource{{fail: true}, {total: maxIPSnapshotRows + 1}} {
		r := newCachedAdminUsageRepository(source)
		result, err := r.GroupAdminUsageByIPSnapshot(context.Background(), domain.AdminUsageFilter{APIKeyID: 149}, 1, 20)
		r.Close()
		if err != nil || result.Snapshot.Status != "unavailable" || len(result.Items) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}
func TestIPSnapshotColdWaitIsBoundedAndLoadIsShared(t *testing.T) {
	source := &ipCacheSource{total: 2, release: make(chan struct{})}
	r := newCachedAdminUsageRepository(source)
	defer r.Close()
	f := domain.AdminUsageFilter{APIKeyID: 149}
	started := time.Now()
	result, err := r.GroupAdminUsageByIPSnapshot(context.Background(), f, 1, 20)
	if err != nil || result.Snapshot.Status != "generating" || time.Since(started) > 2*time.Second {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	close(source.release)
	result, err = r.GroupAdminUsageByIPSnapshot(context.Background(), f, 1, 20)
	if err != nil || result.Snapshot.Status != "ready" || source.calls.Load() != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, source.calls.Load())
	}
}
