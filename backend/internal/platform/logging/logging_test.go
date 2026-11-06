package logging

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"bosapi/backend/internal/platform/httpapi"
)

type testSink struct{ records []Record }

func (sink *testSink) Append(_ context.Context, record Record) error {
	sink.records = append(sink.records, record)
	return nil
}

func TestEventPersistsRequestMetadataAndRedactsSecrets(t *testing.T) {
	previousWriter := log.Writer()
	var output bytes.Buffer
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousWriter); SetSink(nil) })

	sink := &testSink{}
	SetSink(sink)
	ctx := httpapi.WithRequestID(context.Background(), "req_trace-123")
	ctx = httpapi.WithLogMetadata(ctx, httpapi.LogMetadata{
		RequestID: "req_trace-123", ClientIP: "192.0.2.10", OrganizationID: 27, APIKeyMasked: "sk-gw••••••",
	})
	Event(ctx, "error", "gateway.test", "Authorization: Bearer sk-gw-credential otp=123456 api_key=bos_live_sensitive dsn=user:db-password@tcp(localhost:3306)/bos\nfailed")

	if len(sink.records) != 1 {
		t.Fatalf("records=%d, want 1", len(sink.records))
	}
	record := sink.records[0]
	if record.Level != "ERROR" || record.Event != "gateway.test" || record.RequestID != "req_trace-123" || record.ClientIP != "192.0.2.10" || record.OrganizationID != 27 || record.APIKeyMasked != "sk-gw••••••" {
		t.Fatalf("unexpected record metadata: %+v", record)
	}
	logOutput := strings.TrimSuffix(output.String(), "\n")
	if strings.Contains(record.Message, "\n") || strings.Contains(logOutput, "\n") {
		t.Fatalf("log contains an embedded line break: %q", logOutput)
	}
	for _, value := range []string{record.Message, logOutput} {
		for _, secret := range []string{"sk-gw-credential", "bos_live_sensitive", "123456", "db-password"} {
			if strings.Contains(value, secret) {
				t.Fatalf("secret %q was retained in %q", secret, value)
			}
		}
	}
}
