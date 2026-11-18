package infrastructure

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"bosapi/backend/internal/gateway/domain"
)

func TestResponsesChatRejectsUnsupportedOptionsAndToolSelectors(t *testing.T) {
	for _, tc := range []struct{ field, suffix string }{
		{"tool_choice.tools[0].name", `"tools":[{"type":"function","name":"read"}],"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"write"}]}`},
		{"tool_choice.tools[0].name", `"tools":[{"type":"function","name":"files__read"}],"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","namespace":"files","name":"read"}]}`},
		{"tool_choice.tools[0].name", `"tools":[{"type":"namespace","name":"files","tools":[{"type":"function","name":"read"}]}],"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"files__read"}]}`},
		{"tool_choice.tools[0].type", `"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"web_search"}]}`},
		{"truncation", `"truncation":"auto"`},
		{"modalities", `"modalities":["text","audio"]`},
		{"stream_options.include_obfuscation", `"stream":true,"stream_options":{"include_obfuscation":true}`},
		{"stream_options", `"stream_options":{"include_obfuscation":false}`},
		{"safety_identifier", `"safety_identifier":true`},
	} {
		t.Run(tc.field, func(t *testing.T) {
			_, err := ParseResponsesForChat([]byte(`{"model":"m","input":"hi",` + tc.suffix + `}`))
			var conversion *ConversionError
			if !errors.As(err, &conversion) || conversion.Field != tc.field {
				t.Fatalf("error=%v, want conversion error at %s", err, tc.field)
			}
		})
	}
}

func TestResponsesNamespaceNamesAndConflicts(t *testing.T) {
	namespace := strings.Repeat("命名空间", 12)
	first := ResponsesFunctionName(namespace, "read")
	second := ResponsesFunctionName(namespace, "write")
	if len(first) > 64 || !utf8.ValidString(first) || first == second || first != ResponsesFunctionName(namespace, "read") {
		t.Fatalf("invalid namespace mapping: %q %q", first, second)
	}
	for _, tools := range []string{
		`[{"type":"function","name":"files__read"},{"type":"namespace","name":"files","tools":[{"type":"function","name":"read"}]}]`,
		`[{"type":"namespace","name":"a__b","tools":[{"type":"function","name":"c"}]},{"type":"namespace","name":"a","tools":[{"type":"function","name":"b__c"}]}]`,
	} {
		if _, err := ParseResponsesForChat([]byte(`{"model":"m","input":"hello","tools":` + tools + `}`)); err == nil {
			t.Fatalf("ambiguous tools accepted: %s", tools)
		}
	}
}

func TestResponsesNamespaceAuthorizationAndParser(t *testing.T) {
	payload := []byte(`{"tools":[{"type":"namespace","name":"files","children":[{"type":"function","name":"read"}]}]}`)
	authorization := ParseToolAuthorization(payload)
	for _, name := range []string{"read", "other__read", "files__read"} {
		if authorization.Allows(domain.ToolCall{Protocol: domain.ToolProtocolOpenAI, Name: name}) != (name == "files__read") {
			t.Fatalf("unexpected authorization for %q", name)
		}
	}
	parser := NewToolCallParser(domain.ToolCallContext{}, nil)
	calls, err := parser.Parse([]byte(`{"output":[{"type":"function_call","namespace":"files","name":"read","call_id":"c","arguments":"{}"}]}`))
	if err != nil || len(calls) != 1 || calls[0].Name != "files__read" {
		t.Fatalf("namespace parsing failed: calls=%+v err=%v", calls, err)
	}
}
