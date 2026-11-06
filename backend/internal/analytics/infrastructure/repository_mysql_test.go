package infrastructure

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bosapi/backend/internal/analytics/application"
	"bosapi/backend/internal/analytics/domain"
	platformdb "bosapi/backend/internal/platform/database"
	"bosapi/backend/migrations"
	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Each test creates and drops its own randomly named database. Never point this
// variable at a production server; a disposable MySQL 8 server is recommended.
func mysqlRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("BOS_ANALYTICS_TEST_DSN")
	if dsn == "" {
		t.Skip("set BOS_ANALYTICS_TEST_DSN to an isolated MySQL server")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.MultiStatements = true
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := "bos_usage_test_" + strings.ReplaceAll(domain.UUID(), "-", "")
	if _, err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { conn.Close() })
	base, err := migrations.Files.ReadFile("001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"settings", "usage_logs"} {
		start := strings.Index(string(base), "CREATE TABLE IF NOT EXISTS "+table+" (")
		if start < 0 {
			t.Fatal(table)
		}
		end := strings.Index(string(base[start:]), ";") + 1
		if err := db.Exec(string(base[start : start+end])).Error; err != nil {
			t.Fatal(err)
		}
	}
	script, err := migrations.Files.ReadFile("011_usage_reporting.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(script)).Error; err != nil {
		t.Fatal(err)
	}
	privacy, err := migrations.Files.ReadFile("012_installation_telemetry_privacy.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(privacy)).Error; err != nil {
		t.Fatal(err)
	}
	return NewRepository(db), db
}

func enable(t *testing.T, r *Repository) domain.Snapshot {
	t.Helper()
	snap, err := r.Toggle(context.Background(), true, nil, domain.Installation(domain.Metadata{Version: "test", OS: "linux", Arch: "amd64"}, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestMySQLConcurrentDefaultInitializationAndSavedOptOut(t *testing.T) {
	repo, db := mysqlRepository(t)
	ctx := context.Background()
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- repo.InitializeDefault(ctx, domain.Installation(domain.Metadata{Version: "v1.0.0", OS: "linux", Arch: "amd64"}, time.Now()))
		}()
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := repo.Snapshot(ctx)
	if err != nil || !snapshot.Enabled || !snapshot.Configured {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	var count int64
	if err := db.Model(&domain.Job{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("default initialization must enqueue once", count, err)
	}
	if _, err := repo.Toggle(ctx, false, nil, domain.Report{}); err != nil {
		t.Fatal(err)
	}
	if err := repo.InitializeDefault(ctx, domain.Installation(domain.Metadata{}, time.Now())); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.Snapshot(ctx)
	if err != nil || saved.Enabled || !saved.Configured || saved.InstallationID != snapshot.InstallationID {
		t.Fatal("default initialization changed a persisted opt-out", saved, err)
	}
}

func TestMySQLDefaultToggleEpochAndCorruptDisable(t *testing.T) {
	r, db := mysqlRepository(t)
	ctx := context.Background()
	snap, err := r.Snapshot(ctx)
	if err != nil || snap.Enabled || snap.InstallationID != "" {
		t.Fatalf("%+v %v", snap, err)
	}
	first := enable(t, r)
	if first.EnabledSince == nil || first.ScanCursor == nil || first.NextScanAt == nil {
		t.Fatal("missing initial schedule", first)
	}
	day := domain.DayStart(*first.EnabledSince)
	next := application.NextSchedule(day.AddDate(0, 0, 1), first.InstallationID)
	if !first.ScanCursor.Equal(day) || !first.NextScanAt.Equal(next) {
		t.Fatal("initial schedule does not follow UTC midnight", first)
	}
	second := enable(t, r)
	if first.CollectionEpoch != second.CollectionEpoch || first.InstallationID != second.InstallationID || first.ConfigRevision != second.ConfigRevision {
		t.Fatal("enable not idempotent")
	}
	var count int64
	db.Model(&domain.Job{}).Count(&count)
	if count != 1 {
		t.Fatal(count)
	}
	if err := r.Reset(ctx, nil, time.Now()); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := r.Toggle(ctx, false, nil, domain.Report{}); err != nil {
		t.Fatal(err)
	}
	db.Model(&domain.Job{}).Where("payload IS NOT NULL").Count(&count)
	if count != 0 {
		t.Fatal("disable retained payload")
	}
	third := enable(t, r)
	if third.InstallationID != first.InstallationID || third.CollectionEpoch == first.CollectionEpoch {
		t.Fatal("epoch isolation")
	}
	if err := db.Exec("UPDATE settings SET value_text = '{\"enabled\":\"true\"}' WHERE setting_key = ?", domain.SettingKey).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(ctx); err == nil {
		t.Fatal("malformed setting accepted")
	}
	if _, err := r.Toggle(ctx, false, nil, domain.Report{}); err != nil {
		t.Fatal("disable could not repair corrupt setting", err)
	}
	snap, err = r.Snapshot(ctx)
	if err != nil || snap.Enabled {
		t.Fatal(snap, err)
	}
	before := snap.InstallationID
	if err := r.Reset(ctx, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	snap, _ = r.Snapshot(ctx)
	if snap.InstallationID == before || snap.CollectionEpoch != "" {
		t.Fatal("identity not reset")
	}
}

func TestMySQLTelemetryLeavesBusinessLogsUntouched(t *testing.T) {
	r, db := mysqlRepository(t)
	ctx := context.Background()
	const canary = "private-user-content-sk-test-full-credential"
	if err := db.Exec(`INSERT INTO usage_logs (request_id,usage_id,organization_id,api_key_id,model,endpoint,status_code,input_tokens,output_tokens,total_tokens,cost_amount,created_at)
 VALUES ('local-request','local-usage',1,1,?, ?,200,11,7,99,19.99,UTC_TIMESTAMP(3))`, canary, canary).Error; err != nil {
		t.Fatal(err)
	}
	s := application.New(r, application.Config{Endpoint: "https://receiver.invalid"}, nil, "1.2.3")
	defer s.Close()
	if err := s.Toggle(ctx, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.State{}).Where("id=1").Update("next_scan_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	preview, err := s.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(preview.Report)
	if strings.Contains(string(body), canary) {
		t.Fatal("business data leaked into preview")
	}
	var jobs []domain.Job
	db.Find(&jobs)
	for _, job := range jobs {
		if job.Payload != nil {
			if _, err := domain.DecodeReport([]byte(*job.Payload)); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(*job.Payload, canary) || strings.Contains(*job.Payload, "usage") {
				t.Fatal("business data entered queue")
			}
		}
	}
	if err := s.Toggle(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	var local struct {
		Model, Endpoint string
		TotalTokens     uint64
	}
	if err := db.Raw("SELECT model,endpoint,total_tokens FROM usage_logs WHERE request_id='local-request'").Scan(&local).Error; err != nil {
		t.Fatal(err)
	}
	if local.Model != canary || local.Endpoint != canary || local.TotalTokens != 99 {
		t.Fatal("local usage logs changed")
	}
	// The entire business-use table can be inaccessible; installation telemetry still works.
	if err := db.Exec("DROP TABLE usage_logs").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Toggle(ctx, true, nil); err != nil {
		t.Fatal(err)
	}
	db.Model(&domain.State{}).Where("id=1").Update("next_scan_at", time.Now().Add(-time.Hour))
	if err := s.Generate(ctx); err != nil {
		t.Fatal("telemetry depends on usage logs", err)
	}
}

func TestMySQLConcurrentClaimsAndFencedCompletions(t *testing.T) {
	r, db := mysqlRepository(t)
	snap := enable(t, r)
	ctx := context.Background()
	var wg sync.WaitGroup
	var winners atomic.Int32
	var first *domain.Job
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := r.Claim(ctx, snap, domain.UUID(), domain.UUID())
			if err != nil {
				t.Error(err)
			}
			if job != nil {
				winners.Add(1)
				mu.Lock()
				first = job
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("claim winners", winners.Load())
	}
	if err := db.Model(&domain.Job{}).Where("report_id = ?", first.ReportID).Update("lease_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	second, err := r.Claim(ctx, snap, "new-owner", domain.UUID())
	if err != nil || second == nil {
		t.Fatal(second, err)
	}
	if err := r.Complete(ctx, snap, *first, "sent", "", time.Now()); !errors.Is(err, domain.ErrStale) {
		t.Fatal("expired owner completed", err)
	}
	if err := r.Complete(ctx, snap, *second, "sent", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	var saved domain.Job
	db.First(&saved, "report_id = ?", second.ReportID)
	if saved.Payload != nil || saved.Status != "sent" {
		t.Fatal("success retained body", saved.Status)
	}
	// A configuration change fences even the latest claim.
	snap = enable(t, r)
	_, _ = r.Toggle(ctx, false, nil, domain.Report{})
	if err := r.Complete(ctx, snap, *second, "sent", "", time.Now()); !errors.Is(err, domain.ErrStale) {
		t.Fatal(err)
	}
}

func TestMySQLScheduleFreezeBoundsAndCleanup(t *testing.T) {
	r, db := mysqlRepository(t)
	snap := enable(t, r)
	ctx := context.Background()
	now := time.Now().UTC()
	day := domain.DayStart(now).AddDate(0, 0, -1)
	if err := db.Model(&domain.State{}).Where("id = 1").Updates(map[string]any{"enabled_since": day, "next_scan_at": now.Add(-time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	snap, _ = r.Snapshot(ctx)
	token := domain.UUID()
	owned, err := r.AcquireSchedule(ctx, snap, "owner", token)
	if err != nil || !owned {
		t.Fatal(owned, err)
	}
	if owned, err := r.AcquireSchedule(ctx, snap, "other", "other"); err != nil || owned {
		t.Fatal("double schedule", owned, err)
	}
	report := domain.Daily(domain.Metadata{Version: "1.2.3", OS: "linux", Arch: "amd64"}, snap.State, now)
	if err := r.CommitWindow(ctx, snap, "wrong", &report, day.Add(24*time.Hour), now, ""); !errors.Is(err, domain.ErrStale) {
		t.Fatal("wrong schedule token", err)
	}
	if err := r.CommitWindow(ctx, snap, token, &report, day.Add(24*time.Hour), now, ""); err != nil {
		t.Fatal(err)
	}
	// A second generated ID for the same frozen window cannot replace its body.
	if err := r.db.Transaction(func(tx *gorm.DB) error {
		report.ReportID = domain.UUID()
		report.AppVersion = "2.0.0"
		return enqueue(tx, report, now)
	}); err != nil {
		t.Fatal(err)
	}
	var jobs []domain.Job
	db.Where("report_kind = 'daily'").Find(&jobs)
	if len(jobs) != 1 || jobs[0].Payload == nil || strings.Contains(*jobs[0].Payload, `"app_version":"2.0.0"`) {
		t.Fatal("report was not frozen")
	}
	// Enforce the queue bound inside a transaction holding the state lock.
	for i := 1; i <= 20; i++ {
		start := day.AddDate(0, 0, -i)
		value := domain.Daily(domain.Metadata{Version: "1.2.3", OS: "linux", Arch: "amd64"}, snap.State, start)
		if err := r.db.Transaction(func(tx *gorm.DB) error {
			if _, err := snapshot(tx, true); err != nil {
				return err
			}
			return enqueue(tx, value, now.Add(time.Duration(i)*time.Millisecond))
		}); err != nil {
			t.Fatal(err)
		}
	}
	var pending int64
	db.Model(&domain.Job{}).Where("status IN ('pending','sending')").Count(&pending)
	if pending > 16 {
		t.Fatal(pending)
	}
	db.Model(&domain.Job{}).Where("status = 'pending'").Update("expires_at", now.Add(-time.Second))
	if err := r.Cleanup(ctx, false); err != nil {
		t.Fatal(err)
	}
	db.Model(&domain.Job{}).Where("payload IS NOT NULL").Count(&pending)
	if pending != 0 {
		t.Fatal("expired bodies retained", pending)
	}
	if _, err := r.Toggle(ctx, false, nil, domain.Report{}); err != nil {
		t.Fatal(err)
	}
	db.Model(&domain.Job{}).Where("1=1").Update("created_at", now.Add(-31*24*time.Hour))
	if err := r.Cleanup(ctx, false); err != nil {
		t.Fatal(err)
	}
	db.Model(&domain.Job{}).Count(&pending)
	if pending != 0 {
		t.Fatal("old metadata retained")
	}
}

func TestMySQLRealHTTPRetryUsesFrozenReportAndDisableCancels(t *testing.T) {
	r, db := mysqlRepository(t)
	var calls atomic.Int32
	var mu sync.Mutex
	var bodies []string
	committed := make(map[string]string)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var report domain.Report
		if err := json.Unmarshal(body, &report); err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, string(body))
		if previous, exists := committed[report.ReportID]; exists && previous != string(body) {
			t.Error("same report identity changed content")
		}
		committed[report.ReportID] = string(body)
		mu.Unlock()
		if calls.Add(1) == 1 {
			// Simulate a receiver commit followed by a lost HTTP response.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	sender := NewHTTPSender(time.Second)
	sender.client.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig
	s := application.New(r, application.Config{Endpoint: server.URL, Timeout: time.Second}, sender, "test")
	defer s.Close()
	if err := s.Toggle(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.Job{}).Where("status='pending'").Update("next_attempt_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(bodies) != 2 || bodies[0] != bodies[1] || len(committed) != 1 {
		t.Fatal("retry changed frozen body", calls.Load())
	}
	if err := s.Toggle(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("sent after disable")
	}
	var retained int64
	db.Model(&domain.Job{}).Where("payload IS NOT NULL").Count(&retained)
	if retained != 0 {
		t.Fatal(retained)
	}
}

func TestMySQLFullMigrationsLeaveTelemetryUnconfiguredUntilServiceStarts(t *testing.T) {
	r, db := mysqlRepository(t)
	dsn := db.Dialector.(*mysql.Dialector).Config.DSN
	for i := 0; i < 2; i++ {
		if err := platformdb.ApplyMigrations(context.Background(), dsn, migrations.Files, platformdb.InitialAdmin{Email: "reporting-test@example.invalid", Password: "isolated-test-password-123"}); err != nil {
			t.Fatal(err)
		}
		snap, err := r.Snapshot(context.Background())
		if err != nil || snap.Configured || snap.Enabled || snap.InstallationID != "" || snap.CollectionEpoch != "" {
			t.Fatal("migration enabled or identified deployment", snap, err)
		}
	}
}

func TestMySQLExpiredSchedulerAndWrongIdentityCannotCommit(t *testing.T) {
	r, db := mysqlRepository(t)
	ctx := context.Background()
	snap := enable(t, r)
	if err := db.Model(&domain.State{}).Where("id=1").Update("next_scan_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	token := domain.UUID()
	if ok, err := r.AcquireSchedule(ctx, snap, "owner", token); err != nil || !ok {
		t.Fatal(ok, err)
	}
	report := domain.Daily(domain.Metadata{Version: "1.2.3", OS: "linux", Arch: "amd64"}, snap.State, time.Now())
	report.InstallationID = domain.UUID()
	if err := r.CommitWindow(ctx, snap, token, &report, time.Now(), time.Now(), ""); !errors.Is(err, domain.ErrStale) {
		t.Fatal("wrong identity committed", err)
	}
	if err := db.Model(&domain.State{}).Where("id=1").Update("schedule_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.CommitWindow(ctx, snap, token, nil, time.Now(), time.Now(), ""); !errors.Is(err, domain.ErrStale) {
		t.Fatal("expired scheduler committed", err)
	}
}

func TestMySQLReplicaRefreshCancelsHTTPAndWorkerExits(t *testing.T) {
	r, db := mysqlRepository(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		close(started)
		<-req.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	sender := NewHTTPSender(10 * time.Second)
	sender.client.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig
	first := application.New(r, application.Config{Endpoint: server.URL, Timeout: 10 * time.Second}, sender, "test")
	defer first.Close()
	if err := first.Toggle(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	replica := application.New(NewRepository(db), application.Config{Endpoint: server.URL, Timeout: 10 * time.Second}, sender, "test")
	worker := NewWorker(replica)
	defer worker.Close()
	select {
	case <-started:
	case <-time.After(8 * time.Second):
		t.Fatal("replica did not deliver")
	}
	if err := first.Toggle(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(12 * time.Second):
		t.Fatal("replica did not cancel HTTP")
	}
	done := make(chan struct{})
	go func() { worker.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not exit before database shutdown")
	}
	var count int64
	db.Model(&domain.Job{}).Where("payload IS NOT NULL").Count(&count)
	if count != 0 {
		t.Fatal("pending bodies retained")
	}
}

func TestMySQLForceDisabledPurgesPreviousEpochWithoutSending(t *testing.T) {
	r, db := mysqlRepository(t)
	enable(t, r)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { calls.Add(1) }))
	defer server.Close()
	service := application.New(r, application.Config{ForceDisabled: true, Endpoint: server.URL}, NewHTTPSender(time.Second), "test")
	defer service.Close()
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = service.Generate(context.Background())
	_ = service.Deliver(context.Background())
	if err := service.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&domain.Job{}).Where("payload IS NOT NULL").Count(&count)
	if count != 0 || calls.Load() != 0 {
		t.Fatal("forced disable retained or sent payload", count, calls.Load())
	}
	if err := service.Toggle(context.Background(), true, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if err := service.Toggle(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLPrivacyMigrationPurgesLegacyBodies(t *testing.T) {
	r, db := mysqlRepository(t)
	enable(t, r)
	if err := db.Model(&domain.Job{}).Where("1=1").Update("payload", `{"schema_version":1,"prompt":"private-user-content","api_key":"sk-test-full-credential"}`).Error; err != nil {
		t.Fatal(err)
	}
	script, err := migrations.Files.ReadFile("012_installation_telemetry_privacy.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(script)).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&domain.Job{}).Where("payload IS NOT NULL").Count(&count)
	if count != 0 {
		t.Fatal("legacy bodies retained")
	}
}

func TestMySQLDisabledSwitchSurvivesServiceRecreation(t *testing.T) {
	r, db := mysqlRepository(t)
	ctx := context.Background()
	enable(t, r)
	if _, err := r.Toggle(ctx, false, nil, domain.Report{}); err != nil {
		t.Fatal(err)
	}
	restarted := application.New(NewRepository(db), application.Config{Endpoint: "https://receiver.invalid"}, NewHTTPSender(time.Second), "1.2.3")
	defer restarted.Close()
	if err := restarted.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	status := restarted.Status(ctx)
	if status.Enabled || status.EffectiveEnabled || status.State != "disabled" {
		t.Fatal("disabled state not durable", status)
	}
	if err := restarted.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&domain.Job{}).Where("payload IS NOT NULL").Count(&count)
	if count != 0 {
		t.Fatal("reports regenerated after disabled restart")
	}
}
