package infrastructure

import (
	"errors"
	"strings"
	"testing"
)

func TestGeminiThoughtHistoryMapsToChatReasoning(t *testing.T) {
	for _, target := range []string{"openai_chat", ""} {
		for _, answer := range []string{`{"text":"answer","thought":false}`, `{"text":"answer"}`, `{"functionCall":{"id":"call-1","name":"lookup","args":{}}}`} {
			last := `{"role":"user","parts":[{"text":"next"}]}`
			if strings.Contains(answer, "functionCall") {
				last = `{"role":"user","parts":[{"functionResponse":{"id":"call-1","name":"lookup","response":{"ok":true}}}]}`
			}
			raw := `{"contents":[{"role":"user","parts":[{"text":"hi","thought":false}]},{"role":"model","parts":[{"text":"plan","thought":true},{"text":"more","thought":true},` + answer + `]},` + last + `]}`
			q, err := ParseRelayRequest([]byte(raw), "gemini", target)
			if err != nil {
				t.Fatal(err)
			}
			payload, _, err := q.Upstream(target)
			if err != nil {
				t.Fatal(err)
			}
			root, err := DecodeProtocolJSON(payload)
			if err != nil {
				t.Fatal(err)
			}
			message := root["messages"].([]any)[1].(map[string]any)
			if message["reasoning_content"] != "planmore" || strings.Contains(string(payload), `"thought"`) {
				t.Fatalf("reasoning history lost: %s", payload)
			}
			if strings.Contains(answer, "functionCall") {
				if message["content"] != "" || len(message["tool_calls"].([]any)) != 1 || !strings.Contains(string(payload), `"tool_call_id":"call-1"`) {
					t.Fatalf("tool history changed: %s", payload)
				}
			} else if message["content"] != "answer" {
				t.Fatalf("thoughts mixed into answer: %s", payload)
			}
		}
	}
}

func TestGeminiThoughtHistoryRejectsInvalidOrSignedParts(t *testing.T) {
	for _, tc := range []struct{ target, role, part, field string }{
		{"openai_chat", "model", `{"text":"plan","thought":"true"}`, "thought"},
		{"openai_chat", "model", `{"text":"plan","thought":null}`, "thought"},
		{"openai_chat", "user", `{"text":"plan","thought":true}`, "thought"},
		{"openai_chat", "model", `{"text":"plan","thought":true,"thoughtSignature":"signature"}`, "thoughtSignature"},
		{"anthropic_messages", "model", `{"text":"plan","thought":true}`, "thought"},
	} {
		raw := `{"contents":[{"role":"` + tc.role + `","parts":[` + tc.part + `]},{"role":"user","parts":[{"text":"next"}]}]}`
		_, err := ParseRelayRequest([]byte(raw), "gemini", tc.target)
		var failure *ConversionError
		if !errors.As(err, &failure) || failure.Field != "contents[0].parts[0]."+tc.field {
			t.Fatalf("unexpected error for %s: %v", tc.part, err)
		}
	}
}
