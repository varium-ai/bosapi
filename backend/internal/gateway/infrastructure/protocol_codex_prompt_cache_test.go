package infrastructure

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexPromptCacheKeyIsPreserved(t *testing.T) {
	q, err := ParseRelayRequest([]byte(`{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"hello"}],"prompt_cache_key":"bosagent-session"}`), "chat", "openai_responses")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, err := q.CodexUpstream()
	if err != nil {
		t.Fatalf("upstream: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["prompt_cache_key"] != "bosagent-session" {
		t.Fatalf("prompt cache key = %#v", body["prompt_cache_key"])
	}
}

func TestCodexPromptCacheKeyRejectsInvalidValues(t *testing.T) {
	for _, raw := range []string{
		`{"model":"m","messages":[{"role":"user","content":"x"}],"prompt_cache_key":true}`,
		`{"model":"m","messages":[{"role":"user","content":"x"}],"prompt_cache_key":""}`,
	} {
		_, err := ParseRelayRequest([]byte(raw), "chat", "openai_responses")
		if err == nil || !strings.Contains(err.Error(), "prompt_cache_key") {
			t.Fatalf("expected prompt_cache_key error for %s, got %v", raw, err)
		}
	}
}

func TestCodexPromptCacheKeyIsRejectedForOtherTargets(t *testing.T) {
	_, err := ParseRelayRequest([]byte(`{"model":"m","messages":[{"role":"user","content":"x"}],"prompt_cache_key":"k"}`), "chat", "anthropic_messages")
	if err == nil || !strings.Contains(err.Error(), "prompt_cache_key") {
		t.Fatalf("expected target-specific rejection, got %v", err)
	}
}
