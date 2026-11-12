package infrastructure

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCodexChatResponseFormat(t *testing.T) {
	for _, format := range []string{
		`null`, `{"type":"text"}`, `{"type":"json_object"}`,
		`{"type":"json_schema","json_schema":{"name":"result","description":"exact schema","strict":true,"schema":{"type":"object","properties":{"n":{"const":9007199254740993}},"additionalProperties":false}}}`,
	} {
		q, err := ParseRelayRequest([]byte(`{"model":"x","response_format":`+format+`,"messages":[{"role":"user","content":"hi"}]}`), "chat", "openai_responses")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := q.CodexUpstream()
		if err != nil {
			t.Fatal(err)
		}
		root, err := DecodeProtocolJSON(raw)
		if err != nil || root["response_format"] != nil {
			t.Fatalf("Chat field leaked: %s %v", raw, err)
		}
		if format == "null" {
			if root["text"] != nil {
				t.Fatalf("unexpected format: %s", raw)
			}
			continue
		}
		want, _ := DecodeProtocolJSON([]byte(format))
		if want["type"] == "json_schema" {
			inner := want["json_schema"].(map[string]any)
			delete(want, "json_schema")
			for k, v := range inner {
				want[k] = v
			}
		}
		if !reflect.DeepEqual(root["text"].(map[string]any)["format"], want) {
			t.Fatalf("lost format: %s", raw)
		}
	}
	for _, format := range []string{`"json"`, `{}`, `{"type":"unknown"}`, `{"type":"json_object","ignored":true}`, `{"type":"text","json_schema":{}}`, `{"type":"json_schema","json_schema":{"name":"x","schema":{},"strict":"true"}}`, `{"type":"json_schema","json_schema":{"name":"x","schema":null}}`, `{"type":"json_schema","json_schema":{"schema":{}}}`} {
		_, err := ParseRelayRequest([]byte(`{"model":"x","response_format":`+format+`,"messages":[{"role":"user","content":"hi"}]}`), "chat", "openai_responses")
		if err == nil || !strings.Contains(err.Error(), "response_format") {
			t.Fatalf("invalid format accepted: %s %v", format, err)
		}
	}
}

func TestCodexReasoningHistoryRoundTrip(t *testing.T) {
	item, _ := DecodeProtocolJSON([]byte(`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"summary"}],"encrypted_content":"opaque","content":[],"future":{"n":9007199254740993}}`))
	block, err := CodexReasoningBlock(item, "output.")
	if err != nil {
		t.Fatal(err)
	}
	reply := RelayReply{ID: "id", Stop: "tool_calls", Blocks: []RelayBlock{
		{Type: "text", Text: "前"}, block,
		{Type: "tool_use", ID: "call", Name: "read", Input: map[string]any{"n": json.Number("9007199254740993")}},
	}}
	raw, err := reply.JSON("chat", "x")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := DecodeProtocolJSON(raw)
	message := root["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	request, _ := json.Marshal(map[string]any{"model": "x", "messages": []any{message, map[string]any{"role": "tool", "tool_call_id": "call", "content": "done"}}})
	q, err := ParseRelayRequest(request, "chat", "openai_responses")
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := q.CodexUpstream()
	if err != nil {
		t.Fatal(err)
	}
	converted, _ := DecodeProtocolJSON(upstream)
	input := converted["input"].([]any)
	if len(input) != 4 || !reflect.DeepEqual(input[1], item) || input[2].(map[string]any)["type"] != "function_call" || !strings.Contains(string(upstream), "9007199254740993") {
		t.Fatalf("lost reasoning order/history: %s", upstream)
	}
	// Offset 1 splits the Chinese character; no byte slicing may corrupt it.
	message["codex_reasoning"].([]any)[0].(map[string]any)["text_offset"] = json.Number("1")
	request, _ = json.Marshal(map[string]any{"model": "x", "messages": []any{message}})
	if _, err := ParseRelayRequest(request, "chat", "openai_responses"); err == nil || !strings.Contains(err.Error(), "text_offset") {
		t.Fatalf("invalid reasoning offset accepted: %v", err)
	}
}

func TestCodexReasoningHistoryRejectsInvalidItems(t *testing.T) {
	for _, entry := range []string{
		`{"item":{"type":"function_call","name":"read"},"text_offset":0,"tool_offset":0}`,
		`{"item":{"type":"reasoning","summary":"text"},"text_offset":0,"tool_offset":0}`,
		`{"item":{"type":"reasoning","encrypted_content":{}},"text_offset":0,"tool_offset":0}`,
		`{"item":{"type":"reasoning"},"text_offset":-1,"tool_offset":0}`,
		`{"item":{"type":"reasoning"},"text_offset":0,"tool_offset":1}`,
	} {
		_, err := ParseRelayRequest([]byte(`{"model":"x","messages":[{"role":"assistant","content":"text","codex_reasoning":[`+entry+`]}]}`), "chat", "openai_responses")
		if err == nil {
			t.Fatalf("invalid history accepted: %s", entry)
		}
	}
	for _, role := range []string{"user", "system", "developer"} {
		_, err := ParseRelayRequest([]byte(`{"model":"x","messages":[{"role":"`+role+`","content":"text","codex_reasoning":[{"item":{"type":"reasoning"},"text_offset":0,"tool_offset":0}]}]}`), "chat", "openai_responses")
		if err == nil {
			t.Fatalf("reasoning accepted on role %s", role)
		}
	}
}
