package infrastructure

import (
	"testing"

	"bosapi/backend/internal/gateway/domain"
)

func TestToolAuthorizationUsesOnlyExactCallerDeclarations(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		protocol      domain.ToolProtocol
		allowed       bool
	}{
		{"chat", `{"tools":[{"type":"function","function":{"name":"lookup"}}]}`, domain.ToolProtocolOpenAI, true},
		{"responses", `{"tools":[{"type":"function","name":"lookup"}]}`, domain.ToolProtocolOpenAI, true},
		{"anthropic", `{"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`, domain.ToolProtocolAnthropic, true},
		{"websocket", `{"type":"response.create","response":{"tools":[{"type":"function","name":"lookup"}]}}`, domain.ToolProtocolOpenAI, true},
		{"absent", `{}`, domain.ToolProtocolOpenAI, false},
		{"empty", `{"tools":[]}`, domain.ToolProtocolOpenAI, false},
		{"none", `{"tools":[{"type":"function","name":"lookup"}],"tool_choice":"none"}`, domain.ToolProtocolOpenAI, false},
		{"anthropic none", `{"tools":[{"name":"lookup","input_schema":{}}],"tool_choice":{"type":"none"}}`, domain.ToolProtocolAnthropic, false},
		{"case sensitive", `{"tools":[{"type":"function","name":"Lookup"}]}`, domain.ToolProtocolOpenAI, false},
		{"history is not authorization", `{"messages":[{"role":"assistant","tool_calls":[{"function":{"name":"lookup"}}]}]}`, domain.ToolProtocolOpenAI, false},
		{"text is not authorization", `{"tools":[{"type":"function","name":"other","description":"lookup"}],"content":"<tool>lookup</tool>"}`, domain.ToolProtocolOpenAI, false},
		{"protocol separation", `{"tools":[{"type":"function","name":"lookup"}]}`, domain.ToolProtocolAnthropic, false},
		{"hosted tools", `{"tools":[{"type":"web_search","name":"lookup"}]}`, domain.ToolProtocolOpenAI, false},
		{"invalid", `{"tools":`, domain.ToolProtocolOpenAI, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := ParseToolAuthorization([]byte(test.payload))
			if allowed := policy.Allows(domain.ToolCall{Protocol: test.protocol, Name: "lookup"}); allowed != test.allowed {
				t.Fatalf("allowed=%t, want=%t", allowed, test.allowed)
			}
		})
	}
}
