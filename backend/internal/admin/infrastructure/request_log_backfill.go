package infrastructure

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"bosapi/backend/internal/platform/logging"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	requestLogBackfillBatchSize = 200
	requestLogBackfillPause     = 500 * time.Millisecond
	requestLogBackfillTimeout   = 2 * time.Second
)

type requestLogBackfillState struct {
	FromAt, UntilAt, CursorAt time.Time
	CursorID, CopiedRows      uint64
	CompletedAt               *time.Time
	Waiting                   bool
}

// Copy one bounded page and its checkpoint atomically. No source row is changed
// or locked by the reader. The singleton state lock serializes replicas without
// making a busy replica wait. legacy_id makes retries safe even after a rewind.
func requestLogBackfillBatch(ctx context.Context, database *gorm.DB, now time.Time) (bool, error) {
	done := false
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var states []requestLogBackfillState
		if err := tx.Raw(`SELECT from_at, until_at, cursor_at, cursor_id, copied_rows, completed_at,
			COALESCE(next_batch_at > UTC_TIMESTAMP(6), 0) AS waiting
			FROM request_log_backfill WHERE id = 1 FOR UPDATE SKIP LOCKED`).Scan(&states).Error; err != nil {
			return err
		}
		if len(states) == 0 {
			return nil
		}
		state := states[0]
		if state.CompletedAt != nil {
			done = true
			return nil
		}
		if state.Waiting {
			return nil
		}
		from := state.FromAt
		if retentionCutoff := now.UTC().Add(-requestLogRetentionPeriod); retentionCutoff.After(from) {
			from = retentionCutoff
		}
		var records []requestLogRecord
		if err := tx.Raw(`SELECT id, created_at, level, event, message, request_id, client_ip, organization_id, api_key_masked
			FROM request_logs FORCE INDEX (idx_request_logs_created_at)
			WHERE created_at >= ? AND created_at <= ? AND (created_at > ? OR (created_at = ? AND id > ?))
			ORDER BY created_at, id LIMIT ?`, from, state.UntilAt, state.CursorAt, state.CursorAt, state.CursorID, requestLogBackfillBatchSize).Scan(&records).Error; err != nil {
			return err
		}
		if len(records) == 0 {
			if err := tx.Exec("UPDATE request_log_backfill SET completed_at = ? WHERE id = 1", now.UTC()).Error; err != nil {
				return err
			}
			done = true
			return nil
		}
		last := records[len(records)-1]
		for i := range records {
			legacyID := records[i].ID
			records[i].LegacyID, records[i].ID = &legacyID, 0
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "legacy_id"}, {Name: "created_at"}}, DoNothing: true,
		}).Create(&records).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE request_log_backfill SET cursor_at = ?, cursor_id = ?, copied_rows = copied_rows + ?,
			next_batch_at = UTC_TIMESTAMP(6) + INTERVAL 500000 MICROSECOND WHERE id = 1`,
			last.CreatedAt, last.ID, len(records)).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, err
	}
	return done, nil
}

// StartRequestLogBackfill runs independently of the log writer and HTTP startup.
// It preserves the old table, migrates only the fixed 24-hour window recorded by
// 021, and stops on completion. Shutdown cancels and joins the current batch.
func StartRequestLogBackfill(database *gorm.DB) func() {
	return startRequestLogBackfill(func(ctx context.Context) (bool, error) {
		return requestLogBackfillBatch(ctx, database, time.Now().UTC())
	})
}

func startRequestLogBackfill(batch func(context.Context) (bool, error)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Let normal application startup and in-flight legacy inserts settle.
		delay := 5 * time.Second
		for {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			batchCtx, cancelBatch := context.WithTimeout(ctx, requestLogBackfillTimeout)
			complete, err := batch(batchCtx)
			cancelBatch()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				logging.Printf(ctx, "ERROR", "request_logs.backfill.failed", "err=%v", err)
				delay = 5 * time.Second
				continue
			}
			if complete {
				logging.Event(ctx, "INFO", "request_logs.backfill.completed", "recent legacy logs copied; original table preserved")
				return
			}
			delay = requestLogBackfillPause
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}
