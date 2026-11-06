package infrastructure

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatReasoningAndNullableLegacyMetadata(t *testing.T) {
	for _, field := range []string{"reasoning", "reasoning_content"} {
		t.Run(field, func(t *testing.T) {
			raw := strings.Replace(chatReplyFixture, `"content":"hello"`, `"content":"hello","function_call":null,"`+field+`":"private reasoning"`, 1)
			reply, err := ParseRelayReply([]byte(raw), "openai_chat", "fallback")
			if err != nil {
				t.Fatal(err)
			}
			payload, err := reply.JSON("messages", "alias")
			if err != nil || !strings.Contains(string(payload), `"type":"thinking"`) || !strings.Contains(string(payload), chatThinkingPrefix+field) {
				t.Fatalf("reasoning lost: %s %v", payload, err)
			}
			root, err := DecodeProtocolJSON(payload)
			if err != nil {
				t.Fatal(err)
			}
			request := map[string]any{"model": "alias", "messages": []any{map[string]any{"role": "assistant", "content": root["content"]}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "new", "content": "done"}}}}}
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			q, err := ParseRelayRequest(body, "messages", "openai_chat")
			if err != nil {
				t.Fatal(err)
			}
			upstream, _, err := q.Upstream("openai_chat")
			if err != nil || !strings.Contains(string(upstream), `"`+field+`":"private reasoning"`) || !strings.Contains(string(upstream), "9007199254740993") {
				t.Fatalf("reasoning history lost: %s %v", upstream, err)
			}
			chat, err := reply.JSON("chat", "alias")
			if err != nil || !strings.Contains(string(chat), `"`+field+`":"private reasoning"`) {
				t.Fatalf("native reasoning field lost: %s %v", chat, err)
			}
		})
	}
}

func TestChatReasoningRejectsInvalidFieldsAndForeignSignatures(t *testing.T) {
	for _, raw := range []string{
		`{"function_call":{}}`, `{"reasoning":3}`, `{"reasoning_content":[]}`, `{"reasoning":"a","reasoning_content":"b"}`,
	} {
		root, err := DecodeProtocolJSON([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := ChatRelayMessage(root); err == nil {
			t.Fatalf("invalid response accepted: %s", raw)
		}
	}
	for _, block := range []map[string]any{
		{"type": "thinking", "thinking": "private", "signature": "native-anthropic-signature"},
		{"type": "thinking", "thinking": 3, "signature": chatThinkingPrefix + "reasoning"},
		{"type": "thinking", "thinking": "private", "signature": chatThinkingPrefix + "content"},
		{"type": "thinking", "thinking": "private", "signature": chatThinkingPrefix + "reasoning", "unknown": true},
	} {
		if _, err := parseChatThinking(block, "history."); err == nil {
			t.Fatal("invalid reasoning history accepted")
		}
	}
}

func TestClaudeInstructionTurnsRetainTheirPositionForChat(t *testing.T) {
	for _, role := range []string{"system", "developer"} {
		raw := `{"model":"alias","system":"root instructions","messages":[{"role":"user","content":"hello"},{"role":"` + role + `","content":"session instructions"}]}`
		q, err := ParseRelayRequest([]byte(raw), "messages", "openai_chat")
		if err != nil {
			t.Fatal(err)
		}
		payload, _, err := q.Upstream("openai_chat")
		if err != nil {
			t.Fatal(err)
		}
		root, err := DecodeProtocolJSON(payload)
		if err != nil {
			t.Fatal(err)
		}
		messages := root["messages"].([]any)
		if len(messages) != 3 || messages[0].(map[string]any)["content"] != "root instructions" || messages[1].(map[string]any)["role"] != "user" || messages[2].(map[string]any)["role"] != role {
			t.Fatalf("instruction order changed: %s", payload)
		}
		if _, err := ParseRelayRequest([]byte(raw), "messages", "gemini"); err == nil {
			t.Fatal("instruction extension escaped the Chat bridge")
		}
	}
}

func TestChatHistoryRejectsMixedReasoningFormats(t *testing.T) {
	for _, message := range []RelayMessage{
		{Role: "user", Blocks: []RelayBlock{{Type: "thinking", Text: "private", ReasoningField: "reasoning"}}},
		{Role: "assistant", Blocks: []RelayBlock{{Type: "thinking", Text: "private", ReasoningField: "content"}}},
		{Role: "assistant", Blocks: []RelayBlock{{Type: "thinking", Text: "a", ReasoningField: "reasoning"}, {Type: "thinking", Text: "b", ReasoningField: "reasoning_content"}}},
	} {
		if _, err := chatMessages(message); err == nil {
			t.Fatal("ambiguous reasoning history accepted")
		}
	}
}
