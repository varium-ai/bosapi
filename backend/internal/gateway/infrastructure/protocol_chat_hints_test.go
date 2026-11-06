package infrastructure

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeChatClientHints(t *testing.T) {
	for _, hint := range []string{
		`"thinking":{"type":"disabled"}`,
		`"thinking":{"type":"adaptive"}`,
		`"thinking":{"type":"adaptive","display":"omitted"}`,
		`"thinking":{"type":"adaptive","display":null}`,
		`"thinking":{"type":"enabled","budget_tokens":2048,"display":"summarized"}`,
		`"thinking":{"type":"enabled","budget_tokens":2048,"display":null}`,
		`"thinking":{"type":"disabled","display":null}`,
		`"output_config":{}`,
		`"output_config":{"effort":"low"}`,
		`"output_config":{"effort":"medium"}`,
		`"output_config":{"effort":"high"}`,
		`"output_config":{"effort":"xhigh"}`,
		`"output_config":{"effort":"max"}`,
		`"context_management":{"edits":[]}`,
		`"thinking":{"type":"adaptive","display":"omitted"},"output_config":{"effort":"high"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`,
	} {
		t.Run(hint, func(t *testing.T) {
			raw := strings.Replace(messagesRelayFixture, `"model":`, hint+`,"model":`, 1)
			q, err := ParseRelayRequest([]byte(raw), "messages", "openai_chat")
			if err != nil {
				t.Fatal(err)
			}
			payload, path, err := q.Upstream("openai_chat")
			if err != nil || path != "chat/completions" {
				t.Fatalf("upstream: %s %v", path, err)
			}
			root, err := DecodeProtocolJSON(payload)
			if err != nil {
				t.Fatal(err)
			}
			original, err := DecodeProtocolJSON([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			thinking, hasThinking := root["thinking"]
			wantThinking, wantHasThinking := original["thinking"]
			if hasThinking != wantHasThinking || !reflect.DeepEqual(thinking, wantThinking) {
				t.Fatalf("caller thinking changed: %s", payload)
			}
			for _, field := range []string{"output_config", "context_management", "reasoning_effort"} {
				if _, exists := root[field]; exists {
					t.Fatalf("advisory hint leaked upstream: %s", payload)
				}
			}
			if !strings.Contains(string(payload), "9007199254740993") || len(q.Tools) != 1 || len(root["messages"].([]any)) != 5 {
				t.Fatalf("tool history or schemas lost: %s", payload)
			}
		})
	}
}

func TestClaudeChatHintsRejectUnsupportedSemantics(t *testing.T) {
	for _, tc := range []struct{ hint, field string }{
		{`"output_config":null`, "output_config"},
		{`"output_config":{"effort":{}}`, "output_config.effort"},
		{`"output_config":{"effort":"future"}`, "output_config.effort"},
		{`"output_config":{"format":{"type":"json_schema"}}`, "output_config.format.schema"},
		{`"output_config":{"task_budget":10}`, "output_config.task_budget"},
		{`"context_management":null`, "context_management"},
		{`"context_management":{}`, "context_management.edits"},
		{`"context_management":{"edits":null}`, "context_management.edits"},
		{`"context_management":{"edits":[],"future":true}`, "context_management.future"},
		{`"context_management":{"edits":[null]}`, "context_management.edits[0]."},
		{`"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":0}]}`, "context_management.edits[0]."},
		{`"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]}`, "context_management.edits[0]."},
		{`"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all","future":true}]}`, "context_management.edits[0].future"},
		{`"safeguards":[{"type":"future"}]`, "safeguards"},
	} {
		t.Run(tc.hint, func(t *testing.T) {
			raw := `{"model":"alias",` + tc.hint + `,"messages":[{"role":"user","content":"hello"}]}`
			_, err := ParseRelayRequest([]byte(raw), "messages", "openai_chat")
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Field != tc.field {
				t.Fatalf("error=%v want field=%s", err, tc.field)
			}
		})
	}
}

func TestMessagesChatForwardsCallerThinkingControlsUnchanged(t *testing.T) {
	for _, target := range []string{"openai_chat", ""} {
		for _, thinking := range []string{
			`null`, `false`, `[]`, `{}`, `{"type":"future","unknown":true}`,
			`{"type":"enabled"}`, `{"type":"enabled","budget_tokens":0}`,
			`{"type":"enabled","budget_tokens":-1}`, `{"type":"enabled","budget_tokens":1023}`,
			`{"type":"enabled","budget_tokens":2048.5}`, `{"type":"enabled","budget_tokens":"2048"}`,
			`{"type":"enabled","budget_tokens":9007199254740993}`,
			`{"type":"adaptive","budget_tokens":2048,"display":{}}`,
			`{"type":"disabled","budget_tokens":2048,"display":[]}`,
		} {
			t.Run(target+"/"+thinking, func(t *testing.T) {
				raw := `{"model":"alias","thinking":` + thinking + `,"reasoning_effort":{"custom":9007199254740993},"enable_thinking":false,"messages":[{"role":"user","content":"hello"}]}`
				q, err := ParseRelayRequest([]byte(raw), "messages", target)
				if err != nil {
					t.Fatal(err)
				}
				payload, _, err := q.Upstream(target)
				if err != nil {
					t.Fatal(err)
				}
				original, _ := DecodeProtocolJSON([]byte(raw))
				upstream, err := DecodeProtocolJSON(payload)
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"thinking", "reasoning_effort", "enable_thinking"} {
					value, exists := upstream[field]
					if !exists || !reflect.DeepEqual(value, original[field]) {
						t.Fatalf("caller %s changed: %s", field, payload)
					}
				}
			})
		}
	}
}

func TestClaudeChatHintsDoNotChangeOtherConversionTargets(t *testing.T) {
	for _, hint := range []string{`"thinking":{"type":"adaptive"}`, `"output_config":{"effort":"high"}`, `"context_management":{"edits":[]}`} {
		raw := `{"model":"alias",` + hint + `,"messages":[{"role":"user","content":"hello"}]}`
		if _, err := ParseRelayRequest([]byte(raw), "messages", "gemini"); err == nil {
			t.Fatalf("Chat hint compatibility escaped into Gemini: %s", raw)
		}
	}
}
