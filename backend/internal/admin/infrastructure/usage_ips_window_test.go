package infrastructure

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
)

func TestRecentIPUsageWindow(t *testing.T) {
	now := time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC)
	floor := now.Add(-30 * time.Minute)
	before := floor.Add(-time.Hour)
	recent := now.Add(-10 * time.Minute)
	future := now.Add(time.Hour)
	for _, tc := range []struct {
		name             string
		from, to         *time.Time
		wantFrom, wantTo time.Time
	}{
		{"omitted", nil, nil, floor, now},
		{"wide", &before, &future, floor, now},
		{"narrow", &recent, &now, recent, now},
		{"past stays empty", &before, &floor, floor, floor},
		{"future stays empty", &future, nil, future, now},
		{"from only", &recent, nil, recent, now},
		{"to only", nil, &recent, floor, recent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := domain.AdminUsageFilter{APIKeyID: 149, OrganizationID: 7, ClientIP: "192.0.2.1", From: tc.from, To: tc.to}
			got := recentIPUsageFilter(f, now)
			if !got.From.Equal(tc.wantFrom) || !got.To.Equal(tc.wantTo) || got.APIKeyID != 149 || got.OrganizationID != 7 || got.ClientIP != f.ClientIP {
				t.Fatalf("got=%+v", got)
			}
			if f.From != tc.from || f.To != tc.to {
				t.Fatal("modified caller filter")
			}
		})
	}
	// Rolling windows cross midnight; they are not calendar-day boundaries.
	if !recentIPUsageFilter(domain.AdminUsageFilter{}, now.In(time.FixedZone("UTC+8", 8*3600))).From.Equal(floor) {
		t.Fatal("timezone changed rolling window")
	}
}

func TestIPSnapshotDoesNotReuseAllHistoryCache(t *testing.T) {
	source := &ipCacheSource{total: 2}
	r := newCachedAdminUsageRepository(source)
	defer r.Close()
	f := domain.AdminUsageFilter{APIKeyID: 149}
	_, err := r.cache.Get(context.Background(), usageSnapshotKey(f, "", "ips-v1"), false, func(context.Context) (json.RawMessage, time.Time, error) {
		return json.RawMessage(`{"items":[{"client_ip":"192.0.2.99","requests":999}],"total":1}`), time.Time{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.GroupAdminUsageByIPSnapshot(context.Background(), f, 1, 20)
	if err != nil || result.Total != 2 || source.calls.Load() != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestIPWindowPresetsAndFifteenMinuteCache(t *testing.T) {
	now := time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC)
	source := &ipCacheSource{total: 2}
	r := newCachedAdminUsageRepository(source)
	defer r.Close()
	for i, minutes := range []int{30, 60, 360, 720, 1440} {
		f := domain.AdminUsageFilter{APIKeyID: 149, IPWindowMinutes: minutes}
		bounded := recentIPUsageFilter(f, now)
		if bounded.To.Sub(*bounded.From) != time.Duration(minutes)*time.Minute {
			t.Fatalf("minutes=%d filter=%+v", minutes, bounded)
		}
		result, err := r.GroupAdminUsageByIPSnapshot(context.Background(), f, 1, 20)
		if err != nil || result.Snapshot.Status != "ready" || result.Snapshot.ValidUntil.Sub(result.Snapshot.GeneratedAt) != 15*time.Minute {
			t.Fatalf("minutes=%d result=%+v err=%v", minutes, result, err)
		}
		if source.filter.To.Sub(*source.filter.From) != time.Duration(minutes)*time.Minute || source.calls.Load() != int32(i+1) {
			t.Fatalf("wrong scope/calls for %d: %+v", minutes, source.filter)
		}
		repeated, err := r.GroupAdminUsageByIPSnapshot(context.Background(), f, 1, 20)
		if err != nil || repeated.Snapshot.ID != result.Snapshot.ID || source.calls.Load() != int32(i+1) {
			t.Fatalf("cache not reused for %d", minutes)
		}
	}
	// The omitted range and explicit 30-minute range share the same cache entry.
	_, err := r.GroupAdminUsageByIPSnapshot(context.Background(), domain.AdminUsageFilter{APIKeyID: 149}, 1, 20)
	if err != nil || source.calls.Load() != 5 {
		t.Fatalf("default range missed cache: %v", err)
	}
	for _, minutes := range []int{-1, 1, 29, 31, 1441, 2880} {
		if _, valid := domain.IPUsageWindow(minutes); valid {
			t.Fatalf("accepted %d", minutes)
		}
		if _, err := r.GroupAdminUsageByIPSnapshot(context.Background(), domain.AdminUsageFilter{APIKeyID: 149, IPWindowMinutes: minutes}, 1, 20); err == nil {
			t.Fatalf("accepted invalid range %d", minutes)
		}
	}
}
