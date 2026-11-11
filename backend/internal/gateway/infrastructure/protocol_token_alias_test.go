package infrastructure

import (
	"strings"
	"testing"
)

func TestChatTokenLimitAlias(t *testing.T) {
	for _, target := range []string{"anthropic_messages", "gemini"} {
		for _, key := range []string{"max_tokens", "max_completion_tokens"} {
			request := `{"model":"x","messages":[{"role":"user","content":"hi"}],"` + key + `":42}`
			q, err := ParseRelayRequest([]byte(request), "chat", target)
			if err != nil {
				t.Fatal(err)
			}
			raw, _, err := q.Upstream(target)
			if err != nil || !strings.Contains(string(raw), "42") {
				t.Fatalf("%s %s: %s %v", target, key, raw, err)
			}
		}
		_, err := ParseRelayRequest([]byte(`{"model":"x","messages":[{"role":"user","content":"hi"}],"max_tokens":10,"max_completion_tokens":20}`), "chat", target)
		if err == nil || !strings.Contains(err.Error(), "max_completion_tokens") {
			t.Fatalf("missing conflict: %v", err)
		}
		_, err = ParseRelayRequest([]byte(`{"model":"x","messages":[{"role":"user","content":"hi"}],"max_tokens":10,"max_completion_tokens":{}}`), "chat", target)
		if err == nil || !strings.Contains(err.Error(), "max_completion_tokens") {
			t.Fatalf("invalid alias accepted: %v", err)
		}
	}
}
