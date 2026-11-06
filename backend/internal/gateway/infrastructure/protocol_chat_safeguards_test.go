package infrastructure

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestClaudeChatOptionalSafeguards(t *testing.T) {
	baseline, err := ParseRelayRequest([]byte(messagesRelayFixture), "messages", "openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := baseline.Upstream("openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"openai_chat", ""} {
		for _, value := range []string{
			`null`, `[]`,
			`[{"type":"dangerous_tool_use"}]`,
			`[{"type":"dangerous_tool_use","classifier_context":null}]`,
			`[{"type":"dangerous_tool_use","classifier_context":{"v":1,"permission_mode":"auto","home_dir":"PRIVATE_HOME","rules":{"allow":[],"deny":["PRIVATE_RULE"]},"future_context":{"enabled":true}}}]`,
		} {
			t.Run(target+"/"+value, func(t *testing.T) {
				raw := strings.Replace(messagesRelayFixture, `"model":`, `"safeguards":`+value+`,"model":`, 1)
				q, err := ParseRelayRequest([]byte(raw), "messages", target)
				if err != nil {
					t.Fatal(err)
				}
				got, path, err := q.Upstream(target)
				if err != nil || path != "chat/completions" || !bytes.Equal(got, want) {
					t.Fatalf("optional checks changed the upstream request: path=%s err=%v body=%s", path, err, got)
				}
			})
		}
	}
}

func TestClaudeChatSafeguardsRejectUnsupportedEnvelopes(t *testing.T) {
	for _, tc := range []struct{ value, field string }{
		{`{}`, "safeguards"},
		{`true`, "safeguards"},
		{`"dangerous_tool_use"`, "safeguards"},
		{`[null]`, "safeguards[0]."},
		{`[[]]`, "safeguards[0]."},
		{`[{}]`, "safeguards"},
		{`[{"type":"future"}]`, "safeguards"},
		{`[{"type":"dangerous_tool_use","required":true}]`, "safeguards[0].required"},
		{`[{"type":"dangerous_tool_use","classifier_context":[]}]`, "safeguards[0].classifier_context"},
		{`[{"type":"dangerous_tool_use","classifier_context":"PRIVATE_CONTEXT"}]`, "safeguards[0].classifier_context"},
		{`[{"type":"dangerous_tool_use"},{"type":"future"}]`, "safeguards"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			raw := `{"model":"alias","safeguards":` + tc.value + `,"messages":[{"role":"user","content":"hello"}]}`
			_, err := ParseRelayRequest([]byte(raw), "messages", "openai_chat")
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Field != tc.field {
				t.Fatalf("error=%v want field=%s", err, tc.field)
			}
			if strings.Contains(err.Error(), "PRIVATE_CONTEXT") {
				t.Fatal("classifier context leaked into the diagnostic")
			}
		})
	}
}

func TestClaudeChatSafeguardsStayWithinChatBridge(t *testing.T) {
	for _, route := range []struct{ source, target string }{
		{"messages", "gemini"}, {"chat", "anthropic_messages"}, {"chat", "gemini"},
	} {
		t.Run(route.source+"/"+route.target, func(t *testing.T) {
			raw := `{"model":"alias","safeguards":[{"type":"dangerous_tool_use"}],"messages":[{"role":"user","content":"hello"}]}`
			_, err := ParseRelayRequest([]byte(raw), route.source, route.target)
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Field != "safeguards" {
				t.Fatalf("safeguards compatibility escaped the Chat bridge: %v", err)
			}
		})
	}
}
