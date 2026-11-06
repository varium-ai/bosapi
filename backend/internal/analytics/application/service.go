package application

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"bosapi/backend/internal/analytics/domain"
)

type Config struct {
	ForceDisabled bool
	Endpoint      string
	Timeout       time.Duration
	InvalidReason string
}

type SendResult struct {
	Status     int
	RetryAfter time.Duration
}
type Sender interface {
	Send(context.Context, string, []byte) (SendResult, error)
	CloseIdleConnections()
}

type Status struct {
	State            string     `json:"state"`
	Enabled          bool       `json:"enabled"`
	EffectiveEnabled bool       `json:"effective_enabled"`
	DisabledReason   string     `json:"disabled_reason,omitempty"`
	Receiver         string     `json:"receiver"`
	Timezone         string     `json:"timezone"`
	InstallationID   string     `json:"installation_id,omitempty"`
	LastSuccessAt    *time.Time `json:"last_success_at"`
	NextScheduledAt  *time.Time `json:"next_scheduled_at"`
	LastFailureAt    *time.Time `json:"last_failure_at"`
	LastError        string     `json:"last_error,omitempty"`
	PendingCount     int64      `json:"pending_count"`
	DroppedCount     uint64     `json:"dropped_count"`
	Fields           []string   `json:"fields"`
	Retention        string     `json:"retention"`
}

type Preview struct {
	Report      domain.Report `json:"report"`
	Template    bool          `json:"template"`
	Explanation string        `json:"explanation"`
}

// Service owns the per-process cancellation gate shared by HTTP administration and workers.
type Service struct {
	repo        domain.Repository
	cfg         Config
	sender      Sender
	meta        domain.Metadata
	now         func() time.Time
	owner       string
	control     sync.Mutex
	mu          sync.Mutex
	active      context.Context
	cancel      context.CancelFunc
	revision    uint64
	checkedAt   time.Time
	previewAt   time.Time
	closed      bool
	initialized bool
}

func New(repo domain.Repository, cfg Config, sender Sender, version string) *Service {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	u, err := url.Parse(cfg.Endpoint)
	if cfg.Endpoint != "" && (err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(cfg.Endpoint, "#") || u.Opaque != "" || strings.TrimSpace(cfg.Endpoint) != cfg.Endpoint) {
		cfg.InvalidReason = "invalid_endpoint"
	}
	if cfg.Timeout < time.Second || cfg.Timeout > 10*time.Second {
		cfg.InvalidReason = "invalid_timeout"
	}
	version = domain.SafeVersion(version)
	return &Service{repo: repo, cfg: cfg, sender: sender, meta: domain.Metadata{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH}, now: time.Now, owner: domain.UUID()}
}

func (s *Service) reason() string {
	if s.cfg.ForceDisabled {
		return "forced_disabled"
	}
	if s.cfg.InvalidReason != "" {
		return s.cfg.InvalidReason
	}
	if s.repo == nil {
		return "database_unavailable"
	}
	if s.cfg.Endpoint == "" {
		return "endpoint_missing"
	}
	return ""
}

func (s *Service) block() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.active, s.cancel, s.checkedAt = nil, nil, time.Time{}
	s.mu.Unlock()
	if s.sender != nil {
		s.sender.CloseIdleConnections()
	}
}

func (s *Service) Close() {
	s.control.Lock()
	defer s.control.Unlock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.block()
}

// Refresh is independent of aggregation/sending so even slow tasks can be cancelled.
func (s *Service) Refresh(ctx context.Context) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.closed {
		return nil
	}
	if s.reason() != "" {
		s.block()
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if !s.initialized {
		if err := s.repo.InitializeDefault(ctx, domain.Installation(s.meta, s.now())); err != nil {
			s.block()
			return err
		}
		s.initialized = true
	}
	snap, err := s.repo.Snapshot(ctx)
	if err != nil || !snap.Enabled {
		s.block()
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.active == nil || s.revision != snap.ConfigRevision {
		if s.cancel != nil {
			s.cancel()
		}
		s.active, s.cancel = context.WithCancel(context.Background())
		s.revision = snap.ConfigRevision
	}
	s.checkedAt = s.now()
	return nil
}

func (s *Service) workContext(parent context.Context, revision uint64) (context.Context, context.CancelFunc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.active == nil || s.revision != revision || s.now().Sub(s.checkedAt) >= 30*time.Second {
		return nil, nil, false
	}
	// Derive directly from the gate: gate cancellation propagates synchronously
	// before a disable returns, rather than relying on an asynchronous callback.
	ctx, cancel := context.WithCancel(s.active)
	stop := context.AfterFunc(parent, cancel)
	if parent.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }, true
}

func (s *Service) Status(ctx context.Context) Status {
	st := Status{State: "disabled", Receiver: "", Timezone: "UTC", Fields: []string{"schema_version", "report_id", "installation_id", "collection_epoch", "report_kind", "generated_at", "business_date", "timezone", "app_version", "os", "arch"}, Retention: "Unsent reports: up to 7 days / 16 reports; payload removed on success, permanent failure or disable; task metadata and local operations: 30 days. Receiver retention is controlled by its operator."}
	if u, err := url.Parse(s.cfg.Endpoint); err == nil && u.Scheme == "https" && u.Host != "" {
		st.Receiver = "https://" + u.Host
	}
	st.DisabledReason = s.reason()
	if st.DisabledReason == "forced_disabled" {
		st.State = "forced_disabled"
	} else if st.DisabledReason != "" {
		st.State = "unavailable"
	}
	if s.repo == nil {
		return st
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	snap, err := s.repo.Snapshot(ctx)
	if err != nil {
		s.block()
		if st.State != "forced_disabled" {
			st.State, st.DisabledReason = "unavailable", "configuration_unavailable"
		}
		return st
	}
	st.Enabled, st.InstallationID = snap.Enabled, snap.InstallationID
	st.LastSuccessAt, st.LastFailureAt, st.LastError, st.DroppedCount = snap.LastSuccessAt, snap.LastFailureAt, snap.LastError, snap.DroppedCount
	count, next, err := s.repo.Pending(ctx)
	if err != nil {
		s.block()
		if st.State != "forced_disabled" {
			st.State, st.DisabledReason = "unavailable", "database_unavailable"
		}
		return st
	}
	st.PendingCount = count
	if st.DisabledReason == "" && snap.Enabled {
		st.State, st.EffectiveEnabled = "enabled", true
		st.NextScheduledAt = snap.NextScanAt
		if next != nil && (st.NextScheduledAt == nil || next.Before(*st.NextScheduledAt)) {
			st.NextScheduledAt = next
		}
	}
	return st
}

func (s *Service) Toggle(ctx context.Context, enabled bool, actor *uint64) error {
	s.control.Lock()
	if enabled && s.reason() != "" {
		s.control.Unlock()
		return domain.ErrConflict
	}
	s.block()
	if s.repo == nil {
		s.control.Unlock()
		return domain.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.repo.Toggle(ctx, enabled, actor, domain.Installation(s.meta, s.now()))
	s.control.Unlock()
	if err == nil {
		err = s.Refresh(ctx)
	}
	return err
}

func (s *Service) Reset(ctx context.Context, actor *uint64) error {
	s.control.Lock()
	defer s.control.Unlock()
	if s.repo == nil {
		return domain.ErrUnavailable
	}
	s.block()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.Reset(ctx, actor, s.now().UTC())
}

func (s *Service) Preview(ctx context.Context) (Preview, error) {
	s.mu.Lock()
	if !s.previewAt.IsZero() && s.now().Sub(s.previewAt) < 10*time.Second {
		s.mu.Unlock()
		return Preview{}, domain.ErrRateLimited
	}
	s.previewAt = s.now()
	s.mu.Unlock()
	now := s.now().UTC()
	state := domain.State{}
	template := true
	if s.reason() == "" {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		snap, err := s.repo.Snapshot(ctx)
		if err != nil {
			s.block()
			return Preview{}, domain.ErrUnavailable
		}
		if snap.Enabled {
			state, template = snap.State, false

		}
	}
	report := domain.Daily(s.meta, state, now)
	if template {
		report.InstallationID, report.CollectionEpoch = "not-created", "not-created"
	}
	return Preview{Report: report, Template: template, Explanation: "Preview only; never queued or sent. Installation identity, build version, platform and current heartbeat date only. Business logs and content are never read. Receiver retention/deletion policy must be supplied by its operator."}, nil
}

// NextSchedule is stable across restarts and replicas, with 0–30 minutes jitter.
func NextSchedule(day time.Time, installation string) time.Time {
	day = domain.DayStart(day)
	hash := sha256.Sum256([]byte(installation + day.Format(time.RFC3339)))
	return day.Add(15*time.Minute + time.Duration(binary.BigEndian.Uint32(hash[:4])%1801)*time.Second)
}

func (s *Service) Generate(ctx context.Context) error {
	if s.reason() != "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	snap, err := s.repo.Snapshot(ctx)
	if err != nil {
		s.block()
		return err
	}
	if !snap.Enabled {
		s.block()
		return nil
	}
	work, stop, ok := s.workContext(ctx, snap.ConfigRevision)
	if !ok {
		return nil
	}
	defer stop()
	token := domain.UUID()
	owned, err := s.repo.AcquireSchedule(work, snap, s.owner, token)
	if err != nil || !owned {
		return err
	}
	// Another replica may have advanced the cursor between our read and claim.
	current, err := s.repo.Snapshot(work)
	if err != nil {
		s.block()
		return err
	}
	if !current.Enabled || current.ConfigRevision != snap.ConfigRevision || current.CollectionEpoch != snap.CollectionEpoch {
		return domain.ErrStale
	}
	snap = current
	now := s.now().UTC()
	today := domain.DayStart(now)
	next := NextSchedule(today.AddDate(0, 0, 1), snap.InstallationID)
	if now.Before(NextSchedule(today, snap.InstallationID)) {
		return s.repo.CommitWindow(work, snap, token, nil, today, NextSchedule(today, snap.InstallationID), "")
	}
	// Only attest to running now; never invent historical heartbeats after downtime.
	report := domain.Daily(s.meta, snap.State, now)
	return s.repo.CommitWindow(work, snap, token, &report, today, next, "")
}

func (s *Service) Deliver(ctx context.Context) error {
	if s.reason() != "" || s.sender == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	snap, err := s.repo.Snapshot(ctx)
	if err != nil {
		s.block()
		return err
	}
	if !snap.Enabled {
		s.block()
		return nil
	}
	work, stop, ok := s.workContext(ctx, snap.ConfigRevision)
	if !ok {
		return nil
	}
	defer stop()
	job, err := s.repo.Claim(work, snap, s.owner, domain.UUID())
	if err != nil || job == nil {
		return err
	}
	// A fresh persisted read is mandatory immediately before the HTTP call.
	current, err := s.repo.Snapshot(work)
	if err != nil {
		s.block()
		return err
	}
	if !current.Enabled || current.ConfigRevision != snap.ConfigRevision || current.CollectionEpoch != job.CollectionEpoch || !s.now().Before(job.ExpiresAt) {
		return domain.ErrStale
	}
	if job.Payload == nil || len(*job.Payload) > domain.MaxPayload {
		return s.repo.Complete(work, snap, *job, "failed", "invalid_payload", s.now())
	}
	report, validationErr := domain.DecodeReport([]byte(*job.Payload))
	if validationErr != nil || report.ReportID != job.ReportID || report.InstallationID != job.InstallationID || report.CollectionEpoch != job.CollectionEpoch || report.ReportKind != job.ReportKind || (report.ReportKind == "daily" && report.BusinessDate != job.WindowKey) || (report.ReportKind == "installation" && job.WindowKey != "once") {
		return s.repo.Complete(work, snap, *job, "failed", "invalid_payload", s.now())
	}
	requestCtx, requestCancel := context.WithTimeout(work, s.cfg.Timeout)
	result, sendErr := s.sender.Send(requestCtx, s.cfg.Endpoint, []byte(*job.Payload))
	requestCancel()
	status, code, next := DeliveryDecision(result, sendErr, job.Attempts, s.now(), job.ExpiresAt)
	finishCtx, finishCancel := context.WithTimeout(ctx, 2*time.Second)
	defer finishCancel()
	return s.repo.Complete(finishCtx, snap, *job, status, code, next)
}

func DeliveryDecision(result SendResult, err error, attempts int, now, expiry time.Time) (string, string, time.Time) {
	if err == nil && (result.Status == 200 || result.Status == 201 || result.Status == 204) {
		return "sent", "", now
	}
	code := "network_error"
	if err == nil {
		code = "receiver_rejected"
		if result.Status != 408 && result.Status != 429 && (result.Status < 500 || result.Status > 599) {
			return "failed", code, now
		}
		code = "receiver_temporary"
	}
	delays := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	index := attempts - 1
	if index < 0 {
		index = 0
	}
	if index >= len(delays) {
		index = len(delays) - 1
	}
	delay := delays[index]
	hash := sha256.Sum256([]byte(domain.UUID()))
	delay += time.Duration(binary.BigEndian.Uint32(hash[:4])%uint32(delay/10/time.Millisecond+1)) * time.Millisecond
	if delay > 6*time.Hour {
		delay = 6 * time.Hour
	}
	if result.Status == 429 && result.RetryAfter > delay {
		delay = result.RetryAfter
	}
	next := now.Add(delay)
	if !next.Before(expiry) {
		return "expired", "report_expired", now
	}
	return "pending", code, next
}

func (s *Service) Cleanup(ctx context.Context) error {
	if s.repo == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Purge even on force-disable/invalid endpoint. Reading errors also purge pending bodies.
	snap, err := s.repo.Snapshot(ctx)
	disabled := s.reason() != "" || err != nil || !snap.Enabled
	if disabled {
		s.block()
	}
	return s.repo.Cleanup(ctx, s.reason() != "")
}

// MarshalReport is shared by the transactional enqueue path and protocol tests.
func MarshalReport(report domain.Report) ([]byte, error) {
	if err := report.Validate(); err != nil {
		return nil, domain.ErrInvalidPayload
	}
	body, err := json.Marshal(report)
	if err != nil || len(body) > domain.MaxPayload {
		return nil, domain.ErrInvalidPayload
	}
	return body, nil
}

// BuildVersion uses compiler-provided information, never arbitrary runtime environment strings.
func BuildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if version := domain.SafeVersion(info.Main.Version); version != "unknown" {
			return version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return domain.SafeVersion(setting.Value)
			}
		}
	}
	return "unknown"
}
