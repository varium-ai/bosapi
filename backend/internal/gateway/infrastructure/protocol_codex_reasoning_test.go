package infrastructure

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexChatReasoningEffort(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`"none"`, "none"}, {`"minimal"`, "minimal"}, {`"low"`, "low"}, {`"medium"`, "medium"}, {`"high"`, "high"}, {`"xhigh"`, "xhigh"}, {`"max"`, "max"}, {`null`, ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			raw := `{"model":"x","messages":[{"role":"user","content":"test"}],"reasoning_effort":` + tc.input + `}`
			q, err := ParseRelayRequest([]byte(raw), "chat", "openai_responses")
			if err != nil {
				t.Fatal(err)
			}
			result, err := q.CodexUpstream()
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err = json.Unmarshal(result, &body); err != nil {
				t.Fatal(err)
			}
			if _, exists := body["reasoning_effort"]; exists {
				t.Fatal("Chat field leaked")
			}
			if tc.want == "" {
				if _, exists := body["reasoning"]; exists {
					t.Fatalf("unexpected reasoning: %s", result)
				}
				return
			}
			reasoning, ok := body["reasoning"].(map[string]any)
			if !ok || reasoning["effort"] != tc.want {
				t.Fatalf("wrong mapping: %s", result)
			}
		})
	}
	for _, value := range []string{`{}`, `12`, `""`, `"extreme"`} {
		raw := `{"model":"x","messages":[{"role":"user","content":"test"}],"reasoning_effort":` + value + `}`
		if _, err := ParseRelayRequest([]byte(raw), "chat", "openai_responses"); err == nil || !strings.Contains(err.Error(), "reasoning_effort") {
			t.Fatalf("invalid value %s: %v", value, err)
		}
	}
	for _, target := range []string{"anthropic_messages", "gemini"} {
		raw := `{"model":"x","messages":[{"role":"user","content":"test"}],"reasoning_effort":"high"}`
		if _, err := ParseRelayRequest([]byte(raw), "chat", target); err == nil || !strings.Contains(err.Error(), "reasoning_effort") {
			t.Fatalf("%s accepted unsupported effort: %v", target, err)
		}
	}
}
