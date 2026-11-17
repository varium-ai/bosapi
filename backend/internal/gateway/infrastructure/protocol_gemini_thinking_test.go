package infrastructure

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGeminiThinkingControls(t *testing.T) {
	for _, tc := range []struct {
		target, config, want string
	}{
		{"openai_chat", `{"thinkingBudget":0}`, `"thinking":{"type":"disabled"}`},
		{"openai_chat", `{"thinkingBudget":-1}`, `"thinking":{"type":"enabled"}`},
		{"openai_chat", `{"thinkingBudget":2048}`, `"thinking":{"type":"enabled"}`},
		{"", `{"thinkingLevel":"HIGH"}`, `"reasoning_effort":"high"`},
		{"openai_chat", `{"thinkingLevel":"minimal","includeThoughts":true}`, `"reasoning_effort":"minimal"`},
		{"anthropic_messages", `{"thinkingBudget":0}`, `"thinking":{"type":"disabled"}`},
		{"anthropic_messages", `{"thinkingBudget":-1}`, `"thinking":{"type":"adaptive"}`},
		{"anthropic_messages", `{"thinkingBudget":2048}`, `"thinking":{"budget_tokens":2048,"type":"enabled"}`},
		{"anthropic_messages", `{"thinkingLevel":"low"}`, `"output_config":{"effort":"low"}`},
	} {
		t.Run(tc.target+tc.config, func(t *testing.T) {
			raw := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":` + tc.config + `}}`
			q, err := ParseRelayRequest([]byte(raw), "gemini", tc.target)
			if err != nil {
				t.Fatal(err)
			}
			if tc.target == "openai_chat" && tc.config == `{"thinkingBudget":2048}` {
				if q.GeminiThinkingBudget != 2048 {
					t.Fatalf("requested budget missing from diagnostic metadata: %d", q.GeminiThinkingBudget)
				}
			} else if q.GeminiThinkingBudget != 0 {
				t.Fatalf("unexpected advisory budget: %d", q.GeminiThinkingBudget)
			}
			body, _, err := q.Upstream(tc.target)
			if err != nil || !strings.Contains(string(body), tc.want) || strings.Contains(string(body), "thinkingConfig") || strings.Contains(string(body), "includeThoughts") {
				t.Fatalf("incorrect upstream controls: %s err=%v", body, err)
			}
		})
	}
}

func TestGeminiThinkingRejectsInvalidOrLossyControls(t *testing.T) {
	for _, tc := range []struct{ target, config, field string }{
		{"openai_chat", `true`, "thinkingConfig"},
		{"openai_chat", `{"thinkingBudget":1.5}`, "thinkingBudget"},
		{"openai_chat", `{"thinkingBudget":-2}`, "thinkingBudget"},
		{"openai_chat", `{"thinkingBudget":null}`, "thinkingBudget"},
		{"openai_chat", `{"thinkingBudget":0,"thinkingLevel":"high"}`, "thinkingConfig"},
		{"openai_chat", `{"includeThoughts":"true"}`, "includeThoughts"},
		{"openai_chat", `{"thinkingLevel":"unknown"}`, "thinkingLevel"},
		{"openai_chat", `{"future":true}`, "future"},
		{"anthropic_messages", `{"thinkingBudget":512}`, "thinkingBudget"},
		{"anthropic_messages", `{"thinkingBudget":4096}`, "thinkingBudget"},
		{"anthropic_messages", `{"thinkingLevel":"minimal"}`, "thinkingLevel"},
		{"anthropic_messages", `{"includeThoughts":true}`, "includeThoughts"},
	} {
		q := RelayRequest{Controls: map[string]any{}}
		var value any
		d := json.NewDecoder(strings.NewReader(tc.config))
		d.UseNumber()
		if err := d.Decode(&value); err != nil {
			t.Fatal(err)
		}
		err := q.parseGeminiThinking(value, tc.target)
		var failure *ConversionError
		if !errors.As(err, &failure) || !strings.HasSuffix(failure.Field, tc.field) {
			t.Fatalf("%s %s: expected %s, got %v", tc.target, tc.config, tc.field, err)
		}
	}
}
