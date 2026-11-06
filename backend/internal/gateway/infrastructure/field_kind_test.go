package infrastructure

import (
	"encoding/json"
	"testing"

	"bosapi/backend/internal/gateway/domain"
)

// TestInspectionFieldKindsFollowPromptProtocol verifies that prompt inspection
// tags every extracted field as prompt content across the supported protocols.
func TestInspectionFieldKindsFollowPromptProtocol(t *testing.T) {
	// Each case exercises one prompt protocol or the non-JSON fallback.
	for _, test := range []struct {
		name, payload string
	}{
		{"openai", `{"messages":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`},
		{"gemini", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
		{"responses", `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`},
		{"completion", `{"prompt":"hello"}`},
		{"non-json fallback", "hello"},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := contentInspectionDocument("prompt", []byte(test.payload))
			if len(doc.fields) == 0 {
				t.Fatalf("empty prompt inspection document: %s", test.payload)
			}
			for _, field := range doc.fields {
				if field.kind != "prompt" {
					t.Fatalf("%s field kind=%q", test.name, field.kind)
				}
			}
		})
	}
}

// TestInspectionFieldKindsFollowModelOutputAndToolContent verifies the coarse
// kind assigned to response and untrusted tool content.
func TestInspectionFieldKindsFollowModelOutputAndToolContent(t *testing.T) {
	doc := contentInspectionDocument("model_output", []byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	if len(doc.fields) == 0 {
		t.Fatal("empty model output inspection document")
	}
	for _, field := range doc.fields {
		if field.kind != "model_output" {
			t.Fatalf("model output field kind=%q", field.kind)
		}
	}

	// Tool names, arguments, and results all belong to the tool kind even
	// though their evidence sources remain distinct.
	call := domain.ToolCall{
		Name:      "shell",
		Arguments: json.RawMessage(`{"command":"hello"}`),
		Result:    json.RawMessage(`"done"`),
	}
	doc = toolInspectionDocument(call)
	if len(doc.fields) < 3 {
		t.Fatalf("tool inspection fields=%d", len(doc.fields))
	}
	for _, field := range doc.fields {
		if field.kind != "tool_arguments" {
			t.Fatalf("tool field kind=%q", field.kind)
		}
	}
}

// TestFieldKindMapping verifies the coarse check kind derived from each
// fine-grained provenance source.
func TestFieldKindMapping(t *testing.T) {
	// Unknown sources have no check kind rather than being silently widened.
	for _, test := range []struct {
		source, kind string
	}{
		{"prompt", "prompt"},
		{"model_output", "model_output"},
		{"tool_arguments", "tool_arguments"},
		{"tool_result", "tool_arguments"},
		{"tool_name", "tool_arguments"},
		{"unknown", ""},
	} {
		if fieldKind(test.source) != test.kind {
			t.Fatalf("source %q mapped to %q, want %q", test.source, fieldKind(test.source), test.kind)
		}
		if fieldKindSupports("prompt", test.kind) && test.kind != "prompt" {
			t.Fatalf("field kind %q incorrectly supports prompt", test.kind)
		}
		if fieldKindSupports("tool_arguments", test.kind) && test.kind != "tool_arguments" {
			t.Fatalf("field kind %q incorrectly supports tool arguments", test.kind)
		}
	}
}
