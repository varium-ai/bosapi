package infrastructure

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodexChatImageDetailPreservesImagesAndOrder(t *testing.T) {
	for _, url := range []string{"https://example.com/a.png", "data:image/png;base64,aGVsbG8="} {
		for _, detail := range []string{"", "auto", "low", "high"} {
			t.Run(url+"/"+detail, func(t *testing.T) {
				field := ""
				if detail != "" {
					field = fmt.Sprintf(`,"detail":%q`, detail)
				}
				raw := fmt.Sprintf(`{"model":"x","messages":[{"role":"system","content":"Describe the images."},{"role":"user","content":[{"type":"text","text":"first"},{"type":"image_url","image_url":{"url":"https://example.com/first.png"}},{"type":"text","text":"second"},{"type":"image_url","image_url":{"url":%q%s}}]}]}`, url, field)
				q, err := ParseRelayRequest([]byte(raw), "chat", "openai_responses")
				if err != nil {
					t.Fatal(err)
				}
				payload, err := q.CodexUpstream()
				if err != nil {
					t.Fatal(err)
				}
				root, err := DecodeProtocolJSON(payload)
				if err != nil {
					t.Fatal(err)
				}
				input := root["input"].([]any)
				if len(input) != 1 || root["instructions"] != "Describe the images." {
					t.Fatalf("unexpected instructions/input: %s", payload)
				}
				parts := input[0].(map[string]any)["content"].([]any)
				if len(parts) != 4 || parts[0].(map[string]any)["text"] != "first" || parts[2].(map[string]any)["text"] != "second" {
					t.Fatalf("lost content order: %s", payload)
				}
				first, second := parts[1].(map[string]any), parts[3].(map[string]any)
				if first["image_url"] != "https://example.com/first.png" || first["detail"] != nil || second["type"] != "input_image" || second["image_url"] != url {
					t.Fatalf("lost image data: %s", payload)
				}
				if got, exists := second["detail"]; exists != (detail != "") || exists && got != detail {
					t.Fatalf("lost image detail: %s", payload)
				}
			})
		}
	}
}

func TestCodexChatRejectsInvalidImageDetail(t *testing.T) {
	for _, detail := range []string{`false`, `null`, `1`, `""`, `"unknown"`, `[]`, `{}`} {
		t.Run(detail, func(t *testing.T) {
			raw := fmt.Sprintf(`{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":%s}}]}]}`, detail)
			_, err := ParseRelayRequest([]byte(raw), "chat", "openai_responses")
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Field != "messages[0].content[0].image_url.detail" || failure.Reason != "expected auto, low, or high" {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
