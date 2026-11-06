package infrastructure

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const chatReplyFixture = `{"id":"chat_1","choices":[{"index":0,"message":{"role":"assistant","content":"hello","tool_calls":[{"id":"new","type":"function","function":{"name":"read","arguments":"{\"id\":9007199254740993}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":17,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}`

func TestMessagesToChatPreservesHistoryAndControls(t *testing.T) {
	for _, choice := range []string{`{"type":"auto"}`, `{"type":"none"}`, `{"type":"any"}`, `{"type":"tool","name":"read","disable_parallel_tool_use":true}`} {
		t.Run(choice, func(t *testing.T) {
			raw := strings.Replace(messagesRelayFixture, `{"type":"any"}`, choice, 1)
			q, err := ParseRelayRequest([]byte(raw), "messages", "openai_chat")
			if err != nil {
				t.Fatal(err)
			}
			payload, path, err := q.Upstream("openai_chat")
			if err != nil || path != "chat/completions" {
				t.Fatalf("path=%s err=%v", path, err)
			}
			root, _ := DecodeProtocolJSON(payload)
			messages := root["messages"].([]any)
			if len(messages) != 5 || messages[0].(map[string]any)["role"] != "system" || messages[3].(map[string]any)["role"] != "tool" || messages[4].(map[string]any)["content"] != "continue" {
				t.Fatalf("history changed: %s", payload)
			}
			if !strings.Contains(string(payload), "9007199254740993") || !strings.Contains(string(payload), "Tool error:") || root["max_tokens"] != json.Number("512") || root["stream_options"].(map[string]any)["include_usage"] != true {
				t.Fatalf("controls or history lost: %s", payload)
			}
			if strings.Contains(choice, "disable_parallel") && root["parallel_tool_calls"] != false {
				t.Fatalf("serial tools lost: %s", payload)
			}
		})
	}
}

func TestMessagesChatHintsAreValidatedWithoutTouchingArguments(t *testing.T) {
	raw := strings.Replace(messagesRelayFixture, `"system":`, `"metadata":{"user_id":"local-user"},"thinking":{"type":"disabled"},"cache_control":{"type":"ephemeral"},"system":`, 1)
	raw = strings.ReplaceAll(raw, `"type":"text",`, `"type":"text","cache_control":{"type":"ephemeral","ttl":"1h"},`)
	raw = strings.Replace(raw, `"name":"read","input_schema"`, `"name":"read","cache_control":{"type":"ephemeral","ttl":"5m"},"input_schema"`, 1)
	raw = strings.Replace(raw, `"input":{"id":`, `"input":{"cache_control":"opaque argument","id":`, 1)
	q, err := ParseRelayRequest([]byte(raw), "messages", "openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err := q.Upstream("openai_chat")
	if err != nil || strings.Contains(string(payload), "local-user") || strings.Contains(string(payload), "ephemeral") || !strings.Contains(string(payload), "opaque argument") {
		t.Fatalf("hint normalization changed data: %s err=%v", payload, err)
	}
}

func TestMessagesToChatRejectsUnrepresentableInput(t *testing.T) {
	base := `{"model":"x","messages":[{"role":"user","content":"hello"}]}`
	cases := []struct{ name, raw, field string }{
		{"top k", strings.Replace(base, `"model":`, `"top_k":3,"model":`, 1), "top_k"},
		{"metadata type", strings.Replace(base, `"model":`, `"metadata":[],"model":`, 1), "metadata"},
		{"metadata user", strings.Replace(base, `"model":`, `"metadata":{"user_id":3},"model":`, 1), "metadata.user_id"},
		{"metadata unknown", strings.Replace(base, `"model":`, `"metadata":{"future":true},"model":`, 1), "metadata.future"},
		{"cache type", strings.Replace(base, `"model":`, `"cache_control":{"type":"persistent"},"model":`, 1), "cache_control"},
		{"cache ttl", strings.Replace(base, `"model":`, `"cache_control":{"type":"ephemeral","ttl":"forever"},"model":`, 1), "cache_control.ttl"},
		{"cache unknown", strings.Replace(base, `"model":`, `"cache_control":{"type":"ephemeral","future":true},"model":`, 1), "cache_control.future"},
		{"prefill", `{"model":"x","messages":[{"role":"assistant","content":"hello"}]}`, "messages"},
		{"image", `{"model":"x","messages":[{"role":"user","content":[{"type":"image","source":{}}]}]}`, "messages[0].content[0].source.type"},
		{"interrupted history", `{"model":"x","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"read","input":{}}]},{"role":"user","content":[{"type":"text","text":"interrupt"},{"type":"tool_result","tool_use_id":"a","content":"ok"}]}]}`, "messages[1].content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRelayRequest([]byte(tc.raw), "messages", "openai_chat")
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Field != tc.field {
				t.Fatalf("error=%v want field=%s", err, tc.field)
			}
		})
	}
}

func TestChatReplyConvertsToolsAndActualUsage(t *testing.T) {
	reply, err := ParseRelayReply([]byte(chatReplyFixture), "openai_chat", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := reply.JSON("messages", "alias")
	if err != nil || reply.ID != "chat_1" || reply.Input != 13 || reply.CacheRead != 4 || reply.Output != 7 || !strings.Contains(string(payload), "9007199254740993") || !strings.Contains(string(payload), `"stop_reason":"tool_use"`) {
		t.Fatalf("reply=%+v payload=%s err=%v", reply, payload, err)
	}
}

func TestChatReplyRejectsInvalidContent(t *testing.T) {
	cases := []string{
		`null`, `{`, `{"error":{"message":"secret"}}`, `{"choices":[]}`, `{"choices":[null]}`, `{"choices":[{"message":null}]}`,
		strings.Replace(chatReplyFixture, `"role":"assistant"`, `"role":"user"`, 1),
		strings.Replace(chatReplyFixture, `"content":"hello"`, `"content":3`, 1),
		strings.Replace(chatReplyFixture, `"content":"hello"`, `"content":"hello","reasoning_content":{}`, 1),
		strings.Replace(chatReplyFixture, `"tool_calls":[`, `"tool_calls":false,"ignored":[`, 1),
		strings.Replace(chatReplyFixture, `"id":"new"`, `"id":""`, 1),
		strings.Replace(chatReplyFixture, `"type":"function"`, `"type":"custom"`, 1),
		strings.Replace(chatReplyFixture, `"function":{"name"`, `"function":{"future":true,"name"`, 1),
		strings.Replace(chatReplyFixture, `"name":"read"`, `"name":"bad name"`, 1),
		strings.Replace(chatReplyFixture, `"arguments":"{\"id\":9007199254740993}"`, `"arguments":"[]"`, 1),
		strings.Replace(chatReplyFixture, `"arguments":"{\"id\":9007199254740993}"`, `"arguments":{}`, 1),
		strings.Replace(chatReplyFixture, `"finish_reason":"tool_calls"`, `"finish_reason":"stop"`, 1),
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseRelayReply([]byte(raw), "openai_chat", "fallback"); err == nil {
				t.Fatal("invalid upstream content accepted")
			}
		})
	}
}

func TestChatFinishReasonsAndMissingUsage(t *testing.T) {
	for _, reason := range []string{"stop", "length", "content_filter"} {
		raw := `{"choices":[{"message":{"role":"assistant","content":"hello","refusal":null,"audio":null,"annotations":[],"reasoning_content":""},"finish_reason":"` + reason + `"}]}`
		reply, err := ParseRelayReply([]byte(raw), "", "fallback")
		if err != nil || reply.Stop != reason {
			t.Fatalf("reply=%+v err=%v", reply, err)
		}
		if _, err := reply.JSON("messages", "alias"); err == nil {
			t.Fatal("fabricated usage")
		}
	}
	for _, reason := range []string{"", "future", "function_call", "tool_calls"} {
		if _, err := ChatRelayStop(reason, false); err == nil {
			t.Fatalf("unsupported finish accepted: %s", reason)
		}
	}
}

func TestChatUsageRejectsMalformedCounts(t *testing.T) {
	for _, raw := range []string{
		`{"usage":false}`, `{"usage":{}}`, `{"usage":{"prompt_tokens":"1","completion_tokens":1}}`,
		`{"usage":{"prompt_tokens":-1,"completion_tokens":1}}`, `{"usage":{"prompt_tokens":1.5,"completion_tokens":1}}`,
		`{"usage":{"prompt_tokens":9223372036854775808,"completion_tokens":1}}`,
		`{"usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1}}`,
		`{"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":false}}`,
		`{"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":-1}}}`,
		`{"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":2}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			root, err := DecodeProtocolJSON([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateChatRelayUsage(root); err == nil {
				t.Fatal("invalid usage accepted")
			}
		})
	}
}

func TestChatRequestHistoryBoundaries(t *testing.T) {
	cases := []struct {
		name string
		q    RelayRequest
	}{
		{"empty conversation", RelayRequest{}},
		{"unanswered invocation", RelayRequest{Messages: []RelayMessage{{Role: "assistant", Blocks: []RelayBlock{{Type: "tool_use", ID: "a"}}}}}},
		{"unmatched result", RelayRequest{Messages: []RelayMessage{{Role: "user", Blocks: []RelayBlock{{Type: "tool_result", ID: "a"}}}}}},
		{"assistant interrupts tool results", RelayRequest{Messages: []RelayMessage{{Role: "assistant", Blocks: []RelayBlock{{Type: "tool_use", ID: "a"}}}, {Role: "assistant", Blocks: []RelayBlock{{Type: "text", Text: "interrupt"}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := tc.q.Upstream("openai_chat"); err == nil {
				t.Fatal("invalid Chat history accepted")
			}
		})
	}
	q, err := ParseRelayRequest([]byte(`{"model":"x","messages":[{"role":"user","content":""}]}`), "messages", "openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err := q.Upstream("openai_chat")
	if err != nil || !strings.Contains(string(payload), `"content":"","role":"user"`) {
		t.Fatalf("empty text turn lost: %s %v", payload, err)
	}
}

func TestChatParallelCallsAndDuplicateIDs(t *testing.T) {
	call := func(id string) any {
		return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": "read", "arguments": "{}"}}
	}
	blocks, err := ChatRelayToolCalls([]any{call("a"), call("b")})
	if err != nil || len(blocks) != 2 || blocks[0].ID != "a" || blocks[1].ID != "b" {
		t.Fatalf("parallel calls lost: %+v %v", blocks, err)
	}
	for _, calls := range []any{false, []any{call("a"), call("a")}, []any{nil}, []any{map[string]any{"id": "a", "type": "function", "function": nil}}, []any{map[string]any{"id": "a", "type": "function", "function": map[string]any{}, "extra": true}}} {
		if _, err := ChatRelayToolCalls(calls); err == nil {
			t.Fatal("invalid tool calls accepted")
		}
	}
}
