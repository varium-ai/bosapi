package infrastructure

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const chatRelayFixture = `{"model":"alias","stream":true,"stream_options":{"include_usage":true},"max_tokens":512,"temperature":0.7,"top_p":0.9,"stop":["END"],"n":1,"messages":[{"role":"system","content":"instructions"},{"role":"user","content":"read"},{"role":"assistant","content":null,"tool_calls":[{"id":"old","type":"function","function":{"name":"read","arguments":"{\"id\":9007199254740993}"}}]},{"role":"tool","tool_call_id":"old","content":"file content"},{"role":"user","content":[{"type":"text","text":"continue"}]}],"tools":[{"type":"function","function":{"name":"read","description":"read file","parameters":{"type":"object","properties":{"id":{"type":"integer"}}}}}],"tool_choice":{"type":"function","function":{"name":"read"}}}`
const messagesRelayFixture = `{"model":"alias","stream":true,"max_tokens":512,"system":[{"type":"text","text":"instructions"}],"messages":[{"role":"user","content":"read"},{"role":"assistant","content":[{"type":"text","text":"calling"},{"type":"tool_use","id":"old","name":"read","input":{"id":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"old","content":[{"type":"text","text":"file content"}],"is_error":true},{"type":"text","text":"continue"}]}],"tools":[{"name":"read","input_schema":{"type":"object"}}],"tool_choice":{"type":"any"}}`

func TestRelayRequestPairsPreserveCommonSemantics(t *testing.T) {
	for _, test := range []struct{ source, target, raw string }{{"chat", "anthropic_messages", chatRelayFixture}, {"chat", "gemini", chatRelayFixture}, {"messages", "gemini", messagesRelayFixture}} {
		t.Run(test.source+"_"+test.target, func(t *testing.T) {
			q, err := ParseRelayRequest([]byte(test.raw), test.source, test.target)
			if err != nil {
				t.Fatal(err)
			}
			payload, path, err := q.Upstream(test.target)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(payload), "9007199254740993") || !strings.Contains(string(payload), "instructions") || !strings.Contains(string(payload), "file content") {
				t.Fatalf("content lost: %s", payload)
			}
			root, err := DecodeProtocolJSON(payload)
			if err != nil {
				t.Fatal(err)
			}
			if test.target == "anthropic_messages" {
				if path != "messages" || root["max_tokens"] != json.Number("512") || root["stream"] != true || root["tool_choice"].(map[string]any)["type"] != "tool" {
					t.Fatalf("wrong Anthropic request: %s", payload)
				}
			} else {
				if path != "models/alias:streamGenerateContent" || root["generationConfig"].(map[string]any)["maxOutputTokens"] != json.Number("512") {
					t.Fatalf("wrong Gemini request: %s", payload)
				}
				if !strings.Contains(string(payload), "functionCall") || !strings.Contains(string(payload), "functionResponse") || !strings.Contains(string(payload), "parametersJsonSchema") {
					t.Fatalf("tool protocol lost: %s", payload)
				}
				if test.source == "messages" && !strings.Contains(string(payload), `"error":"file content"`) {
					t.Fatalf("tool error lost: %s", payload)
				}
			}
		})
	}
}

func TestRelayRejectsUnsupportedFieldsAtTheirPaths(t *testing.T) {
	cases := []struct{ source, target, raw, field string }{
		{"chat", "gemini", `{"model":"x","messages":[{"role":"user","content":"hi"}],"seed":1}`, "seed"},
		{"chat", "anthropic_messages", `{"model":"x","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema"}}`, "response_format"},
		{"chat", "gemini", `{"model":"x","messages":[{"role":"user","content":"hi"}],"parallel_tool_calls":false}`, "parallel_tool_calls"},
		{"chat", "gemini", `{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"secret"}}]}]}`, "messages[0].content[0].image_url"},
		{"chat", "gemini", `{"model":"x","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"read","strict":true}}]}`, "tools[0].function.strict"},
		{"chat", "gemini", `{"model":"x","messages":[{"role":"system","content":"a"},{"role":"developer","content":"b"},{"role":"user","content":"hi"}]}`, "messages[1].role"},
		{"messages", "gemini", `{"model":"x","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"}}`, "thinking"},
		{"messages", "gemini", `{"model":"x","system":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"}]}`, "system[0].cache_control"},
		{"messages", "gemini", `{"model":"x","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"signed"}]}]}`, "messages[0].content[0].type"},
		{"chat", "gemini", `{"model":"x","messages":[{"role":"assistant","content":"prefill"}]}`, "messages"},
		{"chat", "gemini", `{"model":"x","messages":[{"role":"user","content":"hi"}],"stream_options":{"include_usage":true}}`, "stream_options"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			_, err := ParseRelayRequest([]byte(tc.raw), tc.source, tc.target)
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Field != tc.field {
				t.Fatalf("error=%v expected field=%s", err, tc.field)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
				t.Fatal("content leaked in error")
			}
		})
	}
}

func TestRelayResponseToolsUsageAndBoundaries(t *testing.T) {
	cases := []struct{ target, raw string }{
		{"anthropic_messages", `{"id":"msg","type":"message","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"new","name":"read","input":{"id":9007199254740993}}],"stop_reason":"tool_use","usage":{"input_tokens":11,"cache_read_input_tokens":4,"cache_creation_input_tokens":2,"output_tokens":7}}`},
		{"gemini", `{"responseId":"gem","candidates":[{"content":{"parts":[{"text":"hello"},{"functionCall":{"id":"new","name":"read","args":{"id":9007199254740993}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"cachedContentTokenCount":4,"candidatesTokenCount":5,"thoughtsTokenCount":2}}`},
	}
	for _, tc := range cases {
		reply, err := ParseRelayReply([]byte(tc.raw), tc.target, "fallback")
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{"chat", "messages"} {
			payload, err := reply.JSON(source, "alias")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(payload), "9007199254740993") || !strings.Contains(string(payload), "hello") {
				t.Fatalf("response data lost: %s", payload)
			}
		}
		if reply.ChatUsage()["total_tokens"] != int64(24) {
			t.Fatalf("usage: %+v", reply)
		}
	}
	_, err := ParseRelayReply([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{}},"thoughtSignature":"signed"}]},"finishReason":"STOP"}]}`), "gemini", "id")
	var failure *ConversionError
	if !errors.As(err, &failure) || !strings.Contains(failure.Field, "thoughtSignature") {
		t.Fatalf("signature silently dropped: %v", err)
	}
}

func TestRelayParallelToolResultsStayInOneGeminiTurn(t *testing.T) {
	raw := []byte(`{"model":"alias","messages":[{"role":"user","content":"read"},{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"read","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"a","content":"result a"},{"role":"tool","tool_call_id":"b","content":"result b"},{"role":"user","content":"continue"}]}`)
	q, err := ParseRelayRequest(raw, "chat", "gemini")
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err := q.Upstream("gemini")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := DecodeProtocolJSON(payload)
	contents := root["contents"].([]any)
	if len(contents) != 3 || len(contents[2].(map[string]any)["parts"].([]any)) != 3 {
		t.Fatalf("parallel results separated: %s", payload)
	}
}

func TestRelayAcceptsNullableSDKMessageSnapshotsWithoutLosingFeatures(t *testing.T) {
	raw := []byte(`{"model":"alias","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello","tool_calls":null,"refusal":null,"audio":null,"annotations":[]},{"role":"user","content":"continue"}]}`)
	if _, err := ParseRelayRequest(raw, "chat", "gemini"); err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"audio":null`, `"audio":{"id":"media"}`, 1))
	_, err := ParseRelayRequest(raw, "chat", "gemini")
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Field != "messages[1].audio" {
		t.Fatalf("audio silently discarded: %v", err)
	}
}

func TestRelayImageBlocksConvertToEveryUpstream(t *testing.T) {
	base64Image := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}`
	urlImage := `{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}`
	cases := []struct{ name, source, target, image, expect string }{
		{"base64 to openai", "messages", "openai_chat", base64Image, `"image_url":{"url":"data:image/png;base64,aGVsbG8="}`},
		{"base64 to anthropic", "messages", "anthropic_messages", base64Image, `{"data":"aGVsbG8=","media_type":"image/png","type":"base64"}`},
		{"base64 to gemini", "messages", "gemini", base64Image, `{"inlineData":{"data":"aGVsbG8=","mimeType":"image/png"}}`},
		{"url to openai", "messages", "openai_chat", urlImage, `"image_url":{"url":"https://example.com/a.png"}`},
		{"url to anthropic", "messages", "anthropic_messages", urlImage, `{"type":"url","url":"https://example.com/a.png"}`},
		{"chat data url stays", "chat", "openai_chat", `{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,aGVsbG8="}}`, `"image_url":{"url":"data:image/jpeg;base64,aGVsbG8="}`},
		{"chat http url stays", "chat", "openai_chat", `{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}`, `"image_url":{"url":"https://example.com/a.png"}`},
		{"chat image detail to anthropic", "chat", "anthropic_messages", `{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":"high"}}`, `"type":"url","url":"https://example.com/a.png"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"model":"x","messages":[{"role":"user","content":[{"type":"text","text":"look"},%s]}]}`, tc.image)
			q, err := ParseRelayRequest([]byte(raw), tc.source, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			payload, _, err := q.Upstream(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(payload), "look") || !strings.Contains(string(payload), tc.expect) {
				t.Fatalf("image lost: %s", payload)
			}
		})
	}
}

func TestRelayRejectsImageDetailOutsideAnthropicConversion(t *testing.T) {
	raw := `{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":"high"}}]}]}`
	_, err := ParseRelayRequest([]byte(raw), "chat", "openai_chat")
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Field != "messages[0].content[0].image_url.detail" {
		t.Fatalf("error=%v want detail field rejection", err)
	}
}

func TestRelayRejectsInvalidImageDetailForAnthropicConversion(t *testing.T) {
	raw := `{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":false}}]}]}`
	_, err := ParseRelayRequest([]byte(raw), "chat", "anthropic_messages")
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Field != "messages[0].content[0].image_url.detail" {
		t.Fatalf("error=%v want invalid detail rejection", err)
	}
}

func TestRelayRejectsInvalidImageBlocks(t *testing.T) {
	cases := []struct{ name, source, target, raw, field string }{
		{"assistant image", "messages", "gemini", `{"model":"x","messages":[{"role":"assistant","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}`, "messages[0].content[0].type"},
		{"assistant image_url", "chat", "openai_chat", `{"model":"x","messages":[{"role":"assistant","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}]}`, "messages[0].content[0].image_url"},
		{"system image_url", "chat", "openai_chat", `{"model":"x","messages":[{"role":"system","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}]}`, "messages[0].content[0].image_url"},
		{"bad media type", "messages", "gemini", `{"model":"x","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"text/plain","data":"aGVsbG8="}}]}]}`, "messages[0].content[0].source.media_type"},
		{"bad base64", "messages", "gemini", `{"model":"x","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"!!!"}}]}]}`, "messages[0].content[0].source.data"},
		{"bad url", "messages", "gemini", `{"model":"x","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"ftp://example.com/a.png"}}]}]}`, "messages[0].content[0].source.url"},
		{"unknown source", "messages", "gemini", `{"model":"x","messages":[{"role":"user","content":[{"type":"image","source":{"type":"webp_b64","data":"aGVsbG8="}}]}]}`, "messages[0].content[0].source.type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRelayRequest([]byte(tc.raw), tc.source, tc.target)
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Field != tc.field {
				t.Fatalf("error=%v want field=%s", err, tc.field)
			}
			if strings.Contains(err.Error(), "aGVsbG8") || strings.Contains(err.Error(), "example.com") {
				t.Fatal("image content leaked in error")
			}
		})
	}
}

func TestRelayRejectsImageDetailForUnsupportedTargets(t *testing.T) {
	raw := `{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":"high"}}]}]}`
	for _, target := range []string{"openai_chat", "gemini"} {
		_, err := ParseRelayRequest([]byte(raw), "chat", target)
		var failure *ConversionError
		if !errors.As(err, &failure) || failure.Field != "messages[0].content[0].image_url.detail" {
			t.Fatalf("target=%s error=%v want detail field rejection", target, err)
		}
	}
}

func TestRelayRejectsInvalidImageDetailForAnthropicConversion(t *testing.T) {
	raw := `{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":false}}]}]}`
	_, err := ParseRelayRequest([]byte(raw), "chat", "anthropic_messages")
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Field != "messages[0].content[0].image_url.detail" {
		t.Fatalf("error=%v want invalid detail rejection", err)
	}
}

func TestRelayURLImageCannotReachGeminiUpstream(t *testing.T) {
	raw := []byte(`{"model":"x","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}]}]}`)
	q, err := ParseRelayRequest(raw, "messages", "gemini")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = q.Upstream("gemini")
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Field != "messages[0]" || strings.Contains(err.Error(), "example.com") {
		t.Fatalf("url image reached gemini: %v", err)
	}
}
