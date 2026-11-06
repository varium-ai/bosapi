package infrastructure

import (
	"bosapi/backend/internal/gateway/domain"
	"strings"
	"testing"
)

func TestGeminiToolHistoryAndNamesAreNotParsedRecursively(t *testing.T) {
	payload := []byte(`{"tools":[{"functionDeclarations":[{"name":"definition"}]}],"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"n":9007199254740993,"functionCall":{"name":"not-a-call"}}},"thoughtSignature":"signature"}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"ok":true}}}]}]}`)
	calls, err := NewToolCallParser(domain.ToolCallContext{}, nil).Parse(payload)
	if err != nil || len(calls) != 2 || calls[0].Protocol != domain.ToolProtocolGemini || calls[1].Name != "lookup" || calls[0].ID != calls[1].ID || !strings.Contains(string(calls[1].Arguments), "9007199254740993") {
		t.Fatalf("calls=%+v err=%v", calls, err)
	}
	for _, payload := range []string{
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":"incomplete"}}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","partialArgs":[{}],"willContinue":true}}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":null}]}}]}`,
	} {
		if _, err := NewToolCallParser(domain.ToolCallContext{}, nil).Parse([]byte(payload)); err == nil {
			t.Fatalf("invalid call accepted: %s", payload)
		}
	}
}

func TestGeminiToolDeclarationsRespectCallingConfig(t *testing.T) {
	for _, test := range []struct {
		mode  string
		allow bool
	}{{"AUTO", true}, {"NONE", false}} {
		payload := []byte(`{"tools":[{"functionDeclarations":[{"name":"lookup"},{"name":"shell"}]}],"toolConfig":{"functionCallingConfig":{"mode":"` + test.mode + `","allowedFunctionNames":["lookup"]}}}`)
		auth := ParseToolAuthorization(payload)
		if auth.Allows(domain.ToolCall{Protocol: domain.ToolProtocolGemini, Name: "lookup"}) != test.allow || auth.Allows(domain.ToolCall{Protocol: domain.ToolProtocolGemini, Name: "shell"}) {
			t.Fatalf("incorrect authorization: %+v", auth)
		}
	}
}

func TestGeminiToolFilterHandlesFragmentedSSEWithoutHoldingText(t *testing.T) {
	filter := NewToolDeliveryFilter(domain.ToolCallContext{}, nil, func(call domain.ToolCall) bool { return call.Name == "lookup" }, func(err error) { t.Errorf("parse: %v", err) })
	text := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n"
	if chunks := filter.SSE([]byte(text), false); len(chunks) != 1 || string(chunks[0]) != text {
		t.Fatal("text buffered or changed")
	}
	frame := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"shell","args":{"command":"PRIVATE"}}},{"functionCall":{"id":"allowed","name":"lookup","args":{}},"thoughtSignature":"signed"}]}}]}`
	stream := "data: " + frame + "\n\n"
	var output strings.Builder
	for _, b := range []byte(stream) {
		for _, chunk := range filter.SSE([]byte{b}, false) {
			output.Write(chunk)
		}
	}
	if strings.Contains(output.String(), "PRIVATE") || strings.Contains(output.String(), "shell") || !strings.Contains(output.String(), "allowed") || !strings.Contains(output.String(), "signed") {
		t.Fatalf("unsafe or corrupted output: %s", output.String())
	}
}
