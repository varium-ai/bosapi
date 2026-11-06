package application

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestInvalidUsageReportingConfigurationDoesNotBlockStartup(t *testing.T) {
	t.Setenv("BOS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("BOS_DATABASE_DSN", "")
	t.Setenv("BOS_USAGE_REPORTING_FORCE_DISABLED", "invalid")
	config, err := LoadConfig()
	if err != nil || config.UsageReporting.InvalidReason == "" {
		t.Fatalf("config=%+v err=%v", config.UsageReporting, err)
	}
}

func TestUsageReportingConfigDefaultsAndStrictFailures(t *testing.T) {
	for _, name := range []string{"BOS_USAGE_REPORTING_FORCE_DISABLED", "BOS_USAGE_REPORTING_HTTP_TIMEOUT"} {
		t.Setenv(name, "")
	}
	defaults := UsageReportingConfigFromEnv()
	if defaults.ForceDisabled || defaults.Timeout != 5*time.Second || defaults.InvalidReason != "" {
		t.Fatalf("%+v", defaults)
	}
	for _, tc := range []struct {
		name, value string
		invalid     bool
	}{
		{"BOS_USAGE_REPORTING_FORCE_DISABLED", "true", false}, {"BOS_USAGE_REPORTING_FORCE_DISABLED", "1", true}, {"BOS_USAGE_REPORTING_FORCE_DISABLED", "TRUE", true},
		{"BOS_USAGE_REPORTING_HTTP_TIMEOUT", "1s", false}, {"BOS_USAGE_REPORTING_HTTP_TIMEOUT", "10s", false}, {"BOS_USAGE_REPORTING_HTTP_TIMEOUT", "999ms", true}, {"BOS_USAGE_REPORTING_HTTP_TIMEOUT", "11s", true}, {"BOS_USAGE_REPORTING_HTTP_TIMEOUT", "garbage", true},
	} {
		t.Run(tc.name+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			got := UsageReportingConfigFromEnv()
			if (got.InvalidReason != "") != tc.invalid {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestUsageReportingEndpointIsEmbeddedAndNotEnvironmentControlled(t *testing.T) {
	t.Setenv("BOS_USAGE_REPORTING_FORCE_DISABLED", "")
	t.Setenv("BOS_USAGE_REPORTING_HTTP_TIMEOUT", "")
	baseline := UsageReportingConfigFromEnv()
	t.Setenv("BOS_USAGE_REPORTING_ENDPOINT", "https://unexpected-receiver.invalid")
	actual := UsageReportingConfigFromEnv()
	if actual.Endpoint != baseline.Endpoint || actual.InvalidReason != baseline.InvalidReason {
		t.Fatal("environment overrode the fixed receiving URL")
	}
	for _, tc := range []struct {
		source  string
		invalid bool
	}{
		{`{"endpoint":""}`, false},
		{`{"endpoint":"https://receiver.example/v1/usage-reports"}`, false},
		{`{"endpoint":"http://receiver.example"}`, true},
		{`{"endpoint":"https://user:secret@receiver.example"}`, true},
		{`{"endpoint":"https://receiver.example?"}`, true},
		{`{"endpoint":"https://receiver.example#"}`, true},
		{`{"endpoint":"https://receiver.example?token=secret"}`, true},
		{`{"endpoint":"https:///path"}`, true},
		{`{}`, true}, {`{"endpoint":null}`, true}, {`{"endpoint":1}`, true}, {`{"endpoint":"","extra":1}`, true}, {`{"endpoint":""}{}`, true},
	} {
		got := usageReportingConfig([]byte(tc.source))
		if (got.InvalidReason != "") != tc.invalid {
			t.Fatalf("unexpected embedded configuration validation: %v", got.InvalidReason)
		}
	}
}
