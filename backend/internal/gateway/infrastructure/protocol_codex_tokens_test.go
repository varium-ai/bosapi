package infrastructure

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexTokenLimitMapping(t *testing.T) {
	for _, field := range []string{`"max_tokens":32`, `"max_completion_tokens":32`, `"max_tokens":32,"max_completion_tokens":32`} {
		q, err := ParseRelayRequest([]byte(`{"model":"x","messages":[{"role":"user","content":"hi"}],`+field+`}`), "chat", "openai_responses")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := q.CodexUpstream()
		if err != nil {
			t.Fatal(err)
		}
		root, err := DecodeProtocolJSON(raw)
		if err != nil || root["max_output_tokens"] != json.Number("32") || root["max_tokens"] != nil || root["max_completion_tokens"] != nil || q.Controls["max_tokens"] != json.Number("32") {
			t.Fatalf("bad token mapping or mutated request: %s %v", raw, err)
		}
	}
	for _, field := range []string{`"max_tokens":0`, `"max_tokens":-1`, `"max_tokens":1.5`, `"max_tokens":"32"`, `"max_tokens":null`, `"max_completion_tokens":0`, `"max_completion_tokens":{}`, `"max_tokens":32,"max_completion_tokens":16`} {
		if _, err := ParseRelayRequest([]byte(`{"model":"x","messages":[{"role":"user","content":"hi"}],`+field+`}`), "chat", "openai_responses"); err == nil {
			t.Fatalf("accepted invalid token limit: %s", field)
		}
	}
	q, err := ParseRelayRequest([]byte(`{"model":"x","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"temperature":0.5}`), "chat", "openai_responses")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CodexUpstream(); err == nil || !strings.Contains(err.Error(), "temperature") {
		t.Fatalf("sampling control unexpectedly accepted: %v", err)
	}
}
