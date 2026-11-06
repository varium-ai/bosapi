package infrastructure

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/migrations"
	"gorm.io/gorm"
)

func newRequestLogTransitionMySQL(t *testing.T) (*gorm.DB, time.Time) {
	t.Helper()
	database := newAdminDashboardMySQL(t)
	if err := database.Exec(`CREATE TABLE request_logs (
		id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT, created_at DATETIME(6) NOT NULL,
		level VARCHAR(16) NOT NULL, event VARCHAR(128) NOT NULL, message VARCHAR(2048) NOT NULL,
		request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		client_ip VARCHAR(64) NOT NULL, organization_id BIGINT UNSIGNED NOT NULL, api_key_masked VARCHAR(128) NOT NULL,
		KEY idx_request_logs_created_at (created_at),
		KEY idx_request_logs_org_request_time (organization_id, request_id, created_at, id),
		KEY idx_request_logs_request_time (request_id, created_at, id)
	) ENGINE=InnoDB`).Error; err != nil {
		t.Fatal(err)
	}
	script, err := migrations.Files.ReadFile("021_request_log_transition.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Connection(func(conn *gorm.DB) error {
		for _, statement := range strings.Split(string(script), ";") {
			if strings.TrimSpace(statement) != "" {
				if err := conn.Exec(statement).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := database.Raw("SELECT until_at FROM request_log_backfill WHERE id = 1").Scan(&now).Error; err != nil {
		t.Fatal(err)
	}
	return database, now
}

func TestRequestLogBackfillMySQLWindowResumeRollbackAndReads(t *testing.T) {
	database, now := newRequestLogTransitionMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database = database.WithContext(ctx)
	from := now.Add(-24 * time.Hour)
	var source []requestLogRecord
	add := func(at time.Time, org uint64, message string) {
		source = append(source, requestLogRecord{CreatedAt: at, Level: "INFO", Event: "test", Message: message,
			RequestID: "req-transition", OrganizationID: org, ClientIP: "192.0.2.1", APIKeyMasked: "sk-***"})
	}
	add(from.Add(-time.Microsecond), 1, "older than 24 hours stays in legacy")
	add(from, 1, "inclusive window start")
	add(now, 1, "inclusive window end")
	add(now.Add(time.Microsecond), 1, "late legacy writer stays visible")
	add(from, 2, "another tenant")
	for i := range requestLogBackfillBatchSize + 1 {
		add(from.Add(time.Hour+time.Duration(i)*time.Microsecond), 1, "batch event "+time.Duration(i).String())
	}
	if err := database.Table("request_logs").Omit("LegacyID").CreateInBatches(&source, 100).Error; err != nil {
		t.Fatal(err)
	}
	repository := &RequestLogRepository{database: database, ctx: ctx}
	repository.persistBatch([]domain.RequestLog{{CreatedAt: now.Add(time.Second), Level: "INFO", Event: "test", Message: "new writer", RequestID: "req-transition", OrganizationID: 1, ClientIP: "192.0.2.1", APIKeyMasked: "sk-***"}})
	filter := domain.RequestLogFilter{RequestID: "req-transition", OrganizationID: 1}
	readMessages := func() []string {
		t.Helper()
		rows, more, err := repository.ListRequestLogs(ctx, filter, 0, 1000)
		if err != nil || more || len(rows) != len(source) { // one tenant excluded, one new event added
			t.Fatalf("rows=%d more=%v err=%v", len(rows), more, err)
		}
		messages := make([]string, len(rows))
		for i, row := range rows {
			if row.OrganizationID != 1 {
				t.Fatal("tenant boundary leaked")
			}
			messages[i] = row.Message
		}
		return messages
	}
	want := readMessages()
	// Force failure after destination insertion but before checkpoint update.
	if err := database.Exec(`CREATE TRIGGER reject_backfill_checkpoint BEFORE UPDATE ON request_log_backfill
		FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'checkpoint unavailable'`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := requestLogBackfillBatch(ctx, database, now); err == nil {
		t.Fatal("checkpoint failure must fail the batch")
	}
	var count int64
	if err := database.Table("request_logs_v2").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed batch partially committed destination rows=%d: %v", count, err)
	}
	if err := database.Exec("DROP TRIGGER reject_backfill_checkpoint").Error; err != nil {
		t.Fatal(err)
	}
	if done, err := requestLogBackfillBatch(ctx, database, now); err != nil || done {
		t.Fatalf("first batch done=%v err=%v", done, err)
	}
	if err := database.Table("request_logs_v2").Count(&count).Error; err != nil || count != 1+requestLogBackfillBatchSize {
		t.Fatalf("batch must contain at most %d rows, got %d: %v", requestLogBackfillBatchSize, count, err)
	}
	if got := readMessages(); !reflect.DeepEqual(got, want) {
		t.Fatal("copying a partial batch duplicated, lost or reordered visible history")
	}
	// A new invocation resumes from the committed checkpoint; no process memory
	// is needed. Another page completes, followed by an empty-page acknowledgement.
	for range 3 {
		if err := database.Exec("UPDATE request_log_backfill SET next_batch_at = NULL").Error; err != nil {
			t.Fatal(err)
		}
		if done, err := requestLogBackfillBatch(ctx, database, now); err != nil {
			t.Fatal(err)
		} else if done {
			break
		}
	}
	var state requestLogBackfillState
	if err := database.Table("request_log_backfill").First(&state).Error; err != nil || state.CompletedAt == nil {
		t.Fatalf("backfill not completed: %+v err=%v", state, err)
	}
	if got := readMessages(); !reflect.DeepEqual(got, want) {
		t.Fatal("completed copy changed visible history")
	}
	if err := database.Table("request_logs").Count(&count).Error; err != nil || count != int64(len(source)) {
		t.Fatalf("backfill modified original history rows=%d: %v", count, err)
	}
	if err := database.Table("request_logs_v2").Where("legacy_id IS NOT NULL AND (created_at < ? OR created_at > ?)", from, now).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("copied rows outside the 24-hour window=%d: %v", count, err)
	}
	var before int64
	if err := database.Table("request_logs_v2").Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("UPDATE request_log_backfill SET cursor_at = from_at, cursor_id = 0, completed_at = NULL, next_batch_at = NULL").Error; err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err := database.Exec("UPDATE request_log_backfill SET next_batch_at = NULL").Error; err != nil {
			t.Fatal(err)
		}
		if done, err := requestLogBackfillBatch(ctx, database, now); err != nil {
			t.Fatal(err)
		} else if done {
			break
		}
	}
	if err := database.Table("request_logs_v2").Count(&count).Error; err != nil || count != before {
		t.Fatalf("replaying a batch duplicated rows: %d vs %d: %v", count, before, err)
	}
	page, more, err := repository.ListRequestLogs(ctx, filter, 199, 5)
	if err != nil || !more || len(page) != 5 {
		t.Fatalf("pagination len=%d more=%v err=%v", len(page), more, err)
	}
	for i, row := range page {
		if row.Message != want[199+i] {
			t.Fatal("merged pagination changed ordering")
		}
	}
}

func TestRequestLogBackfillMySQLSkipsBusyReplica(t *testing.T) {
	database, now := newRequestLogTransitionMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Rollback()
	var id int
	if err := owner.QueryRowContext(ctx, "SELECT id FROM request_log_backfill WHERE id=1 FOR UPDATE").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if done, err := requestLogBackfillBatch(ctx, database, now); err != nil || done {
		t.Fatalf("competing replica should skip immediately: done=%v err=%v", done, err)
	}
}

func TestRequestLogBackfillMySQLSharesThrottleAcrossReplicas(t *testing.T) {
	database, now := newRequestLogTransitionMySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.Exec("UPDATE request_log_backfill SET next_batch_at = UTC_TIMESTAMP(6) + INTERVAL 1 DAY").Error; err != nil {
		t.Fatal(err)
	}
	if done, err := requestLogBackfillBatch(ctx, database, now); err != nil || done {
		t.Fatalf("replica must respect the shared pause: done=%v err=%v", done, err)
	}
	var state requestLogBackfillState
	if err := database.Table("request_log_backfill").First(&state).Error; err != nil || state.CompletedAt != nil || state.CursorID != 0 {
		t.Fatalf("paused replica advanced progress: %+v err=%v", state, err)
	}
}
