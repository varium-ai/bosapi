package securityevidence

import (
	"strings"
	"testing"
)

func TestCredentialAssignmentsIgnoreRegexpAndSchemaSyntax(t *testing.T) {
	for _, text := range []string{
		`awk '/redis:/ { print $0; next } /db:|password:[[:space:]]*/ { line=$0; if (line ~ /password:/) { print line } }'`,
		`awk '{ sub(/password:.*$/, "hidden", $0); print $0 }'`,
		`/password:.*$/`,
		`/password:\s*/`,
		`/password:\s*\S+$/`,
		`/password:[^[:space:]]+/`,
		`/password:([a-z0-9]+)$/i`,
		"`password:.*$/`",
		`password:{type:string}`,
		`password: {"type":"string"}`,
		`password: [1234, 5678]`,
		`password: [{"type":"string"}]`,
		`password= api_key=`,
	} {
		t.Run(text, func(t *testing.T) {
			if matches := CredentialAssignments([]byte(text), -1); len(matches) != 0 {
				t.Fatalf("syntax produced credential matches: %v", matches)
			}
		})
	}
}

func TestCredentialAssignmentsKeepLiteralAndSymbolPasswords(t *testing.T) {
	for _, text := range []string{
		`password=hunter2`,
		`password=P@ss!w0rd#2026`,
		`password=P@$$w0rd[abc]{}:*?+/`,
		`password=[special]`,
		`password={type:string}actual-token`,
		`password={tenant:prod}REAL_SECRET`,
		`password={tenant:prod}`,
		`password=[1234]actual-token`,
		`password=.*literal-secret$/`,
		`password=".*$/"`,
		`password='[[:space:]]*/'`,
		`password="{type:string}"`,
		`api_key=PRIVATE_API_KEY_VALUE`,
		`api_key: "sk-proj-abcdefghijklmnopqrstuvwxyz123456"`,
		`authorization: Bearer abcdefghijklmnopqrstuvwxyz123456`,
		`password=[Password Hide]actual-token`,
		`password=[凭证已隐藏]actual-token`,
	} {
		t.Run(text, func(t *testing.T) {
			if matches := CredentialAssignments([]byte(text), -1); len(matches) != 1 {
				t.Fatalf("literal credential lost: %v", matches)
			}
		})
	}
}

func TestCredentialAssignmentsIgnoreCompleteRedactionPlaceholders(t *testing.T) {
	for _, placeholder := range credentialPlaceholders {
		for _, text := range []string{
			"password=" + placeholder,
			"password:" + placeholder + " 后续说明",
			"password=\"" + placeholder + "\";",
		} {
			t.Run(text, func(t *testing.T) {
				if matches := CredentialAssignments([]byte(text), -1); len(matches) != 0 {
					t.Fatalf("redaction marker produced credential matches: %v", matches)
				}
			})
		}
		if !IsCredentialPlaceholder(" \t"+placeholder+"\n") || !IsCredentialPlaceholder(strings.ToUpper(placeholder)) {
			t.Fatalf("complete placeholder not recognised: %s", placeholder)
		}
		if IsCredentialPlaceholder(placeholder+"actual-token") || IsCredentialPlaceholder(placeholder[:len(placeholder)-1]) {
			t.Fatalf("partial placeholder accepted: %s", placeholder)
		}
	}
}

func TestCredentialAssignmentsRetainRealSecretBesideRegexpExample(t *testing.T) {
	text := []byte(`/password:[[:space:]]*/ password=P@ss!w0rd api_key=PRIVATE_API_KEY_VALUE`)
	matches := CredentialAssignments(text, -1)
	if len(matches) != 2 || string(text[matches[0][4]:matches[0][5]]) != "P@ss!w0rd" || string(text[matches[1][4]:matches[1][5]]) != "PRIVATE_API_KEY_VALUE" {
		t.Fatalf("mixed syntax and credentials were not isolated: %v", matches)
	}
}

func TestNaturalLanguageCredentialAssignments(t *testing.T) {
	for _, test := range []struct{ text, value string }{
		{"password is 432432", "432432"},
		{"My PASSWORD IS hunter2", "hunter2"},
		{"my password\tis\tP@ss!w0rd", "P@ss!w0rd"},
		{"the api_key is PRIVATE_API_KEY_VALUE", "PRIVATE_API_KEY_VALUE"},
		{"password is swordfish", "swordfish"},
		{`password is "required"`, "required"},
		{"password=required", "required"},
	} {
		t.Run(test.text, func(t *testing.T) {
			matches := CredentialAssignments([]byte(test.text), -1)
			if len(matches) != 1 || test.text[matches[0][4]:matches[0][5]] != test.value {
				t.Fatalf("credential statement was not recognised: %v", matches)
			}
		})
	}
}

func TestCredentialDescriptionsDoNotBecomeCredentialValues(t *testing.T) {
	for _, text := range []string{
		"a password is required for login", "the password is optional",
		"my password is secure", "the password is encrypted.",
		"your password is incorrect", "password is missing",
		"password is not 432432", "what is a password?",
		"password island", "password is [Password Hide]",
		"password is [凭证已隐藏]", "password is [Credential hidden]",
	} {
		if matches := CredentialAssignments([]byte(text), -1); len(matches) != 0 {
			t.Fatalf("description produced a credential event: %q / %v", text, matches)
		}
	}
}
