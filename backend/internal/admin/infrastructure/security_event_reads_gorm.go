package infrastructure

import (
	"context"
	"database/sql"

	"gorm.io/gorm"

	"bosapi/backend/internal/admin/domain"
)

type securityEventReadRecord struct {
	UserID         uint64 `gorm:"primaryKey;autoIncrement:false"`
	OrganizationID uint64 `gorm:"primaryKey;autoIncrement:false"`
	EventID        uint64 `gorm:"primaryKey;autoIncrement:false"`
}

func (securityEventReadRecord) TableName() string { return "security_event_reads" }

func (repository *SecurityEventRepository) notificationEvents(ctx context.Context, scope domain.SecurityEventReadScope) *gorm.DB {
	query := repository.database.WithContext(ctx).Model(&securityEventRecord{}).
		Where("severity IN ?", securitySeveritiesAtLeast("warning"))
	if scope.OrganizationID > 0 {
		query = query.Where("organization_id = ?", scope.OrganizationID)
	}
	return query
}

// CountUnreadSecurityEvents includes every unacknowledged warning, even when IDs commit out of order.
func (repository *SecurityEventRepository) CountUnreadSecurityEvents(ctx context.Context, scope domain.SecurityEventReadScope) (int64, error) {
	if repository.unreadCache != nil {
		return repository.unreadCache.get(ctx, scope, func(ctx context.Context) (int64, error) {
			return repository.countUnreadSecurityEvents(ctx, scope)
		})
	}
	return repository.countUnreadSecurityEvents(ctx, scope)
}

func (repository *SecurityEventRepository) countUnreadSecurityEvents(ctx context.Context, scope domain.SecurityEventReadScope) (int64, error) {
	receipts := repository.database.WithContext(ctx).Model(&securityEventReadRecord{}).Select("1").
		Where("user_id = ? AND organization_id = ? AND event_id = security_events.id", scope.UserID, scope.OrganizationID)
	var count int64
	err := repository.notificationEvents(ctx, scope).
		Where("NOT EXISTS (?)", receipts).Count(&count).Error
	return count, err
}

const acknowledgeGlobalSecurityEvents = `INSERT INTO security_event_reads (user_id, organization_id, event_id)
 SELECT ?, ?, events.id FROM security_events AS events
 LEFT JOIN security_event_reads AS acknowledged
 ON acknowledged.user_id = ? AND acknowledged.organization_id = ? AND acknowledged.event_id = events.id
 WHERE events.severity IN ? AND acknowledged.event_id IS NULL
 ON DUPLICATE KEY UPDATE event_id = security_event_reads.event_id`

const acknowledgeOrganizationSecurityEvents = `INSERT INTO security_event_reads (user_id, organization_id, event_id)
 SELECT ?, ?, events.id FROM security_events AS events
 LEFT JOIN security_event_reads AS acknowledged
 ON acknowledged.user_id = ? AND acknowledged.organization_id = ? AND acknowledged.event_id = events.id
 WHERE events.severity IN ? AND events.organization_id = ? AND acknowledged.event_id IS NULL
 ON DUPLICATE KEY UPDATE event_id = security_event_reads.event_id`

// MarkSecurityEventsRead atomically acknowledges visible warnings without relying on commit order.
func (repository *SecurityEventRepository) MarkSecurityEventsRead(ctx context.Context, scope domain.SecurityEventReadScope) error {
	// GORM cannot express INSERT SELECT through Create. A single parameterized
	// statement captures exactly the visible events and remains idempotent.
	// READ COMMITTED makes MySQL read the source using a consistent snapshot,
	// instead of waiting for uncommitted rows and acknowledging them afterwards.
	err := repository.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		levels := securitySeveritiesAtLeast("warning")
		if scope.OrganizationID > 0 {
			return transaction.Exec(acknowledgeOrganizationSecurityEvents, scope.UserID, scope.OrganizationID, scope.UserID, scope.OrganizationID, levels, scope.OrganizationID).Error
		}
		return transaction.Exec(acknowledgeGlobalSecurityEvents, scope.UserID, scope.OrganizationID, scope.UserID, scope.OrganizationID, levels).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err == nil {
		repository.unreadCache.invalidate(scope)
	}
	return err
}

var _ domain.SecurityEventReadRepository = (*SecurityEventRepository)(nil)
