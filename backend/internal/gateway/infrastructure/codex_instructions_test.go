package infrastructure

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexChatSystemInstructions(t *testing.T) {
	for _, role := range []string{"system", "developer"} {
		raw := `{"model":"x","messages":[{"role":"` + role + `","content":"first"},{"role":"` + role + `","content":[{"type":"text","text":"second"}]},{"role":"user","content":"hello"}]}`
		q, err := ParseRelayRequest([]byte(raw), "chat", "openai_responses")
		if err != nil {
			t.Fatal(err)
		}
		body, err := q.CodexUpstream()
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err := json.Unmarshal(body, &root); err != nil {
			t.Fatal(err)
		}
		input := root["input"].([]any)
		if role == "system" {
			if root["instructions"] != "first\n\nsecond" || len(input) != 1 || input[0].(map[string]any)["role"] != "user" {
				t.Fatalf("wrong promotion: %s", body)
			}
		} else if len(input) != 2 || input[0].(map[string]any)["role"] != "developer" || root["instructions"] != "" {
			t.Fatalf("developer changed: %s", body)
		}
	}
}

func TestNormalizeCodexInstructionsPreservesHistory(t *testing.T) {
	var root map[string]json.RawMessage
	raw := `{"instructions":"existing","input":[{"role":"system","content":"first"},{"type":"message","role":"system","content":[{"type":"input_text","text":"A"},{"type":"text","text":"B"}]},{"role":"developer","content":"developer"},{"type":"reasoning","encrypted_content":"opaque"},{"type":"function_call","call_id":"call","name":"read","arguments":"{\"n\":9007199254740993}"}],"unknown":{"n":9007199254740993}}`
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatal(err)
	}
	unknown := string(root["unknown"])
	if err := NormalizeCodexInstructions(root); err != nil {
		t.Fatal(err)
	}
	var instructions string
	_ = json.Unmarshal(root["instructions"], &instructions)
	if instructions != "first\n\nAB\n\nexisting" || strings.Contains(string(root["input"]), `"role":"system"`) || !strings.Contains(string(root["input"]), `9007199254740993`) || !strings.Contains(string(root["input"]), `"encrypted_content":"opaque"`) || string(root["unknown"]) != unknown {
		t.Fatalf("lost data: %s %s", root["instructions"], root["input"])
	}
	before := string(root["input"]) + string(root["instructions"])
	if err := NormalizeCodexInstructions(root); err != nil || before != string(root["input"])+string(root["instructions"]) {
		t.Fatalf("not idempotent: %v", err)
	}
}

func TestNormalizeCodexInstructionsRejectsLossyContent(t *testing.T) {
	for _, input := range []string{
		`[{"role":"user","content":"hello"},{"role":"system","content":"late"}]`,
		`[{"role":"system","content":null}]`,
		`[{"role":"system","content":[{"type":"input_image","image_url":"https://example.com/a"}]}]`,
		`[{"role":"system","content":[{"type":"input_text","text":null}]}]`,
		`[{"role":"system","content":"hello","cache_control":{"type":"ephemeral"}}]`,
		`[{"role":"system","content":[{"type":"input_text","text":"hello","unknown":1}]}]`,
	} {
		root := map[string]json.RawMessage{"input": json.RawMessage(input)}
		if err := NormalizeCodexInstructions(root); err == nil {
			t.Fatalf("accepted lossy input: %s", input)
		}
		if string(root["input"]) != input {
			t.Fatal("modified failed request")
		}
	}
}
