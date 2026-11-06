package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStrictPersistedSetting(t *testing.T) {
	for _, tc := range []struct {
		raw              string
		enabled, invalid bool
	}{
		{"", false, false}, {`{"enabled":false}`, false, false}, {`{"enabled":true,"updated_at":"2026-10-20T00:00:00Z"}`, true, false},
		{`{}`, false, true}, {`null`, false, true}, {`{"enabled":null}`, false, true}, {`{"enabled":"true"}`, false, true}, {`{"enabled":true,"extra":1}`, false, true}, {`{"enabled":true} {}`, false, true},
	} {
		got, err := DecodeEnabled(tc.raw)
		if got != tc.enabled || (err != nil) != tc.invalid {
			t.Fatalf("raw=%s got=%v err=%v", tc.raw, got, err)
		}
	}
}

func TestMinimalReportProtocolAndHeartbeatCalendar(t *testing.T) {
	now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
	installation := Installation(Metadata{Version: "1.2.3", OS: "linux", Arch: "arm64"}, now)
	if err := installation.Validate(); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(installation)
	var fields map[string]any
	_ = json.Unmarshal(body, &fields)
	if len(fields) != 9 {
		t.Fatal("non-minimal installation", fields)
	}
	for _, key := range []string{"usage", "business_date", "timezone", "window_start", "window_end", "partial"} {
		if _, ok := fields[key]; ok {
			t.Fatal("forbidden field", key)
		}
	}
	daily := Daily(Metadata{Version: "1.2.3", OS: "linux", Arch: "arm64"}, State{InstallationID: installation.InstallationID, CollectionEpoch: installation.CollectionEpoch}, now)
	if daily.BusinessDate != "2026-10-19" || daily.Timezone != "UTC" || daily.Validate() != nil {
		t.Fatal(daily)
	}
	encoded, _ := json.Marshal(daily)
	if _, err := DecodeReport(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatUsesUTCDateAcrossCalendarBoundaries(t *testing.T) {
	meta := Metadata{Version: "1.2.3", OS: "linux", Arch: "amd64"}
	state := State{InstallationID: UUID(), CollectionEpoch: UUID()}
	for _, tc := range []struct{ instant, date string }{
		{"2026-10-20T00:00:00+08:00", "2026-10-19"},
		{"2026-10-20T07:59:59+08:00", "2026-10-19"},
		{"2026-10-20T08:00:00+08:00", "2026-10-20"},
		{"2027-01-01T07:59:59+08:00", "2026-12-31"},
		{"2027-01-01T08:00:00+08:00", "2027-01-01"},
	} {
		t.Run(tc.instant, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.instant)
			if err != nil {
				t.Fatal(err)
			}
			r := Daily(meta, state, now)
			if r.BusinessDate != tc.date || r.Timezone != "UTC" || r.Validate() != nil {
				t.Fatalf("incorrect UTC report: %+v", r)
			}
			midnight, _ := time.Parse(time.RFC3339, tc.date+"T00:00:00Z")
			if !DayStart(now).Equal(midnight) {
				t.Fatal("incorrect UTC day start", DayStart(now))
			}
			r.BusinessDate = midnight.AddDate(0, 0, 1).Format(time.DateOnly)
			if r.Validate() == nil {
				t.Fatal("accepted a date outside the UTC day")
			}
			r.BusinessDate, r.Timezone = tc.date, "Asia/Shanghai"
			body, _ := json.Marshal(r)
			if _, err := DecodeReport(body); err == nil {
				t.Fatal("accepted an old-calendar payload")
			}
		})
	}
}

func TestWhitelistRejectsContentCredentialsAndAmbiguousJSON(t *testing.T) {
	report := Installation(Metadata{Version: "1.2.3", OS: "linux", Arch: "amd64"}, time.Now())
	valid, _ := json.Marshal(report)
	for _, extra := range []string{`"prompt":"private prompt"`, `"content":"private response"`, `"api_key":"sk-test-full-credential"`, `"usage":{"record_count":1}`, `"Schema_Version":2`, `"app_version":"1.2.3"`} {
		body := strings.TrimSuffix(string(valid), "}") + "," + extra + "}"
		if _, err := DecodeReport([]byte(body)); err == nil {
			t.Fatal("unsafe payload accepted", extra)
		}
	}
	for _, version := range []string{"sk-test-full-credential", "Bearer full-access-token", "private user text", "https://user:secret@example.invalid", "1.2.3-secret-content"} {
		r := Installation(Metadata{Version: version, OS: "linux", Arch: "amd64"}, time.Now())
		if r.AppVersion != "unknown" {
			t.Fatal("free-form version retained")
		}
		r.AppVersion = version
		if r.Validate() == nil {
			t.Fatal("credential-like version accepted")
		}
	}
	report.OS = "private hostname"
	if report.Validate() == nil {
		t.Fatal("free-form platform accepted")
	}
	if _, err := DecodeReport(append(valid, []byte(`{}`)...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}
