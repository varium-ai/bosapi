package infrastructure

import (
	"strings"
	"testing"
)

func TestGeminiReplyOmitsUnsignedChatReasoning(t *testing.T) {
	const raw = `{"choices":[{"index":0,"message":{"role":"assistant","content":"Visible answer","reasoning_content":"PRIVATE_REASONING"},"finish_reason":"stop"}]}`
	reply, err := ParseRelayReply([]byte(raw), "openai_chat", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := reply.JSON("gemini", "alias")
	if err != nil {
		t.Fatalf("Gemini conversion failed: %v", err)
	}
	if !strings.Contains(string(payload), `"text":"Visible answer"`) || strings.Contains(string(payload), "PRIVATE_REASONING") || strings.Contains(string(payload), `"thought":true`) {
		t.Fatalf("unexpected Gemini response: %s", payload)
	}
}

func TestGeminiReplyStillRejectsUnrepresentableThinking(t *testing.T) {
	for _, reply := range []RelayReply{
		{Blocks: []RelayBlock{{Type: "thinking", Text: "private", Signature: "native-signature"}}},
		{Blocks: []RelayBlock{{Type: "thinking", Text: "private"}}},
	} {
		if _, err := reply.JSON("gemini", "alias"); err == nil {
			t.Fatal("unrepresentable thinking was accepted")
		}
	}
}
