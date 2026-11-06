package securityevidence

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestContextMasksNeighbouringSecretsBeforeTruncation(t *testing.T) {
	secret := "sk-proj-" + strings.Repeat("a", 120)
	text := "邮箱 person@example.com password=NEIGHBOUR_SECRET " + secret + " 请使用 api_key=TARGET_SECRET 完成配置 password=ANOTHER_SECRET 13800138000 192.168.1.1"
	start := strings.Index(text, "api_key=")
	end := start + len("api_key=TARGET_SECRET")
	snippet := RedactedSnippet(text, start, end, "credential_assignment")
	if !utf8.ValidString(snippet) || !strings.Contains(snippet, "请使用 api_key=[Credential hidden] 完成配置") || len([]rune(snippet)) > MaxSnippetRunes {
		t.Fatalf("unhelpful or unbounded snippet: %q", snippet)
	}
	for _, private := range []string{"NEIGHBOUR_SECRET", "TARGET_SECRET", "ANOTHER_SECRET", "person@example.com", "13800138000", "192.168.1.1", "sk-proj-", "aaaa"} {
		if strings.Contains(snippet, private) {
			t.Fatalf("private data %q leaked: %q", private, snippet)
		}
	}
}

func TestContextNormalizationIsIdempotentAndRedactsNewSecrets(t *testing.T) {
	for _, reason := range []string{"credential_assignment", "credential_format", "regex_match"} {
		snippet := RedactedSnippet("前文 target 后文 password=NEIGHBOUR_SECRET", len("前文 "), len("前文 target"), reason)
		items := []Evidence{{Source: "prompt", Path: "body", ReasonCode: reason, Snippet: snippet, Visibility: ContextVisibility}}
		first := Normalize(items)
		second := Normalize(first)
		if len(first) != 1 || first[0] != second[0] || first[0].Snippet != snippet || first[0].Visibility != ContextVisibility {
			t.Fatalf("context is not stable: %+v / %+v / %s", first, second, snippet)
		}
	}
	item := Evidence{Source: "prompt", Path: "body", ReasonCode: "credential_format", Snippet: "before [Credential hidden] password=UNMASKED_SECRET after", Visibility: ContextVisibility}
	if got := Normalize([]Evidence{item}); strings.Contains(got[0].Snippet, "UNMASKED_SECRET") {
		t.Fatalf("unmasked persisted secret: %+v", got)
	}
	item.Snippet = strings.Repeat("长", MaxSnippetRunes) + CredentialHidden
	if got := Normalize([]Evidence{item}); got[0].Visibility == ContextVisibility {
		t.Fatal("oversized context accepted")
	}
}

func TestIncompletePrivateKeyAndCustomMatchNeverExposeValues(t *testing.T) {
	text := "private key -----BEGIN PRIVATE KEY-----\nshortbody\nmorebody"
	snippet := RedactedSnippet(text, len("private key "), len(text), "credential_format")
	if strings.Contains(snippet, "shortbody") || strings.Contains(snippet, "morebody") {
		t.Fatalf("key leaked: %s", snippet)
	}
	text = "执行命令 danger 后继续"
	snippet = RedactedSnippet(text, len("执行命令 "), len("执行命令 danger"), "regex_match")
	if snippet != "执行命令 [Match hidden] 后继续" {
		t.Fatalf("custom match: %s", snippet)
	}
}

func TestNaturalLanguageCredentialContextIsRedactedAndStable(t *testing.T) {
	text := "my password is 432432 and api_key is NEIGHBOUR_SECRET"
	matches := CredentialAssignments([]byte(text), -1)
	if len(matches) != 2 {
		t.Fatalf("missing credential statements: %v", matches)
	}
	snippet := RedactedSnippet(text, matches[0][0], matches[0][1], "credential_assignment")
	if !strings.Contains(snippet, "password is "+CredentialHidden) || strings.Contains(snippet, "432432") || strings.Contains(snippet, "NEIGHBOUR_SECRET") {
		t.Fatalf("credential leaked or context was lost: %s", snippet)
	}
	items := Normalize([]Evidence{{Source: "prompt", Path: "messages[0].content", ReasonCode: "credential_assignment", Snippet: snippet, Visibility: ContextVisibility}})
	if len(items) != 1 || items[0].Snippet != snippet || Normalize(items)[0] != items[0] {
		t.Fatalf("context changed across queue/read boundaries: %+v", items)
	}
}
