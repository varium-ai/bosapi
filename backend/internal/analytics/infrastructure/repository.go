package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"bosapi/backend/internal/analytics/application"
	"bosapi/backend/internal/analytics/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository {
	// GORM's error logger can interpolate SQL arguments, including frozen payloads.
	return &Repository{db: db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})}
}

func snapshot(tx *gorm.DB, lock bool) (domain.Snapshot, error) {
	var row struct {
		domain.State
		SettingJSON string
		Configured  bool
	}
	query := "SELECT s.*, COALESCE(t.value_text, '') AS setting_json, (t.setting_key IS NOT NULL) AS configured FROM usage_reporting_state s LEFT JOIN settings t ON t.setting_key = ? WHERE s.id = 1"
	if lock {
		query += " FOR UPDATE"
	}
	result := tx.Raw(query, domain.SettingKey).Scan(&row)
	if result.Error != nil {
		return domain.Snapshot{}, result.Error
	}
	if result.RowsAffected != 1 {
		return domain.Snapshot{}, domain.ErrUnavailable
	}
	enabled, err := domain.DecodeEnabled(row.SettingJSON)
	if err != nil {
		return domain.Snapshot{State: row.State, Configured: row.Configured}, err
	}
	if enabled && (row.InstallationID == "" || row.CollectionEpoch == "" || row.EnabledSince == nil || row.ScanCursor == nil) {
		return domain.Snapshot{State: row.State, Configured: row.Configured}, domain.ErrUnavailable
	}
	return domain.Snapshot{State: row.State, Enabled: enabled, Configured: row.Configured}, nil
}

func (r *Repository) Snapshot(ctx context.Context) (domain.Snapshot, error) {
	return snapshot(r.db.WithContext(ctx), false)
}

func dbNow(tx *gorm.DB) (time.Time, error) {
	var row struct{ Now time.Time }
	err := tx.Raw("SELECT UTC_TIMESTAMP(6) AS now").Scan(&row).Error
	return row.Now.UTC(), err
}

func audit(tx *gorm.DB, action string, actor *uint64, now time.Time) error {
	return tx.Exec("INSERT INTO usage_reporting_events (action, actor_id, created_at) VALUES (?, ?, ?)", action, actor, now).Error
}

func cancelPending(tx *gorm.DB) error {
	return tx.Model(&domain.Job{}).Where("payload IS NOT NULL OR status IN ('pending', 'sending')").Updates(map[string]any{"payload": nil, "status": "cancelled", "lease_token": "", "lease_until": nil}).Error
}

// InitializeDefault shares Toggle's state lock so replicas and explicit choices
// cannot overwrite one another. An existing setting always takes precedence.
func (r *Repository) InitializeDefault(ctx context.Context, initial domain.Report) error {
	_, err := r.configure(ctx, true, nil, initial, true)
	return err
}

func (r *Repository) Toggle(ctx context.Context, enabled bool, actor *uint64, initial domain.Report) (domain.Snapshot, error) {
	return r.configure(ctx, enabled, actor, initial, false)
}

func (r *Repository) configure(ctx context.Context, enabled bool, actor *uint64, initial domain.Report, onlyIfUnset bool) (domain.Snapshot, error) {
	var result domain.Snapshot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, snapshotErr := snapshot(tx, true)
		if onlyIfUnset && current.Configured {
			result = current
			return snapshotErr
		}
		// A disable can repair corrupt configuration, provided the state table exists.
		if snapshotErr != nil && (enabled || current.ID != 1) {
			return snapshotErr
		}
		now, err := dbNow(tx)
		if err != nil {
			return err
		}
		if current.Enabled == enabled && current.Configured && snapshotErr == nil {
			// Repeated disable must still clear any abandoned bodies.
			if !enabled {
				if err := cancelPending(tx); err != nil {
					return err
				}
			}
			result = current
			return nil
		}
		if err := cancelPending(tx); err != nil {
			return err
		}
		updates := map[string]any{"config_revision": current.ConfigRevision + 1, "schedule_token": "", "schedule_until": nil, "last_error": "", "next_scan_at": nil}
		if enabled {
			if current.InstallationID == "" {
				current.InstallationID = initial.InstallationID
			}
			current.CollectionEpoch, current.EnabledSince = initial.CollectionEpoch, &now
			cursor := domain.DayStart(now)
			next := application.NextSchedule(cursor.AddDate(0, 0, 1), current.InstallationID)
			updates["installation_id"], updates["collection_epoch"] = current.InstallationID, current.CollectionEpoch
			updates["enabled_since"], updates["scan_cursor"], updates["next_scan_at"] = now, cursor, next
			initial.InstallationID, initial.CollectionEpoch, initial.GeneratedAt = current.InstallationID, current.CollectionEpoch, now
			if err := enqueue(tx, initial, now); err != nil {
				return err
			}
		}
		if err := tx.Model(&domain.State{}).Where("id = 1").Updates(updates).Error; err != nil {
			return err
		}
		setting, err := json.Marshal(struct {
			Enabled   bool      `json:"enabled"`
			UpdatedAt time.Time `json:"updated_at"`
		}{enabled, now})
		if err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO settings (setting_key, value_text, is_secret, updated_by, updated_at) VALUES (?, ?, false, ?, ?) ON DUPLICATE KEY UPDATE value_text = VALUES(value_text), is_secret = false, updated_by = VALUES(updated_by), updated_at = VALUES(updated_at)", domain.SettingKey, string(setting), actor, now).Error; err != nil {
			return err
		}
		action := "disabled"
		if enabled {
			action = "enabled"
		}
		if err := audit(tx, action, actor, now); err != nil {
			return err
		}
		result, err = snapshot(tx, false)
		return err
	})
	return result, err
}

func (r *Repository) Reset(ctx context.Context, actor *uint64, _ time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := snapshot(tx, true)
		if err != nil {
			return err
		}
		if current.Enabled {
			return domain.ErrConflict
		}
		if err := cancelPending(tx); err != nil {
			return err
		}
		// Never-enabled deployments remain without an identity until first enable.
		identity := ""
		if current.InstallationID != "" {
			identity = domain.UUID()
		}
		if err := tx.Model(&domain.State{}).Where("id = 1").Updates(map[string]any{"installation_id": identity, "collection_epoch": "", "enabled_since": nil, "config_revision": current.ConfigRevision + 1, "schedule_token": "", "schedule_until": nil, "scan_cursor": nil, "next_scan_at": nil, "last_success_at": nil, "last_failure_at": nil, "last_error": ""}).Error; err != nil {
			return err
		}
		now, err := dbNow(tx)
		if err != nil {
			return err
		}
		return audit(tx, "identity_reset", actor, now)
	})
}

func (r *Repository) AcquireSchedule(ctx context.Context, snap domain.Snapshot, owner, token string) (bool, error) {
	result := r.db.WithContext(ctx).Exec(`UPDATE usage_reporting_state SET schedule_owner = ?, schedule_token = ?, schedule_until = DATE_ADD(UTC_TIMESTAMP(6), INTERVAL 60 SECOND)
 WHERE id = 1 AND config_revision = ? AND collection_epoch = ? AND (next_scan_at IS NULL OR next_scan_at <= UTC_TIMESTAMP(6)) AND (schedule_until IS NULL OR schedule_until < UTC_TIMESTAMP(6))`, owner, token, snap.ConfigRevision, snap.CollectionEpoch)
	return result.RowsAffected == 1, result.Error
}

func same(current, expected domain.Snapshot) bool {
	return current.Enabled && current.ConfigRevision == expected.ConfigRevision && current.CollectionEpoch == expected.CollectionEpoch
}

func enqueue(tx *gorm.DB, report domain.Report, now time.Time) error {
	body, err := application.MarshalReport(report)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(body)
	payload := string(body)
	window := "once"
	if report.ReportKind == "daily" {
		window = report.BusinessDate
	}
	expires := now.Add(7 * 24 * time.Hour)

	job := domain.Job{ReportID: report.ReportID, InstallationID: report.InstallationID, CollectionEpoch: report.CollectionEpoch, ReportKind: report.ReportKind, WindowKey: window, Payload: &payload, PayloadHash: hex.EncodeToString(hash[:]), Status: "pending", NextAttemptAt: now, CreatedAt: now, ExpiresAt: expires}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
		return err
	}
	var pending []domain.Job
	if err := tx.Select("report_id").Where("status IN ('pending', 'sending')").Order("created_at DESC, report_id DESC").Limit(16).Offset(16).Find(&pending).Error; err != nil {
		return err
	}
	for _, old := range pending {
		if err := tx.Model(&domain.Job{}).Where("report_id = ?", old.ReportID).Updates(map[string]any{"payload": nil, "status": "dropped", "lease_token": "", "lease_until": nil}).Error; err != nil {
			return err
		}
	}
	if len(pending) != 0 {
		if err := tx.Model(&domain.State{}).Where("id = 1").Updates(map[string]any{"dropped_count": gorm.Expr("dropped_count + ?", len(pending)), "last_failure_at": now, "last_error": "queue_limit"}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) CommitWindow(ctx context.Context, snap domain.Snapshot, token string, report *domain.Report, cursor, next time.Time, failure string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := snapshot(tx, true)
		if err != nil {
			return err
		}
		now, err := dbNow(tx)
		if err != nil {
			return err
		}
		if !same(current, snap) || current.ScheduleToken != token || current.ScheduleUntil == nil || !current.ScheduleUntil.After(now) {
			return domain.ErrStale
		}
		if report != nil {
			if report.InstallationID != current.InstallationID || report.CollectionEpoch != current.CollectionEpoch || report.ReportKind != "daily" || report.GeneratedAt.Before(*current.EnabledSince) {
				return domain.ErrStale
			}
			if err := enqueue(tx, *report, now); err != nil {
				return err
			}
		}
		updates := map[string]any{"scan_cursor": cursor, "next_scan_at": next, "schedule_token": "", "schedule_until": nil}
		if failure != "" {
			updates["last_error"], updates["last_failure_at"] = failure, now
		}
		return tx.Model(&domain.State{}).Where("id = 1").Updates(updates).Error
	})
}

func (r *Repository) Claim(ctx context.Context, snap domain.Snapshot, owner, token string) (*domain.Job, error) {
	var job *domain.Job
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := snapshot(tx, true)
		if err != nil {
			return err
		}
		if !same(current, snap) {
			return domain.ErrStale
		}
		// State lock serializes the tiny claim transaction across all replicas.
		var rows []domain.Job
		if err := tx.Where("installation_id = ? AND collection_epoch = ? AND payload IS NOT NULL AND expires_at > UTC_TIMESTAMP(6) AND next_attempt_at <= UTC_TIMESTAMP(6) AND (status = 'pending' OR (status = 'sending' AND lease_until < UTC_TIMESTAMP(6)))", snap.InstallationID, snap.CollectionEpoch).Order("next_attempt_at, created_at").Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		value := rows[0]
		if err := tx.Model(&domain.Job{}).Where("report_id = ?", value.ReportID).Updates(map[string]any{"status": "sending", "attempts": gorm.Expr("attempts + 1"), "lease_owner": owner, "lease_token": token, "lease_until": gorm.Expr("DATE_ADD(UTC_TIMESTAMP(6), INTERVAL 30 SECOND)")}).Error; err != nil {
			return err
		}
		value.LeaseToken, value.Attempts, value.Status = token, value.Attempts+1, "sending"
		job = &value
		return nil
	})
	return job, err
}

func (r *Repository) Complete(ctx context.Context, snap domain.Snapshot, job domain.Job, status, code string, next time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := snapshot(tx, true)
		if err != nil {
			return err
		}
		if !same(current, snap) {
			return domain.ErrStale
		}
		updates := map[string]any{"status": status, "next_attempt_at": next, "lease_token": "", "lease_until": nil}
		if status != "pending" {
			updates["payload"] = nil
		}
		result := tx.Model(&domain.Job{}).Where("report_id = ? AND status = 'sending' AND lease_token = ? AND lease_until > UTC_TIMESTAMP(6)", job.ReportID, job.LeaseToken).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return domain.ErrStale
		}
		state := map[string]any{}
		if status == "sent" {
			state["last_success_at"], state["last_error"] = gorm.Expr("UTC_TIMESTAMP(6)"), ""
		} else {
			state["last_failure_at"], state["last_error"] = gorm.Expr("UTC_TIMESTAMP(6)"), code
		}
		return tx.Model(&domain.State{}).Where("id = 1").Updates(state).Error
	})
}

func (r *Repository) Cleanup(ctx context.Context, disabled bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock independently of settings decoding, so corrupt settings can be purged.
		var state domain.State
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, 1).Error; err != nil {
			return err
		}
		current, readErr := snapshot(tx, false)
		if disabled || readErr != nil || !current.Enabled {
			if err := cancelPending(tx); err != nil {
				return err
			}
		}
		if err := tx.Model(&domain.Job{}).Where("status IN ('pending', 'sending') AND expires_at <= UTC_TIMESTAMP(6)").Updates(map[string]any{"status": "expired", "payload": nil, "lease_token": "", "lease_until": nil}).Error; err != nil {
			return err
		}
		if err := tx.Where("created_at < DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 30 DAY) AND payload IS NULL AND status NOT IN ('pending', 'sending')").Delete(&domain.Job{}).Error; err != nil {
			return err
		}
		return tx.Exec("DELETE FROM usage_reporting_events WHERE created_at < DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 30 DAY)").Error
	})
}

func (r *Repository) Pending(ctx context.Context) (int64, *time.Time, error) {
	var row struct {
		Count int64
		Next  *time.Time
	}
	err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) AS count, MIN(CASE WHEN status = 'sending' THEN lease_until ELSE next_attempt_at END) AS next FROM usage_reporting_outbox WHERE status IN ('pending', 'sending') AND expires_at > UTC_TIMESTAMP(6)").Scan(&row).Error
	return row.Count, row.Next, err
}

var _ domain.Repository = (*Repository)(nil)
