package application

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	analyticsapp "bosapi/backend/internal/analytics/application"
)

// DefaultMaxRequestBodyBytes permits large AI contexts while keeping requests bounded.
const DefaultMaxRequestBodyBytes int64 = 128 << 20

// DefaultGatewayTimeout bounds upstream requests.
const DefaultGatewayTimeout = 300 * time.Second

// Safe login lockout defaults permit a small number of human mistakes before
// locking and keep each temporary lock long enough to slow brute force.
const (
	DefaultLoginFailureThreshold = 5
	DefaultLoginFailureWindow    = 10 * time.Minute
	DefaultLoginLockDuration     = 15 * time.Minute
)

// Config contains gateway startup settings shared by lifecycle and adapters.
type Config struct {
	UsageReporting                                 analyticsapp.Config
	GatewayTimeout                                 time.Duration
	MaxRequestBodyBytes                            int64
	DataFile, DatabaseDSN, JWTSigningKey, RedisURL string
	LoginFailureThreshold                          int
	LoginFailureWindow, LoginLockDuration          time.Duration
	EncryptionKey                                  []byte
	Listen, CallbackListen                         string
}

// LoadConfig reads and validates gateway settings from environment variables.
func LoadConfig() (Config, error) {
	config := Config{
		UsageReporting: UsageReportingConfigFromEnv(),
		DataFile:       env("BOS_DATA_FILE", "./gateway.enc"),
		DatabaseDSN:    os.Getenv("BOS_DATABASE_DSN"),
		JWTSigningKey:  os.Getenv("BOS_JWT_SIGNING_KEY"),
		RedisURL:       redisURLFromEnv(),
		Listen:         env("BOS_LISTEN", "127.0.0.1:8081"),
		CallbackListen: env("BOS_CALLBACK_LISTEN", "127.0.0.1:1455"),
	}
	limit, err := requestBodyLimitFromEnv()
	if err != nil {
		return Config{}, err
	}
	config.MaxRequestBodyBytes = limit
	config.GatewayTimeout, err = gatewayTimeoutFromEnv()
	if err != nil {
		return Config{}, err
	}
	config.LoginFailureThreshold, config.LoginFailureWindow, config.LoginLockDuration, err = loginLockPolicyFromEnv()
	if err != nil {
		return Config{}, err
	}
	keyValue := os.Getenv("BOS_ENCRYPTION_KEY")
	if keyValue == "" {
		keyValue = os.Getenv("GATEWAY_ENCRYPTION_KEY")
	}
	key, err := base64.StdEncoding.DecodeString(keyValue)
	if err != nil {
		key, err = hex.DecodeString(keyValue)
	}
	if err != nil || len(key) != 32 {
		return Config{}, errors.New("BOS_ENCRYPTION_KEY must be a base64 encoded 32 byte key")
	}
	if config.DatabaseDSN != "" && len(config.JWTSigningKey) < 32 {
		return Config{}, errors.New("BOS_JWT_SIGNING_KEY must contain at least 32 bytes")
	}
	config.EncryptionKey = key
	return config, nil
}

func redisURLFromEnv() string {
	if configured := strings.TrimSpace(os.Getenv("BOS_REDIS_URL")); configured != "" {
		return configured
	}
	host := strings.TrimSpace(os.Getenv("REDIS_HOST"))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(os.Getenv("REDIS_PORT"))
	if port == "" {
		port = "6379"
	}
	db := strings.TrimSpace(os.Getenv("REDIS_DB"))
	if db == "" {
		db = "0"
	}
	if _, err := strconv.Atoi(db); err != nil {
		return ""
	}
	user := os.Getenv("REDIS_USERNAME")
	password := os.Getenv("REDIS_PASSWORD")
	userinfo := ""
	if user != "" {
		userinfo = url.UserPassword(user, password).String() + "@"
	} else if password != "" {
		userinfo = ":" + url.QueryEscape(password) + "@"
	}
	return "redis://" + userinfo + host + ":" + port + "/" + db
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func requestBodyLimitFromEnv() (int64, error) {
	raw := strings.TrimSpace(os.Getenv("BOS_MAX_REQUEST_BODY_MB"))
	if raw == "" {
		return DefaultMaxRequestBodyBytes, nil
	}
	megabytes, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || megabytes <= 0 || megabytes > math.MaxInt64>>20 {
		return 0, fmt.Errorf("bos_max_request_body_mb must be a positive integer no greater than %d", math.MaxInt64>>20)
	}
	return megabytes << 20, nil
}

func gatewayTimeoutFromEnv() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("BOS_GATEWAY_TIMEOUT_SECONDS"))
	if raw == "" {
		return DefaultGatewayTimeout, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
		return 0, errors.New("BOS_GATEWAY_TIMEOUT_SECONDS must be a positive integer within the supported duration range")
	}
	return time.Duration(seconds) * time.Second, nil
}

func loginLockPolicyFromEnv() (int, time.Duration, time.Duration, error) {
	threshold := DefaultLoginFailureThreshold
	if raw := strings.TrimSpace(os.Getenv("BOS_LOGIN_FAILURE_THRESHOLD")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 2 || parsed > 100 {
			return 0, 0, 0, errors.New("BOS_LOGIN_FAILURE_THRESHOLD must be an integer from 2 to 100")
		}
		threshold = parsed
	}

	window, err := durationRangeFromEnv("BOS_LOGIN_FAILURE_WINDOW_SECONDS", DefaultLoginFailureWindow, time.Minute, 24*time.Hour)
	if err != nil {
		return 0, 0, 0, err
	}
	lockDuration, err := durationRangeFromEnv("BOS_LOGIN_LOCK_SECONDS", DefaultLoginLockDuration, time.Minute, 24*time.Hour)
	if err != nil {
		return 0, 0, 0, err
	}
	return threshold, window, lockDuration, nil
}

func durationRangeFromEnv(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds < int64(minimum/time.Second) || seconds > int64(maximum/time.Second) {
		return 0, fmt.Errorf("%s must be an integer from %d to %d", name, int64(minimum/time.Second), int64(maximum/time.Second))
	}
	return time.Duration(seconds) * time.Second, nil
}
