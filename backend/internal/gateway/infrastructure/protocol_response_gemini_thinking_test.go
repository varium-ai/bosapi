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

func TestGeminiReplyMapsSignedThinkingToRequestedThoughtText(t *testing.T) {
	reply := RelayReply{IncludeThoughts: true, Blocks: []RelayBlock{
		{Type: "thinking", Text: "plan", Signature: "native-signature"},
		{Type: "redacted_thinking", Data: "opaque"},
		{Type: "text", Text: "answer"},
	}, Stop: "stop"}
	payload, err := reply.JSON("gemini", "alias")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"text":"plan","thought":true`) || !strings.Contains(string(payload), `"text":"answer"`) || strings.Contains(string(payload), "native-signature") || strings.Contains(string(payload), "opaque") {
		t.Fatalf("unexpected Gemini response: %s", payload)
	}
}
