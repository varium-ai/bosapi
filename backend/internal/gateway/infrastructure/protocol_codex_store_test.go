package infrastructure

import (
	"strings"
	"testing"
)

func TestCodexChatStoreCompatibility(t *testing.T) {
	for _, value := range []string{"false", "null"} {
		raw := `{"model":"x","store":` + value + `,"messages":[{"role":"user","content":"hi"}]}`
		q, err := ParseRelayRequest([]byte(raw), "chat", "openai_responses")
		if value == "null" {
			if err == nil || !strings.Contains(err.Error(), "store") {
				t.Fatalf("null store accepted: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		upstream, err := q.CodexUpstream()
		if err != nil || !strings.Contains(string(upstream), `"store":false`) {
			t.Fatalf("stateless store=false was not set upstream: %s %v", upstream, err)
		}
	}
	for _, value := range []string{"true", `"false"`, `{}`} {
		raw := `{"model":"x","store":` + value + `,"messages":[{"role":"user","content":"hi"}]}`
		_, err := ParseRelayRequest([]byte(raw), "chat", "openai_responses")
		if err == nil || !strings.Contains(err.Error(), "store") {
			t.Fatalf("invalid store accepted: %s %v", value, err)
		}
	}
}
