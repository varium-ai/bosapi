package infrastructure

import (
	"errors"
	"time"

	"bosapi/backend/internal/admin/domain"
	"gorm.io/gorm"
)

// Clamp every IP aggregation to its selected rolling window (at most 24 hours), including callers
// that omit dates or explicitly request older history. Disjoint ranges stay
// empty (From >= To); never widen them into a different reporting period.
func recentIPUsageFilter(filter domain.AdminUsageFilter, now time.Time) domain.AdminUsageFilter {
	to := now.UTC()
	window, valid := domain.IPUsageWindow(filter.IPWindowMinutes)
	if !valid {
		window = 24 * time.Hour
	}
	from := to.Add(-window)
	if filter.From == nil || filter.From.Before(from) {
		filter.From = &from
	}
	if filter.To == nil || filter.To.After(to) {
		filter.To = &to
	}
	return filter
}

// Aggregate within each physical table so the UNION materializes IP groups,
// never full request rows. Do not limit branches: that would undercount shared IPs.
func adminUsageIPGroups(db *gorm.DB, filter domain.AdminUsageFilter) *gorm.DB {
	branch := func(table string, security bool) *gorm.DB {
		// The all-history IP covering index puts time last. Use the existing
		// Key/time range index so old requests are not scanned for this window.
		index := "idx_usage_key_time"
		if security {
			index = "idx_security_key_time"
		}
		q := db.Table(table+" FORCE INDEX ("+index+")").Where("api_key_id = ? AND client_ip IS NOT NULL AND LENGTH(client_ip) > 0", filter.APIKeyID)
		if filter.OrganizationID > 0 {
			q = q.Where("organization_id = ?", filter.OrganizationID)
		}
		if filter.From != nil {
			q = q.Where("created_at >= ?", *filter.From)
		}
		if filter.To != nil {
			q = q.Where("created_at < ?", *filter.To)
		}
		if filter.ClientIP != "" {
			q = q.Where("client_ip = INET6_ATON(?)", filter.ClientIP)
		}
		counts := "COUNT(*) AS requests, 0 AS security_events"
		if security {
			counts = "0 AS requests, COUNT(*) AS security_events"
		}
		return q.Select("client_ip, " + counts + ", MAX(created_at) AS last_seen").Group("client_ip")
	}
	return db.Table("(? UNION ALL ? UNION ALL ?) AS ip_groups", branch("usage_logs", false), branch("usage_logs_v2", false), branch("security_events", true)).
		Select("INET6_NTOA(client_ip) AS client_ip, SUM(requests) AS requests, SUM(security_events) AS security_events, MAX(last_seen) AS last_seen, COUNT(*) OVER () AS total").
		Group("client_ip")
}

func groupAdminUsageByIP(db *gorm.DB, filter domain.AdminUsageFilter, page, size int) ([]domain.AdminUsageIP, int64, error) {
	if _, valid := domain.IPUsageWindow(filter.IPWindowMinutes); !valid {
		return nil, 0, errors.New("invalid IP usage window")
	}
	filter = recentIPUsageFilter(filter, time.Now())
	var rows []struct {
		domain.AdminUsageIP
		Total int64
	}
	query := adminUsageIPGroups(db, filter)
	if err := query.Order("last_seen DESC, client_ip ASC").Limit(size).Offset((page - 1) * size).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]domain.AdminUsageIP, 0, len(rows))
	var total int64
	for _, row := range rows {
		items = append(items, row.AdminUsageIP)
		total = row.Total
	}
	// Keep the counted contract for legacy callers requesting an empty later page.
	// The snapshot loader always reads page one, so it never takes this path.
	if len(rows) == 0 && page > 1 {
		if err := db.Table("(?) AS ips", adminUsageIPGroups(db, filter)).Count(&total).Error; err != nil {
			return nil, 0, err
		}
	}
	return items, total, nil
}
