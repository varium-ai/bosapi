package infrastructure

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCurrentUserInputExcludesHistoricalSecretsAndMetadata(t *testing.T) {
	for _, payload := range []string{
		`{"contents":[{"role":"user","parts":[{"text":"password=OLD_USER_SECRET"}]},{"role":"user","parts":[{"text":"你好"},{"functionResponse":{"response":{"password":"TOOL_SECRET"}}}]}],"systemInstruction":{"parts":[{"text":"password=SYSTEM_SECRET"}]}}`,
		`{"contents":[{"role":"user","parts":[{"text":"password=OLD_USER_SECRET"}]},{"role":"model","parts":[{"text":"password=ASSISTANT_SECRET"}]}]}`,
		`{"model":"test","messages":[{"role":"system","content":"password=SYSTEM_SECRET"},{"role":"developer","content":"token=DEVELOPER_SECRET"},{"role":"user","content":"api_key=OLD_USER_SECRET"},{"role":"assistant","content":"password=OLD_ASSISTANT_SECRET"},{"role":"tool","content":"token=TOOL_SECRET"},{"role":"user","content":"你好"}],"instructions":"password=ROOT_SECRET"}`,
		`{"input":[{"role":"user","content":"password=OLD_USER_SECRET"},{"type":"function_call","arguments":"{\"password\":\"OLD_ARGUMENT_SECRET\"}"},{"type":"function_call_output","output":"password=TOOL_SECRET"},{"role":"user","content":[{"type":"input_text","text":"你好"}]}],"instructions":"password=SYSTEM_SECRET"}`,
		`{"system":"password=SYSTEM_SECRET","messages":[{"role":"user","content":"password=OLD_USER_SECRET"},{"role":"assistant","content":"password=OLD_ASSISTANT_SECRET"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"old","content":"password=TOOL_SECRET"},{"type":"text","text":"你好"}]}]}`,
		`{"messages":[{"role":"user","content":"password=OLD_USER_SECRET"},{"role":"tool","content":"password=TOOL_SECRET"}]}`,
		`{"messages":[{"role":"user","content":"password=OLD_USER_SECRET"},{"role":"assistant","content":"password=ASSISTANT_PREFILL"}]}`,
		`{"messages":[{"role":"user","content":"password=OLD_USER_SECRET"},{"role":"user","content":[{"type":"tool_result","content":"password=TOOL_SECRET"}]}]}`,
		`{"input":[{"role":"user","content":"password=OLD_USER_SECRET"},{"type":"function_call_output","output":"password=TOOL_SECRET"}]}`,
		`{"input":[{"type":"function_call","arguments":"{\"password\":\"OLD_ARGUMENT_SECRET\"}"}]}`,
		`{"model":"test","instructions":"password=SYSTEM_SECRET","messages":[]}`,
		`{"messages":[{"role":"user","content":"password is 432432"},{"role":"assistant","content":"Got it"},{"role":"user","content":"你好"}]}`,
		`{"model":"test","instructions":"password=SYSTEM_SECRET"}`,
		`{"text":{"format":{"schema":{"password":"SCHEMA_SECRET"}}}}`,
		`{"input":[{"type":"input_image","image_url":"https://example.test/?api_key=IMAGE_METADATA_SECRET"},{"type":"input_text","text":"你好"}]}`,
	} {
		result, err := (RuleSecurityEventSink{}).InspectContent(context.Background(), "prompt", []byte(payload))
		masked, changed, maskErr := (RuleSecurityEventSink{}).SanitizeContent(context.Background(), "prompt", []byte(payload))
		if err != nil || maskErr != nil || result.HitCount != 0 || len(result.Evidence) != 0 || changed || string(masked) != payload {
			t.Fatalf("history inspected or modified: %s => %+v / %s / %v / %v", payload, result, masked, err, maskErr)
		}
	}
}

func TestOnlyCurrentUserTextIsRedactedWithOriginalEvidenceIndices(t *testing.T) {
	for _, test := range []struct {
		name, payload, expectedPath string
	}{
		{"chat", `{"model":"test","messages":[{"role":"user","content":"password=OLD_USER_SECRET"},{"role":"assistant","content":"password=OLD_ASSISTANT_SECRET"},{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/?password=MEDIA_SECRET"}},{"type":"text","text":"本次 api_key=NEW_SECRET"}]}],"instructions":"password=SYSTEM_SECRET","count":9007199254740993}`, "messages[2].content[1].text"},
		{"responses", `{"input":[{"role":"user","content":"password=OLD_USER_SECRET"},{"type":"function_call_output","output":"password=TOOL_SECRET"},{"type":"message","role":"user","content":[{"type":"input_image","image_url":"password=MEDIA_SECRET"},{"type":"input_text","text":"本次 api_key=NEW_SECRET"}]}],"instructions":"password=SYSTEM_SECRET"}`, "input[2].content[1].text"},
		{"anthropic", `{"messages":[{"role":"user","content":"password=OLD_USER_SECRET"},{"role":"user","content":[{"type":"tool_result","content":"password=TOOL_SECRET"},{"type":"text","text":"本次 api_key=NEW_SECRET"}]}],"system":"password=SYSTEM_SECRET"}`, "messages[1].content[1].text"},
		{"implicit response message", `{"input":[{"content":"本次 api_key=NEW_SECRET"}]}`, "input[0].content"},
		{"input string", `{"input":"api_key=NEW_SECRET","instructions":"password=SYSTEM_SECRET"}`, "input"},
		{"direct input block", `{"input":[{"type":"input_text","text":"api_key=NEW_SECRET"}]}`, "input[0].text"},
		{"input batch", `{"input":["你好","api_key=NEW_SECRET"]}`, "input[1]"},
		{"completion", `{"prompt":"api_key=NEW_SECRET","system":"password=SYSTEM_SECRET"}`, "prompt"},
		{"trailing policy", `{"messages":[{"role":"user","content":"password=OLD_USER_SECRET"},{"role":"user","content":"api_key=NEW_SECRET"},{"role":"developer","content":"password=SYSTEM_SECRET"}]}`, "messages[1].content"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := (RuleSecurityEventSink{}).InspectContent(context.Background(), "prompt", []byte(test.payload))
			masked, changed, maskErr := (RuleSecurityEventSink{}).SanitizeContent(context.Background(), "prompt", []byte(test.payload))
			if err != nil || maskErr != nil || result.HitCount != 1 || len(result.Evidence) != 1 || result.Evidence[0].Path != test.expectedPath || !changed || strings.Contains(string(masked), "NEW_SECRET") {
				t.Fatalf("current input not detected/redacted: %+v / %s / %v / %v", result, masked, err, maskErr)
			}
			// Compare every value to prove that only the new secret changed,
			// even when marshalling reorders keys or escapes JSON text.
			decoder := json.NewDecoder(strings.NewReader(test.payload))
			decoder.UseNumber()
			var expected, actual any
			if err := decoder.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			expected = replaceCurrentTestSecret(expected)
			decoder = json.NewDecoder(strings.NewReader(string(masked)))
			decoder.UseNumber()
			if err := decoder.Decode(&actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("history/metadata or numeric precision changed: %s", masked)
			}
		})
	}
}

func replaceCurrentTestSecret(value any) any {
	switch value := value.(type) {
	case string:
		return strings.ReplaceAll(value, "NEW_SECRET", passwordHide)
	case []any:
		for i, item := range value {
			value[i] = replaceCurrentTestSecret(item)
		}
		return value
	case map[string]any:
		for key, item := range value {
			value[key] = replaceCurrentTestSecret(item)
		}
		return value
	default:
		return value
	}
}

func TestGeminiCurrentPromptSanitization(t *testing.T) {
	payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"password=OLD_USER_SECRET"}]},{"role":"user","parts":[{"text":"password=NEW_USER_SECRET"},{"functionResponse":{"response":{"password":"TOOL_SECRET"}}}]}],"systemInstruction":{"parts":[{"text":"password=SYSTEM_SECRET"}]}}`)
	masked, changed, err := (RuleSecurityEventSink{}).SanitizeContent(context.Background(), "prompt", payload)
	if err != nil || !changed || strings.Contains(string(masked), "NEW_USER_SECRET") || !strings.Contains(string(masked), "OLD_USER_SECRET") || !strings.Contains(string(masked), "SYSTEM_SECRET") || !strings.Contains(string(masked), "TOOL_SECRET") {
		t.Fatalf("Gemini sanitation: %s %v", masked, err)
	}
}
