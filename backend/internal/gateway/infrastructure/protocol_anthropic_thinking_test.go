package infrastructure

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestChatAnthropicThinkingControlsPreserveCallerValues(t *testing.T) {
	for _, thinking := range []string{
		`{"type":"disabled"}`,
		`{"type":"adaptive","display":"omitted"}`,
		`{"type":"enabled","budget_tokens":2048}`,
		`{"type":"enabled","budget_tokens":9007199254740993,"future":true}`,
	} {
		raw := `{"model":"alias","thinking":` + thinking + `,"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hello"}]}`
		q, err := ParseRelayRequest([]byte(raw), "chat", "anthropic_messages")
		if err != nil {
			t.Fatal(err)
		}
		payload, _, err := q.Upstream("anthropic_messages")
		if err != nil {
			t.Fatal(err)
		}
		original, _ := DecodeProtocolJSON([]byte(raw))
		upstream, _ := DecodeProtocolJSON(payload)
		for _, name := range []string{"thinking", "output_config"} {
			if !reflect.DeepEqual(original[name], upstream[name]) {
				t.Fatalf("%s changed: %s", name, payload)
			}
		}
		if _, err := ParseRelayRequest([]byte(raw), "chat", "gemini"); err == nil {
			t.Fatal("Anthropic thinking escaped its conversion path")
		}
	}
	q, err := ParseRelayRequest([]byte(`{"model":"alias","messages":[{"role":"user","content":"hello"}]}`), "chat", "anthropic_messages")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := q.Controls["thinking"]; exists {
		t.Fatal("thinking enabled without a caller setting")
	}
}

func TestAnthropicThinkingJSONToolHistoryRoundTrip(t *testing.T) {
	const raw = `{"type":"message","id":"msg_thinking","content":[{"type":"thinking","thinking":"plan","signature":"signed-one"},{"type":"text","text":"你"},{"type":"redacted_thinking","data":"opaque-encrypted"},{"type":"tool_use","id":"call_1","name":"read","input":{"id":9007199254740993}},{"type":"thinking","thinking":"more","signature":"signed-two"},{"type":"text","text":"done"}],"stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":7}}`
	reply, err := ParseRelayReply([]byte(raw), "anthropic_messages", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := reply.JSON("chat", "alias")
	if err != nil {
		t.Fatal(err)
	}
	chat, _ := DecodeProtocolJSON(payload)
	message := chat["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "你done" || message["reasoning_content"] != "planmore" || len(message["anthropic_thinking"].([]any)) != 3 {
		t.Fatalf("thinking not separated/preserved: %s", payload)
	}
	body, _ := json.Marshal(map[string]any{"model": "alias", "thinking": map[string]any{"type": "adaptive"}, "messages": []any{
		message, map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "result"},
	}})
	q, err := ParseRelayRequest(body, "chat", "anthropic_messages")
	if err != nil {
		t.Fatal(err)
	}
	upstream, _, err := q.Upstream("anthropic_messages")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := DecodeProtocolJSON([]byte(raw))
	got, _ := DecodeProtocolJSON(upstream)
	if !reflect.DeepEqual(got["messages"].([]any)[0].(map[string]any)["content"], want["content"]) {
		t.Fatalf("signed history/order changed: %s", upstream)
	}
	if q.Controls["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Fatal("thinking mode lost on tool continuation")
	}
	// Native rendering must keep provider signatures, never synthetic markers.
	native, err := reply.JSON("messages", "alias")
	if err != nil || strings.Contains(string(native), chatThinkingPrefix) || !strings.Contains(string(native), `"signature":"signed-two"`) || !strings.Contains(string(native), `"data":"opaque-encrypted"`) {
		t.Fatalf("native thinking changed: %s %v", native, err)
	}
}

func TestAnthropicThinkingHistoryRejectsInvalidOrUnsignedData(t *testing.T) {
	for _, tc := range []struct{ extra, role, field string }{
		{`"reasoning_content":"PRIVATE"`, "assistant", "anthropic_thinking"},
		{`"anthropic_thinking":{}`, "assistant", "anthropic_thinking"},
		{`"anthropic_thinking":[{"block":{"type":"thinking","thinking":"PRIVATE"},"text_offset":0,"tool_offset":0}]`, "assistant", "signature"},
		{`"anthropic_thinking":[{"block":{"type":"tool_use","input":{"PRIVATE":true}},"text_offset":0,"tool_offset":0}]`, "assistant", "type"},
		{`"anthropic_thinking":[{"block":{"type":"thinking","thinking":"PRIVATE","signature":"sig"},"text_offset":-1,"tool_offset":0}]`, "assistant", "text_offset"},
		{`"anthropic_thinking":[{"block":{"type":"thinking","thinking":"PRIVATE","signature":"sig"},"text_offset":1,"tool_offset":0}]`, "assistant", "text_offset"},
		{`"anthropic_thinking":[{"block":{"type":"thinking","thinking":"PRIVATE","signature":"sig"},"text_offset":0,"tool_offset":1}]`, "assistant", "tool_offset"},
		{`"anthropic_thinking":[{"block":{"type":"thinking","thinking":"PRIVATE","signature":"sig"},"text_offset":0,"tool_offset":0}]`, "user", "anthropic_thinking"},
		{`"anthropic_thinking":[{"block":{"type":"thinking","thinking":"PRIVATE","signature":"sig"},"text_offset":0,"tool_offset":0}]`, "system", "anthropic_thinking"},
		{`"anthropic_thinking":[{"block":{"type":"thinking","thinking":"PRIVATE","signature":"sig"},"text_offset":0,"tool_offset":0}]`, "developer", "anthropic_thinking"},
		{`"reasoning_content":"changed","anthropic_thinking":[{"block":{"type":"thinking","thinking":"PRIVATE","signature":"sig"},"text_offset":0,"tool_offset":0}]`, "assistant", "reasoning_content"},
	} {
		raw := `{"model":"alias","messages":[{"role":"` + tc.role + `","content":"你",` + tc.extra + `}]}`
		_, err := ParseRelayRequest([]byte(raw), "chat", "anthropic_messages")
		var failure *ConversionError
		if !errors.As(err, &failure) || !strings.HasSuffix(failure.Field, tc.field) || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("wrong safe diagnostic: %v for %s", err, raw)
		}
	}
	for _, extra := range []string{`"thinking":null`, `"thinking":true`, `"thinking":{}`, `"output_config":[]`} {
		if _, err := ParseRelayRequest([]byte(`{"model":"alias",`+extra+`,"messages":[{"role":"user","content":"hi"}]}`), "chat", "anthropic_messages"); err == nil {
			t.Fatalf("invalid thinking control accepted: %s", extra)
		}
	}
}

func TestAnthropicThinkingResponseRejectsMalformedBlocks(t *testing.T) {
	for _, block := range []string{
		`{"type":"thinking","thinking":"PRIVATE"}`,
		`{"type":"thinking","thinking":3,"signature":"PRIVATE"}`,
		`{"type":"thinking","thinking":"PRIVATE","signature":"sig","future":true}`,
		`{"type":"redacted_thinking","data":null}`,
	} {
		_, err := ParseRelayReply([]byte(`{"type":"message","content":[`+block+`],"stop_reason":"end_turn"}`), "anthropic_messages", "id")
		if err == nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("wrong malformed thinking result: %v", err)
		}
	}
}
