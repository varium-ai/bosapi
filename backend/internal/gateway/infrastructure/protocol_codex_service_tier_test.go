package infrastructure

import (
	"errors"
	"testing"
)

func TestCodexChatServiceTierConversion(t *testing.T) {
	const base = `{"model":"alias","messages":[{"role":"user","content":"hello"}]`
	for _, tc := range []struct {
		name, value, want string
		invalid           bool
	}{
		{name: "omitted"},
		{name: "null", value: "null"},
		{name: "default", value: `"default"`, want: "default"},
		{name: "fast", value: `"fast"`, want: "fast"},
		{name: "invalid tier", value: `"unknown"`, invalid: true},
		{name: "invalid type", value: "42", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := base
			if tc.value != "" {
				body += `,"service_tier":` + tc.value
			}
			q, err := ParseRelayRequest([]byte(body+"}"), "chat", "openai_responses")
			if tc.invalid {
				var failure *ConversionError
				if !errors.As(err, &failure) || failure.Field != "service_tier" {
					t.Fatalf("expected service_tier conversion error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := q.CodexUpstream()
			if err != nil {
				t.Fatal(err)
			}
			upstream, err := DecodeProtocolJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, exists := upstream["service_tier"]
			if exists != (tc.want != "") || exists && got != tc.want {
				t.Fatalf("upstream service_tier = %v (present: %v), want %q", got, exists, tc.want)
			}
		})
	}
}
