// Package domain defines the deliberately small, versioned usage-reporting contract.
package domain

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const SettingKey = "analytics.usage_reporting"
const MaxPayload = 16 << 10

var ErrInvalidPayload = errors.New("usage_reporting_invalid_payload")

var ErrUnavailable = errors.New("usage_reporting_unavailable")
var ErrConflict = errors.New("usage_reporting_conflict")
var ErrStale = errors.New("usage_reporting_stale")
var ErrRateLimited = errors.New("usage_reporting_preview_rate_limited")

// ReservedSetting also reserves future identity/configuration keys in this namespace.
func ReservedSetting(key string) bool {
	// settings uses a case/accent-insensitive MySQL primary key. Reject non-ASCII
	// generic write keys so Unicode aliases cannot overwrite a protected key.
	for _, r := range key {
		if r > 127 {
			return true
		}
	}
	key = strings.ToLower(strings.TrimSpace(key))
	return key == SettingKey || strings.HasPrefix(key, SettingKey+".")
}

func UUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random source unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Report is the entire outbound allowlist. Never embed usage rows or status snapshots.
type Report struct {
	SchemaVersion   int       `json:"schema_version"`
	ReportID        string    `json:"report_id"`
	InstallationID  string    `json:"installation_id"`
	CollectionEpoch string    `json:"collection_epoch"`
	ReportKind      string    `json:"report_kind"`
	GeneratedAt     time.Time `json:"generated_at"`
	BusinessDate    string    `json:"business_date,omitempty"`
	Timezone        string    `json:"timezone,omitempty"`
	AppVersion      string    `json:"app_version"`
	OS              string    `json:"os"`
	Arch            string    `json:"arch"`
}

type Metadata struct{ Version, OS, Arch string }

// DayStart returns the UTC midnight used by installation telemetry, independently
// of the business calendar used for usage and quotas.
func DayStart(now time.Time) time.Time {
	return now.UTC().Truncate(24 * time.Hour)
}

func Installation(meta Metadata, now time.Time) Report {
	return Report{SchemaVersion: 2, ReportID: UUID(), InstallationID: UUID(), CollectionEpoch: UUID(), ReportKind: "installation", GeneratedAt: now.UTC(), AppVersion: SafeVersion(meta.Version), OS: meta.OS, Arch: meta.Arch}
}

// Daily is a current-day running-instance heartbeat, not a business-use report.
func Daily(meta Metadata, state State, now time.Time) Report {
	r := Installation(meta, now)
	r.InstallationID, r.CollectionEpoch, r.ReportKind = state.InstallationID, state.CollectionEpoch, "daily"
	r.BusinessDate, r.Timezone = now.UTC().Format("2006-01-02"), "UTC"
	return r
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var versionPattern = regexp.MustCompile(`^(v?[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}|[0-9a-f]{40}|[0-9a-f]{64})$`)

// Free-form runtime/environment values cannot be used to carry secrets as a version.
func SafeVersion(value string) string {
	if versionPattern.MatchString(value) {
		return value
	}
	return "unknown"
}

func (r Report) Validate() error {
	if r.SchemaVersion != 2 || !uuidPattern.MatchString(r.ReportID) || !uuidPattern.MatchString(r.InstallationID) || !uuidPattern.MatchString(r.CollectionEpoch) || r.GeneratedAt.IsZero() || r.GeneratedAt.Location() != time.UTC || SafeVersion(r.AppVersion) != r.AppVersion {
		return ErrInvalidPayload
	}
	switch r.OS {
	case "linux", "darwin", "windows", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos", "aix", "android", "ios", "plan9", "js", "wasip1":
	default:
		return ErrInvalidPayload
	}
	switch r.Arch {
	case "386", "amd64", "arm", "arm64", "loong64", "mips", "mipsle", "mips64", "mips64le", "ppc64", "ppc64le", "riscv64", "s390x", "wasm":
	default:
		return ErrInvalidPayload
	}
	switch r.ReportKind {
	case "installation":
		if r.BusinessDate != "" || r.Timezone != "" {
			return ErrInvalidPayload
		}
	case "daily":
		if r.Timezone != "UTC" || r.BusinessDate != r.GeneratedAt.UTC().Format("2006-01-02") {
			return ErrInvalidPayload
		}
	default:
		return ErrInvalidPayload
	}
	return nil
}

// DecodeReport validates the exact JSON key spelling, rejects duplicate/unknown
// fields and permits only scalar values. It is also used before sending old jobs.
func DecodeReport(body []byte) (Report, error) {
	if len(body) > MaxPayload {
		return Report{}, ErrInvalidPayload
	}
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return Report{}, ErrInvalidPayload
	}
	allowed := map[string]bool{"schema_version": true, "report_id": true, "installation_id": true, "collection_epoch": true, "report_kind": true, "generated_at": true, "business_date": true, "timezone": true, "app_version": true, "os": true, "arch": true}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return Report{}, ErrInvalidPayload
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || seen[key] {
			return Report{}, ErrInvalidPayload
		}
		seen[key] = true
		token, err = d.Token()
		if err != nil || token == nil {
			return Report{}, ErrInvalidPayload
		}
		if _, object := token.(json.Delim); object {
			return Report{}, ErrInvalidPayload
		}
	}
	if _, err = d.Token(); err != nil {
		return Report{}, ErrInvalidPayload
	}
	if d.Decode(new(any)) != io.EOF {
		return Report{}, ErrInvalidPayload
	}
	var r Report
	if json.Unmarshal(body, &r) != nil || r.Validate() != nil {
		return Report{}, ErrInvalidPayload
	}
	return r, nil
}

// DecodeEnabled fails closed for malformed/missing booleans; an absent setting is disabled.
func DecodeEnabled(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	var value struct {
		Enabled   *bool     `json:"enabled"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil {
		return false, ErrUnavailable
	}
	if value.Enabled == nil {
		return false, ErrUnavailable
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return false, ErrUnavailable
	}
	return *value.Enabled, nil
}

type State struct {
	ID              uint `gorm:"primaryKey"`
	InstallationID  string
	CollectionEpoch string
	EnabledSince    *time.Time
	ConfigRevision  uint64
	ScheduleOwner   string
	ScheduleToken   string
	ScheduleUntil   *time.Time
	NextScanAt      *time.Time
	ScanCursor      *time.Time
	LastSuccessAt   *time.Time
	LastFailureAt   *time.Time
	LastError       string
	DroppedCount    uint64
}

func (State) TableName() string { return "usage_reporting_state" }

type Snapshot struct {
	State
	Enabled    bool
	Configured bool
}
type Job struct {
	ReportID        string `gorm:"primaryKey"`
	InstallationID  string
	CollectionEpoch string
	ReportKind      string
	WindowKey       string
	Payload         *string
	PayloadHash     string
	Status          string
	Attempts        int
	NextAttemptAt   time.Time
	LeaseOwner      string
	LeaseToken      string
	LeaseUntil      *time.Time
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

func (Job) TableName() string { return "usage_reporting_outbox" }

type Repository interface {
	InitializeDefault(context.Context, Report) error
	Snapshot(context.Context) (Snapshot, error)
	Toggle(context.Context, bool, *uint64, Report) (Snapshot, error)
	Reset(context.Context, *uint64, time.Time) error
	AcquireSchedule(context.Context, Snapshot, string, string) (bool, error)
	CommitWindow(context.Context, Snapshot, string, *Report, time.Time, time.Time, string) error
	Claim(context.Context, Snapshot, string, string) (*Job, error)
	Complete(context.Context, Snapshot, Job, string, string, time.Time) error
	Cleanup(context.Context, bool) error
	Pending(context.Context) (int64, *time.Time, error)
}
