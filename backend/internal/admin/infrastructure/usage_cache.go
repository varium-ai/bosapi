package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/geoip"
	"bosapi/backend/internal/platform/snapshot"
	"bosapi/backend/internal/platform/usagetime"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const adminUsageCacheTTL = 30 * time.Minute
const maxDashboardRows = 10000

type CachedAdminUsageRepository struct {
	domain.AdminUsageRepository
	cache   *snapshot.Cache
	ipCache *snapshot.Cache
}

func NewCachedAdminUsageRepository(database *gorm.DB, resolver ...*geoip.Resolver) *CachedAdminUsageRepository {
	return NewSharedAdminUsageRepository(database, nil, resolver...)
}
func NewSharedAdminUsageRepository(database *gorm.DB, client redis.UniversalClient, resolver ...*geoip.Resolver) *CachedAdminUsageRepository {
	source := NewPreaggregatedAdminUsageRepository(database, resolver...)
	source.materializedOnly = true
	return &CachedAdminUsageRepository{AdminUsageRepository: source, cache: snapshot.New(client, "bos:admin-usage:v2:", 20*time.Minute, adminUsageCacheTTL, 64), ipCache: newIPUsageCache(client)}
}

// NewSharedPortalUsageRepository retains exact rollups plus pending usage while
// bounding repeated portal display queries independently of quota authorization.
func NewSharedPortalUsageRepository(database *gorm.DB, client redis.UniversalClient, resolver ...*geoip.Resolver) *CachedAdminUsageRepository {
	source := NewPreaggregatedAdminUsageRepository(database, resolver...)
	return &CachedAdminUsageRepository{
		AdminUsageRepository: source,
		cache:                snapshot.New(client, "bos:key-portal-usage:v1:", time.Minute, 5*time.Minute, 64),
		ipCache:              newIPUsageCache(client),
	}
}
func newCachedAdminUsageRepository(source domain.AdminUsageRepository) *CachedAdminUsageRepository {
	return &CachedAdminUsageRepository{AdminUsageRepository: source, cache: snapshot.New(nil, "bos:admin-usage:test:", 20*time.Minute, adminUsageCacheTTL, 64), ipCache: newIPUsageCache(nil)}
}
func (r *CachedAdminUsageRepository) Close() { r.cache.Close(); r.ipCache.Close() }

// WarmDefault is called on startup and every minute to cover business-day rollover.
func (r *CachedAdminUsageRepository) WarmDefault(ctx context.Context) {
	from := usagetime.DayStart(time.Now()).UTC()
	to := from.Add(24 * time.Hour)
	_, _ = r.DailyAdminOrganizationUsage(ctx, domain.AdminOrganizationUsageFilter{AdminUsageFilter: domain.AdminUsageFilter{From: &from, To: &to}}, 1, 20)
}
func usageSnapshotKey(filter domain.AdminUsageFilter, search, period string) string {
	filter.Refresh = false
	filter.SkipTotal = false
	filter.Before = nil
	filter.BeforeID = 0
	filter.SnapshotID = ""
	if filter.From != nil {
		v := filter.From.UTC()
		filter.From = &v
	}
	if filter.To != nil {
		v := filter.To.UTC()
		filter.To = &v
	}
	return snapshot.Key(struct {
		Filter         domain.AdminUsageFilter
		Search, Period string
	}{filter, search, period})
}
func usageSnapshotMetadata(v snapshot.Value) *domain.UsageSnapshotMetadata {
	return &domain.UsageSnapshotMetadata{ID: v.ID, GeneratedAt: v.GeneratedAt, ValidUntil: v.ValidUntil, Status: v.Status}
}
func (r *CachedAdminUsageRepository) load(ctx context.Context, filter domain.AdminUsageFilter, load func(domain.AdminUsageRepository) (any, error)) (json.RawMessage, time.Time, error) {
	if source, ok := r.AdminUsageRepository.(*AdminUsageRepository); ok {
		return source.loadUsageSnapshot(ctx, filter, load)
	}
	value, err := load(r.AdminUsageRepository)
	if err != nil {
		return nil, time.Time{}, err
	}
	data, err := json.Marshal(value)
	return data, time.Time{}, err
}
func (r *CachedAdminUsageRepository) DailyAdminOrganizationUsage(ctx context.Context, filter domain.AdminOrganizationUsageFilter, page, size int) (domain.AdminOrganizationUsageDashboard, error) {
	if page < 1 || size < 1 || size > 100 {
		return domain.AdminOrganizationUsageDashboard{}, errors.New("invalid snapshot pagination")
	}
	v, err := r.cache.Get(ctx, usageSnapshotKey(filter.AdminUsageFilter, filter.Search, "dashboard"), filter.Refresh, func(ctx context.Context) (json.RawMessage, time.Time, error) {
		return r.load(ctx, filter.AdminUsageFilter, func(source domain.AdminUsageRepository) (any, error) {
			result, err := source.DailyAdminOrganizationUsage(ctx, filter, 1, maxDashboardRows)
			if err == nil && result.Total > maxDashboardRows {
				err = errors.New("dashboard exceeds snapshot row limit; narrow the date range")
			}
			return result, err
		})
	})
	result := domain.AdminOrganizationUsageDashboard{Items: []domain.AdminOrganizationUsagePoint{}, Snapshot: usageSnapshotMetadata(v)}
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
	if err = json.Unmarshal(v.Data, &result); err != nil {
		return result, err
	}
	result.Snapshot = usageSnapshotMetadata(v)
	start := int64(page-1) * int64(size)
	if start >= int64(len(result.Items)) {
		result.Items = []domain.AdminOrganizationUsagePoint{}
	} else {
		end := min(start+int64(size), int64(len(result.Items)))
		result.Items = slices.Clone(result.Items[start:end])
	}
	return result, nil
}
func (r *CachedAdminUsageRepository) AggregateAdminUsageSnapshot(ctx context.Context, filter domain.AdminUsageFilter, period string) (domain.AdminUsageAggregateSnapshot, error) {
	v, err := r.cache.Get(ctx, usageSnapshotKey(filter, "", period), filter.Refresh, func(ctx context.Context) (json.RawMessage, time.Time, error) {
		return r.load(ctx, filter, func(source domain.AdminUsageRepository) (any, error) {
			return source.AggregateAdminUsage(ctx, filter, period)
		})
	})
	result := domain.AdminUsageAggregateSnapshot{Items: []domain.AdminUsagePoint{}, Snapshot: usageSnapshotMetadata(v)}
	if errors.Is(err, snapshot.ErrPending) || errors.Is(err, snapshot.ErrExpired) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(v.Data, &result.Items)
	return result, err
}
func (r *CachedAdminUsageRepository) AggregateAdminUsage(ctx context.Context, filter domain.AdminUsageFilter, period string) ([]domain.AdminUsagePoint, error) {
	result, err := r.AggregateAdminUsageSnapshot(ctx, filter, period)
	return result.Items, err
}
