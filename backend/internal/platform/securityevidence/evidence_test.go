package securityevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQueueBoundaryRebuildsEvidenceWithoutCustomerText(t *testing.T) {
	items := []Evidence{
		{Source: "prompt", Path: "messages[3].content", ReasonCode: "credential_format", Snippet: "PRIVATE_KEY_VALUE", Trigger: "PRIVATE_PREFIX", Category: "PRIVATE_CATEGORY"},
		{Source: "tool_arguments", Path: "arguments.PRIVATE_CUSTOMER_EMAIL", ReasonCode: "regex_match", Snippet: "PRIVATE_CONTENT"},
		{Source: "model_output", Path: "choices[0].message.content", ReasonCode: "code_marker", Trigger: "<script", Snippet: "PRIVATE_CONTEXT"},
		{Source: "model_output", Path: "body", ReasonCode: "code_marker", Trigger: "PRIVATE_CONTENT"},
		{Source: "prompt", Path: "body", ReasonCode: "PRIVATE_REASON", Snippet: "PRIVATE_CONTENT"},
	}
	got := Normalize(items)
	if len(got) != 3 || got[0].Snippet != "[Credential hidden]" || got[1].Path != "body" || got[1].Visibility != "hidden" || got[1].Snippet != "" || got[2].Snippet != "[omitted] <script [omitted]" {
		t.Fatalf("unexpected normalization: %+v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatalf("customer data crossed boundary: %s", encoded)
	}
	if items[0].Snippet != "PRIVATE_KEY_VALUE" {
		t.Fatal("normalization mutated caller's evidence")
	}
}

func TestEvidenceBoundAndSafePaths(t *testing.T) {
	var items []Evidence
	for i := 0; i < 30; i++ {
		items = append(items, Evidence{Source: "prompt", Path: "body", ReasonCode: "credential_assignment"})
	}
	if len(Normalize(items)) != MaxItems {
		t.Fatal("evidence limit not enforced")
	}
	for _, path := range []string{"messages[3].content[0].text", "arguments.fields[2].command", "stream.choices[0].delta.content"} {
		if SafePath(path) != path {
			t.Fatalf("safe path rejected: %s", path)
		}
	}
	for _, path := range []string{"arguments.customer@example.com", "arguments.PRIVATE_VALUE", strings.Repeat("x", MaxPathBytes+1)} {
		if SafePath(path) != "body" {
			t.Fatalf("unsafe path retained: %s", path)
		}
	}
}
