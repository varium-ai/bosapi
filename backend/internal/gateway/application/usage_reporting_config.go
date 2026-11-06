package application

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	analytics "bosapi/backend/internal/analytics/application"
)

// The maintainer sets the fixed receiving URL in this file before building.
// Deployments cannot override it using an environment variable or admin request.
//
//go:embed usage_reporting.json
var usageReportingDefaults []byte

// Invalid telemetry configuration disables only telemetry, never gateway startup.
func UsageReportingConfigFromEnv() analytics.Config {
	return usageReportingConfig(usageReportingDefaults)
}

func usageReportingConfig(defaults []byte) analytics.Config {
	c := analytics.Config{Timeout: 5 * time.Second}
	var file struct {
		Endpoint *string `json:"endpoint"`
	}
	decoder := json.NewDecoder(bytes.NewReader(defaults))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&file) != nil || file.Endpoint == nil || decoder.Decode(new(any)) != io.EOF {
		c.InvalidReason = "invalid_endpoint"
	} else {
		c.Endpoint = *file.Endpoint
	}
	if raw := os.Getenv("BOS_USAGE_REPORTING_FORCE_DISABLED"); raw != "" {
		if raw != "true" && raw != "false" {
			c.InvalidReason = "invalid_force_disabled"
		} else {
			c.ForceDisabled = raw == "true"
		}
	}
	if raw := os.Getenv("BOS_USAGE_REPORTING_HTTP_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Second || d > 10*time.Second {
			c.InvalidReason = "invalid_timeout"
		} else {
			c.Timeout = d
		}
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(c.Endpoint, "#") || strings.TrimSpace(c.Endpoint) != c.Endpoint {
			c.InvalidReason = "invalid_endpoint"
		}
	}
	return c
}
