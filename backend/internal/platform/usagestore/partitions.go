package usagestore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"bosapi/backend/internal/platform/usagetime"
	"gorm.io/gorm"
)

const usageLogPartitionLock = "SHA2(CONCAT(DATABASE(), ':usage_logs_v2:partitions'), 256)"

type usageLogPartition struct {
	Name        string `gorm:"column:partition_name"`
	Description string `gorm:"column:partition_description"`
}

var usageLogPartitionName = regexp.MustCompile(`^p[0-9]{8}$`)

func readUsageLogPartitions(db *gorm.DB) ([]usageLogPartition, error) {
	var partitions []usageLogPartition
	err := db.Raw(`SELECT PARTITION_NAME AS partition_name, PARTITION_DESCRIPTION AS partition_description FROM information_schema.PARTITIONS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'usage_logs_v2' AND PARTITION_NAME IS NOT NULL
		ORDER BY PARTITION_ORDINAL_POSITION`).Scan(&partitions).Error
	return partitions, err
}

// Partition maintenance never wraps DDL in a transaction. A session lock keeps
// replicas from reorganizing the same catch-all partition concurrently.
func Maintain(ctx context.Context, database *gorm.DB, now time.Time) (bool, error) {
	partitions, err := readUsageLogPartitions(database.WithContext(ctx))
	if err != nil || len(partitions) == 0 {
		return false, err
	}
	err = database.WithContext(ctx).Connection(func(conn *gorm.DB) (resultErr error) {
		// Keep a reusable base on this pinned connection. Otherwise GORM retains
		// a failed DDL's Error and skips even the deferred session cleanup SQL.
		conn = conn.Session(&gorm.Session{NewDB: true})
		var acquired int
		if err := conn.Raw("SELECT GET_LOCK(" + usageLogPartitionLock + ", 0)").Scan(&acquired).Error; err != nil {
			return err
		}
		if acquired != 1 {
			return nil
		}
		// Always release the advisory lock, including after a query deadline.
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var released int
			resultErr = errors.Join(resultErr, conn.WithContext(cleanupCtx).
				Raw("SELECT RELEASE_LOCK("+usageLogPartitionLock+")").Scan(&released).Error)
		}()
		var previousLockWait int
		if err := conn.Raw("SELECT @@SESSION.lock_wait_timeout").Scan(&previousLockWait).Error; err != nil {
			return err
		}
		if err := conn.Exec("SET SESSION lock_wait_timeout = 1").Error; err != nil {
			return err
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, conn.WithContext(cleanupCtx).
				Exec(fmt.Sprintf("SET SESSION lock_wait_timeout = %d", previousLockWait)).Error)
		}()
		// Re-read after acquiring the lock: another replica may have maintained it.
		partitions, err := readUsageLogPartitions(conn)
		if err != nil {
			return err
		}
		// Seal the boundary first. Shared gateway/mover locks make this wait for
		// existing inserts; subsequent backdated reports aggregate without raw rows.
		if err := conn.Transaction(func(tx *gorm.DB) error {
			return tx.Exec("UPDATE usage_log_retention SET retained_from = GREATEST(retained_from, ?) WHERE id = 1", usagetime.DayStart(now).AddDate(0, 0, -30)).Error
		}); err != nil {
			return err
		}
		return maintainUsageLogPartitionBounds(conn, partitions, now)
	})
	return true, err
}

func maintainUsageLogPartitionBounds(conn *gorm.DB, partitions []usageLogPartition, now time.Time) error {
	day := usagetime.DayStart(now)
	cutoff := day.AddDate(0, 0, -30)
	target := day.AddDate(0, 0, 8)
	var lastBoundary time.Time
	var expired []string
	future := false
	for _, partition := range partitions {
		if partition.Name == "p_future" && partition.Description == "MAXVALUE" {
			future = true
			continue
		}
		if partition.Name != "p_before" && !usageLogPartitionName.MatchString(partition.Name) {
			return fmt.Errorf("unexpected usage log partition %q", partition.Name)
		}
		boundary, err := time.Parse("2006-01-02 15:04:05", strings.Trim(partition.Description, "'"))
		if err != nil {
			return fmt.Errorf("invalid usage log partition boundary: %w", err)
		}
		if boundary.After(lastBoundary) {
			lastBoundary = boundary
		}
		if !boundary.After(cutoff) {
			expired = append(expired, "`"+partition.Name+"`")
		}
	}
	if !future || lastBoundary.IsZero() {
		return errors.New("usage log partition layout is incomplete")
	}
	// The catch-all preserves writes even after prolonged downtime. Bound the
	// number of DDLs; a later tick continues from the last committed boundary.
	var additions []string
	for added := 0; lastBoundary.Before(target) && added < 32; added++ {
		next := lastBoundary.AddDate(0, 0, 1)
		additions = append(additions, fmt.Sprintf("PARTITION p%s VALUES LESS THAN ('%s')", lastBoundary.In(usagetime.Location()).Format("20060102"), next.Format("2006-01-02 15:04:05")))
		lastBoundary = next
	}
	if len(additions) > 0 {
		// Split the catch-all once, even when several days need catching up.
		additions = append(additions, "PARTITION p_future VALUES LESS THAN (MAXVALUE)")
		if err := conn.Exec("ALTER TABLE usage_logs_v2 REORGANIZE PARTITION p_future INTO (" + strings.Join(additions, ", ") + ")").Error; err != nil {
			return err
		}
	}
	if len(expired) > 0 {
		// Acknowledged rows are already represented in durable daily summaries.
		// The retention fence prevents new pending inserts into these partitions.
		var pending bool
		if err := conn.Raw("SELECT EXISTS(SELECT 1 FROM usage_logs_v2 PARTITION (" + strings.Join(expired, ", ") + ") WHERE rollup_applied = 0 LIMIT 1)").Scan(&pending).Error; err != nil {
			return err
		}
		if pending {
			return nil
		}
		return conn.Exec("ALTER TABLE usage_logs_v2 DROP PARTITION " + strings.Join(expired, ", ")).Error
	}
	return nil
}
