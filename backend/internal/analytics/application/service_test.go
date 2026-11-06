package application

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bosapi/backend/internal/analytics/domain"
)

type memoryRepo struct {
	mu      sync.Mutex
	snap    domain.Snapshot
	jobs    []domain.Job
	reports []domain.Report
	readErr error
}

func (r *memoryRepo) Snapshot(context.Context) (domain.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap, r.readErr
}
func (r *memoryRepo) InitializeDefault(_ context.Context, report domain.Report) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr != nil {
		return r.readErr
	}
	if !r.snap.Configured {
		r.toggle(true, report)
	}
	return nil
}
func (r *memoryRepo) Toggle(_ context.Context, enabled bool, _ *uint64, report domain.Report) (domain.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.toggle(enabled, report), nil
}
func (r *memoryRepo) toggle(enabled bool, report domain.Report) domain.Snapshot {
	if r.snap.Configured && r.snap.Enabled == enabled {
		return r.snap
	}
	r.snap.Configured = true
	r.snap.ConfigRevision++
	r.snap.Enabled = enabled
	r.jobs = nil
	if enabled {
		if r.snap.InstallationID == "" {
			r.snap.InstallationID = report.InstallationID
		}
		r.snap.CollectionEpoch = report.CollectionEpoch
		r.snap.EnabledSince = &report.GeneratedAt
		day := domain.DayStart(report.GeneratedAt)
		r.snap.ScanCursor = &day
		report.InstallationID = r.snap.InstallationID
		r.reports = append(r.reports, report)
	}
	return r.snap
}
func (r *memoryRepo) Reset(context.Context, *uint64, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snap.Enabled {
		return domain.ErrConflict
	}
	r.snap.InstallationID = domain.UUID()
	return nil
}
func (r *memoryRepo) AcquireSchedule(context.Context, domain.Snapshot, string, string) (bool, error) {
	return true, nil
}
func (r *memoryRepo) CommitWindow(_ context.Context, s domain.Snapshot, _ string, report *domain.Report, cursor, next time.Time, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.snap.Enabled || r.snap.ConfigRevision != s.ConfigRevision {
		return domain.ErrStale
	}
	r.snap.ScanCursor = &cursor
	r.snap.NextScanAt = &next
	if report != nil {
		r.reports = append(r.reports, *report)
	}
	return nil
}
func (r *memoryRepo) Claim(_ context.Context, _ domain.Snapshot, _ string, token string) (*domain.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.jobs) == 0 {
		return nil, nil
	}
	r.jobs[0].LeaseToken = token
	r.jobs[0].Attempts++
	job := r.jobs[0]
	return &job, nil
}
func (r *memoryRepo) Complete(_ context.Context, s domain.Snapshot, job domain.Job, status, _ string, next time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.snap.Enabled || r.snap.ConfigRevision != s.ConfigRevision {
		return domain.ErrStale
	}
	if len(r.jobs) > 0 {
		r.jobs[0].Status = status
		r.jobs[0].NextAttemptAt = next
		if status != "pending" {
			r.jobs[0].Payload = nil
		}
	}
	return nil
}
func (r *memoryRepo) Cleanup(_ context.Context, disabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if disabled || !r.snap.Enabled {
		r.jobs = nil
	}
	return nil
}
func (r *memoryRepo) Pending(context.Context) (int64, *time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.jobs)), nil, nil
}

type testSender struct {
	do func(context.Context, string, []byte) (SendResult, error)
}

func (s testSender) Send(ctx context.Context, url string, body []byte) (SendResult, error) {
	return s.do(ctx, url, body)
}
func (testSender) CloseIdleConnections() {}

func enabledService(t *testing.T, sender Sender) (*Service, *memoryRepo) {
	t.Helper()
	r := &memoryRepo{}
	s := New(r, Config{Endpoint: "https://receiver.invalid/v1/usage-reports", Timeout: time.Second}, sender, "test")
	if err := s.Toggle(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, r
}

func TestDisabledAndForcedDisabledNeverResolveOrConnect(t *testing.T) {
	for _, mode := range []string{"saved_disabled", "forced", "saved_disabled_after_restart"} {
		t.Run(mode, func(t *testing.T) {
			forced := mode == "forced"
			var dns, dials atomic.Int32
			resolver := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
				dns.Add(1)
				return nil, errors.New("unexpected DNS")
			}}
			dialer := &net.Dialer{Resolver: resolver}
			client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				dials.Add(1)
				return dialer.DialContext(ctx, network, address)
			}}}
			sender := testSender{do: func(ctx context.Context, url string, body []byte) (SendResult, error) {
				req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
				resp, err := client.Do(req)
				if resp != nil {
					resp.Body.Close()
				}
				return SendResult{}, err
			}}
			r := &memoryRepo{snap: domain.Snapshot{Configured: true}}
			if forced {
				r.snap.Enabled = true
			}
			s := New(r, Config{Endpoint: "https://receiver.invalid", ForceDisabled: forced}, sender, "")
			if mode == "saved_disabled_after_restart" {
				if err := s.Toggle(context.Background(), true, nil); err != nil {
					t.Fatal(err)
				}
				if err := s.Toggle(context.Background(), false, nil); err != nil {
					t.Fatal(err)
				}
				s.Close()
				s = New(r, Config{Endpoint: "https://receiver.invalid"}, sender, "")
			}
			defer s.Close()
			_ = s.Refresh(context.Background())
			_ = s.Generate(context.Background())
			_ = s.Deliver(context.Background())
			_, err := s.Preview(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if dns.Load() != 0 || dials.Load() != 0 {
				t.Fatalf("dns=%d connects=%d", dns.Load(), dials.Load())
			}
		})
	}
}

func TestEpochIsolationAndIdempotentSwitches(t *testing.T) {
	s, r := enabledService(t, nil)
	first := r.snap
	if err := s.Toggle(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	if r.snap.CollectionEpoch != first.CollectionEpoch || len(r.reports) != 1 {
		t.Fatal("repeated enable changed epoch")
	}
	_ = s.Toggle(context.Background(), false, nil)
	_ = s.Toggle(context.Background(), false, nil)
	if r.snap.ConfigRevision != 2 {
		t.Fatal("repeated disable changed revision")
	}
	_ = s.Toggle(context.Background(), true, nil)
	if r.snap.InstallationID != first.InstallationID || r.snap.CollectionEpoch == first.CollectionEpoch {
		t.Fatal("identity or epoch isolation")
	}
}

func TestDisableCancelsInflightAndReadFailureFailsClosed(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "disable", true: "read_failure"}[failure], func(t *testing.T) {
			started, done := make(chan struct{}), make(chan struct{})
			sender := testSender{do: func(ctx context.Context, _ string, _ []byte) (SendResult, error) {
				close(started)
				<-ctx.Done()
				close(done)
				return SendResult{}, ctx.Err()
			}}
			s, r := enabledService(t, sender)
			report := r.reports[0]
			payload, err := MarshalReport(report)
			if err != nil {
				t.Fatal(err)
			}
			body := string(payload)
			r.jobs = []domain.Job{{ReportID: report.ReportID, InstallationID: report.InstallationID, CollectionEpoch: report.CollectionEpoch, ReportKind: report.ReportKind, WindowKey: "once", Payload: &body, ExpiresAt: time.Now().Add(time.Hour)}}
			finished := make(chan struct{})
			go func() { defer close(finished); _ = s.Deliver(context.Background()) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("delivery did not start")
			}
			if failure {
				r.mu.Lock()
				r.readErr = domain.ErrUnavailable
				r.mu.Unlock()
				_ = s.Refresh(context.Background())
			} else {
				if err := s.Toggle(context.Background(), false, nil); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("HTTP not cancelled")
			}
			<-finished
			_ = s.Deliver(context.Background())
		})
	}
}

func TestHeartbeatOnlyAttestsToCurrentDayAfterDowntime(t *testing.T) {
	s, r := enabledService(t, nil)
	enabled := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r.snap.EnabledSince = &enabled
	s.now = func() time.Time { return time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC) }
	_ = s.Refresh(context.Background())
	before := len(r.reports)
	if err := s.Generate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.reports) != before+1 {
		t.Fatal("historical heartbeats fabricated")
	}
	last := r.reports[len(r.reports)-1]
	if last.ReportKind != "daily" || last.BusinessDate != "2026-10-20" || !last.GeneratedAt.Equal(s.now()) {
		t.Fatal(last)
	}
	if !r.snap.NextScanAt.Equal(NextSchedule(domain.DayStart(s.now()).AddDate(0, 0, 1), r.snap.InstallationID)) {
		t.Fatal("next day schedule")
	}
}

func TestHeartbeatScheduleUsesUTCMidnight(t *testing.T) {
	for _, tc := range []struct {
		instant  string
		generate bool
	}{
		{"2026-12-31T16:30:00Z", true},
		{"2027-01-01T00:00:00Z", false},
		{"2027-01-01T00:14:59Z", false},
		{"2027-01-01T00:45:01Z", true},
	} {
		t.Run(tc.instant, func(t *testing.T) {
			s, r := enabledService(t, nil)
			now, _ := time.Parse(time.RFC3339, tc.instant)
			s.now = func() time.Time { return now }
			if err := s.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			before := len(r.reports)
			if err := s.Generate(context.Background()); err != nil {
				t.Fatal(err)
			}
			expected := before
			nextDay := now.Format(time.DateOnly)
			if tc.generate {
				expected++
				nextDay = now.AddDate(0, 0, 1).Format(time.DateOnly)
				last := r.reports[len(r.reports)-1]
				if last.BusinessDate != now.Format(time.DateOnly) || last.Timezone != "UTC" {
					t.Fatal("wrong heartbeat date", last)
				}
			}
			if len(r.reports) != expected {
				t.Fatal("heartbeat generated outside UTC schedule", r.reports)
			}
			earliest, _ := time.Parse(time.RFC3339, nextDay+"T00:15:00Z")
			latest := earliest.Add(30 * time.Minute)
			if r.snap.NextScanAt == nil || r.snap.NextScanAt.Before(earliest) || r.snap.NextScanAt.After(latest) {
				t.Fatal("next schedule outside UTC window", r.snap.NextScanAt)
			}
			if status := s.Status(context.Background()); status.Timezone != "UTC" {
				t.Fatal("status uses a different calendar", status.Timezone)
			}
			local := earliest.In(time.FixedZone("UTC+8", 8*60*60))
			if !NextSchedule(local, r.snap.InstallationID).Equal(*r.snap.NextScanAt) {
				t.Fatal("schedule depends on input time zone")
			}
		})
	}
}

func TestDisabledPreviewDoesNotTouchUsageOrNetwork(t *testing.T) {
	r := &memoryRepo{}
	s := New(r, Config{}, nil, "")
	defer s.Close()
	p, err := s.Preview(context.Background())
	if err != nil || !p.Template {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := s.Preview(context.Background()); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatal(err)
	}
}

func TestEnabledPreviewOnlyContainsInstallationMetadata(t *testing.T) {
	s, r := enabledService(t, nil)
	before := len(r.reports)
	p, err := s.Preview(context.Background())
	if err != nil || p.Template || p.Report.InstallationID != r.snap.InstallationID || len(r.reports) != before {
		t.Fatal(p, err)
	}
	body, _ := json.Marshal(p.Report)
	for _, field := range []string{"usage", "tokens", "prompt", "content", "credential", "window_start", "partial"} {
		if strings.Contains(string(body), field) {
			t.Fatal("non-minimal preview", field)
		}
	}
}

func TestInvalidAndLegacyQueuedBodiesArePurgedWithoutSending(t *testing.T) {
	var calls atomic.Int32
	s, r := enabledService(t, testSender{do: func(context.Context, string, []byte) (SendResult, error) {
		calls.Add(1)
		return SendResult{Status: 204}, nil
	}})
	report := r.reports[0]
	valid, _ := MarshalReport(report)
	for _, suffix := range []string{`,"prompt":"secret-user-content"}`, `,"api_key":"sk-test-full-credential"}`, `,"usage":{"record_count":1}}`} {
		body := strings.TrimSuffix(string(valid), "}") + suffix
		r.jobs = []domain.Job{{ReportID: report.ReportID, InstallationID: report.InstallationID, CollectionEpoch: report.CollectionEpoch, ReportKind: report.ReportKind, WindowKey: "once", Payload: &body, ExpiresAt: time.Now().Add(time.Hour)}}
		if err := s.Deliver(context.Background()); err != nil {
			t.Fatal(err)
		}
		if r.jobs[0].Payload != nil || r.jobs[0].Status != "failed" {
			t.Fatal("invalid body retained")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid queued payload sent")
	}
}

func TestUnavailableAndForceDisabledStates(t *testing.T) {
	for _, tc := range []struct {
		cfg  Config
		repo domain.Repository
		want string
	}{
		{Config{}, nil, "unavailable"},
		{Config{ForceDisabled: true}, nil, "forced_disabled"},
		{Config{}, &memoryRepo{}, "unavailable"},
		{Config{Endpoint: "http://invalid.example"}, &memoryRepo{}, "unavailable"},
		{Config{Endpoint: "https://receiver.invalid"}, &memoryRepo{}, "disabled"},
	} {
		s := New(tc.repo, tc.cfg, nil, "")
		status := s.Status(context.Background())
		s.Close()
		if status.State != tc.want || status.EffectiveEnabled {
			t.Fatal(status)
		}
	}
}

func TestRetryPolicyAndSchedule(t *testing.T) {
	now := time.Now()
	expiry := now.Add(7 * 24 * time.Hour)
	for _, status := range []int{200, 201, 204} {
		state, _, _ := DeliveryDecision(SendResult{Status: status}, nil, 1, now, expiry)
		if state != "sent" {
			t.Fatal(status)
		}
	}
	for _, status := range []int{202, 301, 400, 401, 403, 404, 409, 413, 422} {
		state, _, _ := DeliveryDecision(SendResult{Status: status}, nil, 1, now, expiry)
		if state != "failed" {
			t.Fatal(status)
		}
	}
	for _, status := range []int{408, 429, 500, 503} {
		state, _, next := DeliveryDecision(SendResult{Status: status}, nil, 1, now, expiry)
		if state != "pending" || next.Before(now.Add(time.Minute)) || next.After(now.Add(66*time.Second)) {
			t.Fatal(status, state, next)
		}
	}
	state, _, next := DeliveryDecision(SendResult{Status: 429, RetryAfter: 24 * time.Hour}, nil, 1, now, expiry)
	if state != "pending" || !next.Equal(now.Add(24*time.Hour)) {
		t.Fatal(state, next)
	}
	state, _, _ = DeliveryDecision(SendResult{Status: 429, RetryAfter: 8 * 24 * time.Hour}, nil, 1, now, expiry)
	if state != "expired" {
		t.Fatal(state)
	}
	_, _, next = DeliveryDecision(SendResult{}, errors.New("network"), 20, now, expiry)
	if next.Sub(now) > 6*time.Hour {
		t.Fatal(next)
	}
	day := domain.DayStart(now)
	schedule := NextSchedule(day, "deployment")
	if schedule.Before(day.Add(15*time.Minute)) || schedule.After(day.Add(45*time.Minute)) || !schedule.Equal(NextSchedule(day, "deployment")) {
		t.Fatal(schedule)
	}
}
