// Package logging provides the shared, redacting application logger.
package logging

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"bosapi/backend/internal/platform/httpapi"
)

// Record is the safe persisted representation of one application event.
type Record struct {
	CreatedAt      time.Time
	Level          string
	Event          string
	Message        string
	RequestID      string
	ClientIP       string
	OrganizationID uint64
	APIKeyMasked   string
}

// Sink persists application events so administrators can inspect a request chain.
type Sink interface {
	Append(context.Context, Record) error
}

var (
	sinkMu sync.RWMutex
	sink   Sink

	credentialPattern = regexp.MustCompile(`(?i)\b(?:sk-gw-|bos_live_)[A-Za-z0-9._:-]{4,}`)
	bearerPattern     = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;]+`)
	secretPattern     = regexp.MustCompile(`(?i)\b(password|passwd|otp|mfa_token|access_token|refresh_token|api_key|client_secret|secret|credential|token|private_key)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
	dsnSecretPattern  = regexp.MustCompile(`(?i)(://[^:/\s]+:|\b[^@\s:/]+:)[^@/\s]+@((?:tcp|unix)\(|[^\s/]*)`)
)

// SetSink configures the persistence target used by all application log events.
func SetSink(value Sink) {
	sinkMu.Lock()
	sink = value
	sinkMu.Unlock()
}

// Event writes a structured event to stdout and the configured persistence sink.
func Event(ctx context.Context, level, event, message string) {
	if ctx == nil {
		ctx = context.Background()
	}
	metadata := httpapi.LogMetadataFromContext(ctx)
	level = strings.ToUpper(strings.TrimSpace(level))
	if level == "" {
		level = "INFO"
	}
	event = clean(event, 128)
	message = Sanitize(message, 2048)
	record := Record{
		CreatedAt:      time.Now().UTC(),
		Level:          level,
		Event:          event,
		Message:        message,
		RequestID:      clean(metadata.RequestID, 128),
		ClientIP:       clean(metadata.ClientIP, 64),
		OrganizationID: metadata.OrganizationID,
		APIKeyMasked:   clean(metadata.APIKeyMasked, 128),
	}

	Fallback(ctx, record.Level, record.Event, record.Message)

	sinkMu.RLock()
	currentSink := sink
	sinkMu.RUnlock()
	if currentSink != nil {
		writeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := currentSink.Append(writeContext, record); err != nil {
			Fallback(ctx, "ERROR", "logging.sink.enqueue_failed", fmt.Sprintf("err=%s", Sanitize(err.Error(), 1024)))
		}
	}
}

// Fallback writes one sanitized event to stdout without sending it to the persistence sink.
// It is used to report sink failures without recursively attempting to persist them.
func Fallback(ctx context.Context, level, event, message string) {
	if ctx == nil {
		ctx = context.Background()
	}
	metadata := httpapi.LogMetadataFromContext(ctx)
	level = strings.ToUpper(strings.TrimSpace(level))
	if level == "" {
		level = "INFO"
	}
	log.Printf("level=%s event=%s request_id=%s ip=%s organization_id=%d api_key_masked=%s %s",
		level, clean(event, 128), value(clean(metadata.RequestID, 128)), value(clean(metadata.ClientIP, 64)), metadata.OrganizationID, value(clean(metadata.APIKeyMasked, 128)), Sanitize(message, 2048))
}

// Printf formats and writes an application event through the shared logger.
func Printf(ctx context.Context, level, event, format string, args ...any) {
	Event(ctx, level, event, fmt.Sprintf(format, args...))
}

// Sanitize removes credentials and line breaks from free-form log data.
func Sanitize(value string, maxBytes int) string {
	value = dsnSecretPattern.ReplaceAllString(value, "${1}[REDACTED]@${2}")
	value = credentialPattern.ReplaceAllString(value, "[REDACTED_KEY]")
	value = bearerPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = secretPattern.ReplaceAllString(value, "${1}${2}[REDACTED]")
	return clean(value, maxBytes)
}

func clean(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if maxBytes > 0 && len(value) > maxBytes {
		value = value[:maxBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func value(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
