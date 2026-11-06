package application

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRedisURLFromEnv(t *testing.T) {
	for _, name := range []string{"BOS_REDIS_URL", "REDIS_HOST", "REDIS_PORT", "REDIS_DB", "REDIS_USERNAME", "REDIS_PASSWORD"} {
		t.Setenv(name, "")
	}
	t.Setenv("REDIS_HOST", "172.18.95.245")
	t.Setenv("REDIS_PORT", "6379")
	t.Setenv("REDIS_DB", "1")
	if got := redisURLFromEnv(); got != "redis://172.18.95.245:6379/1" {
		t.Fatalf("redis URL=%q", got)
	}
	t.Setenv("REDIS_USERNAME", "service user")
	t.Setenv("REDIS_PASSWORD", "p@ss:/word")
	parsed, err := url.Parse(redisURLFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	user := parsed.User.Username()
	password, _ := parsed.User.Password()
	if user != "service user" || password != "p@ss:/word" {
		t.Fatalf("credentials=%q,%q", user, password)
	}
	t.Setenv("BOS_REDIS_URL", "redis://override:6380/5")
	if got := redisURLFromEnv(); got != "redis://override:6380/5" {
		t.Fatalf("explicit URL=%q", got)
	}
}

func TestRedisURLMissingHost(t *testing.T) {
	t.Setenv("BOS_REDIS_URL", "")
	t.Setenv("REDIS_HOST", "")
	if got := redisURLFromEnv(); got != "" {
		t.Fatalf("redis URL=%q", got)
	}
}

func TestLoadConfigRequestBodyLimit(t *testing.T) {
	t.Setenv("BOS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("BOS_DATABASE_DSN", "")
	for _, tt := range []struct {
		name, value string
		want        int64
		invalid     bool
	}{
		{"default", "", 128 << 20, false},
		{"smaller", "20", 20 << 20, false},
		{"larger", "256", 256 << 20, false},
		{"whitespace", " 64 ", 64 << 20, false},
		{"zero", "0", 0, true},
		{"negative", "-1", 0, true},
		{"fraction", "1.5", 0, true},
		{"malformed", "128M", 0, true},
		{"overflow", "8796093022208", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BOS_MAX_REQUEST_BODY_MB", tt.value)
			got, err := LoadConfig()
			if (err != nil) != tt.invalid {
				t.Fatalf("error=%v, invalid=%v", err, tt.invalid)
			}
			if err == nil && got.MaxRequestBodyBytes != tt.want {
				t.Fatalf("limit=%d, want %d", got.MaxRequestBodyBytes, tt.want)
			}
		})
	}
}

func TestLoadConfigGatewayTimeout(t *testing.T) {
	t.Setenv("BOS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("BOS_DATABASE_DSN", "")
	for _, tt := range []struct {
		value   string
		want    time.Duration
		invalid bool
	}{
		{"", 300 * time.Second, false}, {"600", 600 * time.Second, false},
		{" 60 ", time.Minute, false}, {"0", 0, true}, {"-1", 0, true},
		{"1.5", 0, true}, {"300s", 0, true}, {"9223372037", 0, true},
	} {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("BOS_GATEWAY_TIMEOUT_SECONDS", tt.value)
			got, err := LoadConfig()
			if (err != nil) != tt.invalid {
				t.Fatalf("error=%v, invalid=%v", err, tt.invalid)
			}
			if err == nil && got.GatewayTimeout != tt.want {
				t.Fatalf("timeout=%s, want %s", got.GatewayTimeout, tt.want)
			}
		})
	}
}

func TestLoadConfigLoginLockPolicy(t *testing.T) {
	t.Setenv("BOS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("BOS_DATABASE_DSN", "")
	for _, tt := range []struct {
		name       string
		threshold  string
		window     string
		lock       string
		want       int
		wantWindow time.Duration
		wantLock   time.Duration
		invalid    bool
	}{
		{name: "default", threshold: "", window: "", lock: "", want: 5, wantWindow: 10 * time.Minute, wantLock: 15 * time.Minute},
		{name: "custom", threshold: "8", window: "1200", lock: "1800", want: 8, wantWindow: 20 * time.Minute, wantLock: 30 * time.Minute},
		{name: "minimum threshold", threshold: "2", window: "60", lock: "60", want: 2, wantWindow: time.Minute, wantLock: time.Minute},
		{name: "invalid threshold", threshold: "1", window: "600", lock: "900", invalid: true},
		{name: "invalid window", threshold: "5", window: "0", lock: "900", invalid: true},
		{name: "invalid lock", threshold: "5", window: "600", lock: "-1", invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BOS_LOGIN_FAILURE_THRESHOLD", tt.threshold)
			t.Setenv("BOS_LOGIN_FAILURE_WINDOW_SECONDS", tt.window)
			t.Setenv("BOS_LOGIN_LOCK_SECONDS", tt.lock)
			got, err := LoadConfig()
			if (err != nil) != tt.invalid {
				t.Fatalf("error=%v, invalid=%v", err, tt.invalid)
			}
			if err == nil && (got.LoginFailureThreshold != tt.want || got.LoginFailureWindow != tt.wantWindow || got.LoginLockDuration != tt.wantLock) {
				t.Fatalf("policy=%d/%s/%s, want %d/%s/%s", got.LoginFailureThreshold, got.LoginFailureWindow, got.LoginLockDuration, tt.want, tt.wantWindow, tt.wantLock)
			}
		})
	}
}
