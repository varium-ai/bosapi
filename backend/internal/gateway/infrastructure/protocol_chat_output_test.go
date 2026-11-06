package infrastructure

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestMessagesChatStructuredOutputPreservesSchema(t *testing.T) {
	const schema = `{"type":"object","properties":{"id":{"type":"integer","const":9007199254740993},"nested":{"$ref":"#/$defs/nested"}},"required":["id"],"additionalProperties":false,"$defs":{"nested":{"type":"object","properties":{"cache_control":{"type":"string"}},"additionalProperties":true}},"x-provider-extension":{"format":"opaque"}}`
	wantSchema, err := DecodeProtocolJSON([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"openai_chat", ""} {
		for _, stream := range []string{"true", "false"} {
			t.Run(target+"/stream="+stream, func(t *testing.T) {
				raw := `{"model":"alias","stream":` + stream + `,"output_config":{"effort":"high","format":{"type":"json_schema","schema":` + schema + `}},"messages":[{"role":"user","content":"hello"}]}`
				q, err := ParseRelayRequest([]byte(raw), "messages", target)
				if err != nil {
					t.Fatal(err)
				}
				payload, path, err := q.Upstream(target)
				if err != nil || path != "chat/completions" {
					t.Fatalf("upstream: %s %v", path, err)
				}
				root, err := DecodeProtocolJSON(payload)
				if err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "response", "strict": true, "schema": wantSchema}}
				if !reflect.DeepEqual(root["response_format"], want) {
					t.Fatalf("schema or constraints changed: %s", payload)
				}
				if root["stream"] != (stream == "true") || root["output_config"] != nil || root["reasoning_effort"] != nil {
					t.Fatalf("client controls changed: %s", payload)
				}
				for _, other := range []string{"anthropic_messages", "gemini"} {
					if _, _, err := q.Upstream(other); err == nil {
						t.Fatalf("structured output silently lost for %s", other)
					}
				}
			})
		}
	}
}

func TestMessagesChatNullOutputFormatIsOmitted(t *testing.T) {
	for _, config := range []string{`{"format":null}`, `{"effort":"high","format":null}`} {
		raw := strings.Replace(messagesRelayFixture, `"model":`, `"output_config":`+config+`,"model":`, 1)
		q, err := ParseRelayRequest([]byte(raw), "messages", "openai_chat")
		if err != nil {
			t.Fatal(err)
		}
		payload, _, err := q.Upstream("openai_chat")
		if err != nil {
			t.Fatal(err)
		}
		root, _ := DecodeProtocolJSON(payload)
		for _, key := range []string{"output_config", "response_format"} {
			if _, exists := root[key]; exists {
				t.Fatalf("null output format leaked upstream: %s", payload)
			}
		}
	}
}

func TestMessagesChatStructuredOutputRejectsInvalidEnvelope(t *testing.T) {
	for _, tc := range []struct{ format, field string }{
		{`false`, "output_config.format"},
		{`[]`, "output_config.format"},
		{`"json_schema"`, "output_config.format"},
		{`{}`, "output_config.format.type"},
		{`{"type":null}`, "output_config.format.type"},
		{`{"type":{}}`, "output_config.format.type"},
		{`{"type":"json_object"}`, "output_config.format.type"},
		{`{"type":"json_schema"}`, "output_config.format.schema"},
		{`{"type":"json_schema","schema":null}`, "output_config.format.schema"},
		{`{"type":"json_schema","schema":[]}`, "output_config.format.schema"},
		{`{"type":"json_schema","schema":true}`, "output_config.format.schema"},
		{`{"type":"json_schema","schema":"PRIVATE_SCHEMA"}`, "output_config.format.schema"},
		{`{"type":"json_schema","schema":{},"strict":false}`, "output_config.format.strict"},
		{`{"type":"json_schema","schema":{},"future":"PRIVATE_VALUE"}`, "output_config.format.future"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			raw := `{"model":"alias","output_config":{"format":` + tc.format + `},"messages":[{"role":"user","content":"hello"}]}`
			_, err := ParseRelayRequest([]byte(raw), "messages", "openai_chat")
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Field != tc.field || strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatalf("error=%v want field=%s without private values", err, tc.field)
			}
		})
	}
}

func TestMessagesStructuredOutputDoesNotWidenOtherProtocols(t *testing.T) {
	for _, format := range []string{`null`, `{"type":"json_schema","schema":{}}`} {
		raw := `{"model":"alias","output_config":{"format":` + format + `},"messages":[{"role":"user","content":"hello"}]}`
		for _, target := range []string{"gemini", "anthropic_messages"} {
			if _, err := ParseRelayRequest([]byte(raw), "messages", target); err == nil {
				t.Fatalf("Chat output compatibility escaped into %s", target)
			}
		}
		if _, err := ParseRelayRequest([]byte(raw), "chat", "openai_chat"); err == nil {
			t.Fatal("Anthropic field accepted in a Chat request")
		}
	}
}
