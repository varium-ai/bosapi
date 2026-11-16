package infrastructure

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGeminiRelayRequestConvertsNativeRequestToOpenAIChat(t *testing.T) {
	raw := `{"systemInstruction":{"parts":[{"text":"follow policy"}]},"contents":[{"role":"user","parts":[{"text":"inspect"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]},{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"lookup","args":{"q":"x","n":9007199254740993}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call-1","name":"lookup","response":{"result":"found"}}}]}],"generationConfig":{"maxOutputTokens":512,"temperature":0.4,"topP":0.8,"stopSequences":["END"]},"tools":[{"functionDeclarations":[{"name":"lookup","description":"find data","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["lookup"]}}}`
	q, err := ParseRelayRequest([]byte(raw), "gemini", "openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	q.Model = "actual"
	q.Stream = true
	payload, path, err := q.Upstream("openai_chat")
	if err != nil {
		t.Fatal(err)
	}
	if path != "chat/completions" {
		t.Fatalf("path=%s", path)
	}
	root, err := DecodeProtocolJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	choice, _ := root["tool_choice"].(map[string]any)
	function, _ := choice["function"].(map[string]any)
	if root["model"] != "actual" || root["stream"] != true || root["max_tokens"] != json.Number("512") || root["temperature"] != json.Number("0.4") || root["top_p"] != json.Number("0.8") || choice["type"] != "function" || function["name"] != "lookup" {
		t.Fatalf("wrong OpenAI controls: %s", payload)
	}
	if !strings.Contains(string(payload), `"role":"system"`) || !strings.Contains(string(payload), `follow policy`) || !strings.Contains(string(payload), `"image_url":{"url":"data:image/png;base64,aGVsbG8="}`) || !strings.Contains(string(payload), `"tool_call_id":"call-1"`) || !strings.Contains(string(payload), `9007199254740993`) || !strings.Contains(string(payload), `"name":"lookup"`) {
		t.Fatalf("Gemini request semantics lost: %s", payload)
	}
}

func TestGeminiRelayRequestPreservesAutoModeAndRejectsUnsupportedFields(t *testing.T) {
	raw := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parametersJsonSchema":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"AUTO","allowedFunctionNames":["lookup"]}}}`
	q, err := ParseRelayRequest([]byte(raw), "gemini", "openai_chat")
	if err != nil || q.Choice != "auto" || len(q.Tools) != 1 {
		t.Fatalf("AUTO mode changed: choice=%s tools=%d err=%v", q.Choice, len(q.Tools), err)
	}
	for _, tc := range []struct{ raw, field string }{
		{`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"seed":1}}`, "generationConfig.seed"},
		{`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"candidateCount":2}}`, "generationConfig.candidateCount"},
		{`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"not-base64!"}}]}]}`, "contents[0].parts[0].inlineData.data"},
	} {
		_, err := ParseRelayRequest([]byte(tc.raw), "gemini", "openai_chat")
		var failure *ConversionError
		if !errors.As(err, &failure) || failure.Field != tc.field {
			t.Fatalf("error=%v expected field=%s", err, tc.field)
		}
	}
}

func TestOpenAIChatRelayReplyRendersGeminiResponse(t *testing.T) {
	chat := `{"id":"chatcmpl-1","choices":[{"index":0,"message":{"role":"assistant","content":"hello","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}`
	reply, err := ParseRelayReply([]byte(chat), "openai_chat", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := reply.JSON("gemini", "alias")
	if err != nil {
		t.Fatal(err)
	}
	root, err := DecodeProtocolJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	candidate := root["candidates"].([]any)[0].(map[string]any)
	usage := root["usageMetadata"].(map[string]any)
	if candidate["finishReason"] != "STOP" || !strings.Contains(string(payload), `"role":"model"`) || !strings.Contains(string(payload), `"functionCall"`) || usage["cachedContentTokenCount"] != json.Number("3") || usage["candidatesTokenCount"] != json.Number("3") || usage["thoughtsTokenCount"] != json.Number("2") || usage["totalTokenCount"] != json.Number("17") {
		t.Fatalf("wrong Gemini response: %s", payload)
	}
	length, err := ParseRelayReply([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}]}`), "openai_chat", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	payload, err = length.JSON("gemini", "alias")
	if err != nil || !strings.Contains(string(payload), `"finishReason":"MAX_TOKENS"`) {
		t.Fatalf("length finish reason was not mapped: %s err=%v", payload, err)
	}
}
