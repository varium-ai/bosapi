package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

const requestLogPartitionLock = "SHA2(CONCAT(DATABASE(), ':request_logs_v2:partitions'), 256)"

type requestLogPartition struct {
	Name        string `gorm:"column:partition_name"`
	Description string `gorm:"column:partition_description"`
}

var requestLogPartitionName = regexp.MustCompile(`^p[0-9]{8}$`)

func readRequestLogPartitions(db *gorm.DB) ([]requestLogPartition, error) {
	var partitions []requestLogPartition
	err := db.Raw(`SELECT PARTITION_NAME AS partition_name, PARTITION_DESCRIPTION AS partition_description FROM information_schema.PARTITIONS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'request_logs_v2' AND PARTITION_NAME IS NOT NULL
		ORDER BY PARTITION_ORDINAL_POSITION`).Scan(&partitions).Error
	return partitions, err
}

// Partition maintenance never wraps DDL in a transaction. A session lock keeps
// replicas from reorganizing the same catch-all partition concurrently.
func (repository *RequestLogRepository) maintainRequestLogPartitions(ctx context.Context, now time.Time) (bool, error) {
	partitions, err := readRequestLogPartitions(repository.database.WithContext(ctx))
	if err != nil || len(partitions) == 0 {
		return false, err
	}
	err = repository.database.WithContext(ctx).Connection(func(conn *gorm.DB) (resultErr error) {
		// Keep a reusable base on this pinned connection. Otherwise GORM retains
		// a failed DDL's Error and skips even the deferred session cleanup SQL.
		conn = conn.Session(&gorm.Session{NewDB: true})
		var acquired int
		if err := conn.Raw("SELECT GET_LOCK(" + requestLogPartitionLock + ", 0)").Scan(&acquired).Error; err != nil {
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
				Raw("SELECT RELEASE_LOCK("+requestLogPartitionLock+")").Scan(&released).Error)
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
		partitions, err := readRequestLogPartitions(conn)
		if err != nil {
			return err
		}
		return maintainRequestLogPartitionBounds(conn, partitions, now)
	})
	return true, err
}

func maintainRequestLogPartitionBounds(conn *gorm.DB, partitions []requestLogPartition, now time.Time) error {
	day := now.UTC().Truncate(24 * time.Hour)
	cutoff := day.Add(-requestLogRetentionPeriod)
	target := day.AddDate(0, 0, 8)
	var lastBoundary time.Time
	var expired []string
	future := false
	for _, partition := range partitions {
		if partition.Name == "p_future" && partition.Description == "MAXVALUE" {
			future = true
			continue
		}
		if partition.Name != "p_before" && !requestLogPartitionName.MatchString(partition.Name) {
			return fmt.Errorf("unexpected request log partition %q", partition.Name)
		}
		boundary, err := time.Parse("2006-01-02", strings.Trim(partition.Description, "'"))
		if err != nil {
			return fmt.Errorf("invalid request log partition boundary: %w", err)
		}
		if boundary.After(lastBoundary) {
			lastBoundary = boundary
		}
		if !boundary.After(cutoff) {
			expired = append(expired, "`"+partition.Name+"`")
		}
	}
	if !future || lastBoundary.IsZero() {
		return errors.New("request log partition layout is incomplete")
	}
	// The catch-all preserves writes even after prolonged downtime. Bound the
	// number of DDLs; a later tick continues from the last committed boundary.
	var additions []string
	for added := 0; lastBoundary.Before(target) && added < 32; added++ {
		next := lastBoundary.AddDate(0, 0, 1)
		additions = append(additions, fmt.Sprintf("PARTITION p%s VALUES LESS THAN ('%s')", lastBoundary.Format("20060102"), next.Format("2006-01-02")))
		lastBoundary = next
	}
	if len(additions) > 0 {
		// Split the catch-all once, even when several days need catching up.
		additions = append(additions, "PARTITION p_future VALUES LESS THAN (MAXVALUE)")
		if err := conn.Exec("ALTER TABLE request_logs_v2 REORGANIZE PARTITION p_future INTO (" + strings.Join(additions, ", ") + ")").Error; err != nil {
			return err
		}
	}
	if len(expired) > 0 {
		return conn.Exec("ALTER TABLE request_logs_v2 DROP PARTITION " + strings.Join(expired, ", ")).Error
	}
	return nil
}
