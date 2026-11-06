package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/snapshot"
	"github.com/redis/go-redis/v9"
)

const maxIPSnapshotRows = 10000
const ipUsageCacheTTL = 15 * time.Minute

func newIPUsageCache(client redis.UniversalClient) *snapshot.Cache {
	return snapshot.New(client, "bos:ip-usage:v3:", ipUsageCacheTTL, ipUsageCacheTTL, 64)
}

// All pages share one immutable aggregate. Unlike usage rollups, IP counts are
// read directly from both physical tables and do not depend on rollup progress.
func (r *CachedAdminUsageRepository) GroupAdminUsageByIPSnapshot(ctx context.Context, filter domain.AdminUsageFilter, page, size int) (domain.AdminUsageIPSnapshot, error) {
	window, valid := domain.IPUsageWindow(filter.IPWindowMinutes)
	if !valid || filter.APIKeyID == 0 || page < 1 || size < 1 || size > 100 {
		return domain.AdminUsageIPSnapshot{}, errors.New("invalid IP snapshot pagination")
	}
	filter.IPWindowMinutes = int(window / time.Minute)
	// Keep the rolling clock out of the cache key so pages share a snapshot.
	// Recompute the window on every background load, not when capturing the
	// callback. The dedicated cache isolates the 15-minute TTL from other stats.
	v, err := r.ipCache.Get(ctx, usageSnapshotKey(filter, "", "ips-presets-v3"), filter.Refresh, func(ctx context.Context) (json.RawMessage, time.Time, error) {
		bounded := recentIPUsageFilter(filter, time.Now())
		items, total, err := r.AdminUsageRepository.GroupAdminUsageByIP(ctx, bounded, 1, maxIPSnapshotRows)
		if err != nil {
			return nil, time.Time{}, err
		}
		if total > maxIPSnapshotRows {
			return nil, time.Time{}, errors.New("IP snapshot exceeds row limit; narrow the date range")
		}
		data, err := json.Marshal(domain.AdminUsageIPSnapshot{Items: items, Total: total})
		return data, time.Time{}, err
	})
	result := domain.AdminUsageIPSnapshot{Items: []domain.AdminUsageIP{}, Snapshot: usageSnapshotMetadata(v)}
	if errors.Is(err, snapshot.ErrPending) || errors.Is(err, snapshot.ErrExpired) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if filter.SnapshotID != "" && filter.SnapshotID != v.ID {
		result.Snapshot.Status = "replaced"
		return result, nil
	}
	if err := json.Unmarshal(v.Data, &result); err != nil {
		return result, err
	}
	result.Snapshot = usageSnapshotMetadata(v)
	// Bound before multiplying so an arbitrary HTTP page cannot overflow into
	// a negative slice offset. Out-of-range pages retain the correct total.
	start := int64(min(page-1, len(result.Items))) * int64(size)
	if start >= int64(len(result.Items)) {
		result.Items = []domain.AdminUsageIP{}
	} else {
		result.Items = slices.Clone(result.Items[start:min(start+int64(size), int64(len(result.Items)))])
	}
	return result, nil
}
