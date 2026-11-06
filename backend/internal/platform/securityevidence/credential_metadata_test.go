package securityevidence

import (
	"strings"
	"testing"
)

// TestCredentialPatternMetadataOnlyProvesSafePreconditions verifies grammar
// metadata remains conservative: a false or zero precondition can never skip
// a possible match.
func TestCredentialPatternMetadataOnlyProvesSafePreconditions(t *testing.T) {
	tests := []struct {
		regex          string
		requiresEquals bool
		minDigits      uint8
	}{
		{regex: `password\s*[:=]`, requiresEquals: false, minDigits: 0},
		{regex: `password=`, requiresEquals: true, minDigits: 0},
		{regex: `[A-Z0-9]{4}`, requiresEquals: false, minDigits: 0},
		{regex: `[0-9]{4}`, requiresEquals: false, minDigits: 4},
		{regex: `(?:[0-9]{2}|[0-9]{3})`, requiresEquals: false, minDigits: 2},
		{regex: `(?:[0-9]{2}|[a-z]{2})`, requiresEquals: false, minDigits: 0},
		{regex: strings.Repeat("9", 300), requiresEquals: false, minDigits: 255},
	}
	for _, test := range tests {
		pattern := credentialPattern(test.regex)
		if pattern.RequiresEquals != test.requiresEquals || pattern.MinDigits != test.minDigits {
			t.Fatalf("%s metadata=(%v,%d), want=(%v,%d)", test.regex, pattern.RequiresEquals, pattern.MinDigits, test.requiresEquals, test.minDigits)
		}
	}
}

// TestCredentialPatternCanMatchAppliesPreconditions verifies the prefilter is
// only an optimization in front of the compiled regex.
func TestCredentialPatternCanMatchAppliesPreconditions(t *testing.T) {
	requiresEquals := credentialPattern(`password=`)
	if CredentialPatternCanMatch(requiresEquals, []byte("password: value")) {
		t.Fatal("pattern without equals was not filtered")
	}
	if !CredentialPatternCanMatch(requiresEquals, []byte("password=value")) {
		t.Fatal("eligible pattern was filtered")
	}
	digits := credentialPattern(`[0-9]{3}`)
	if CredentialPatternCanMatch(digits, []byte("ab1")) {
		t.Fatal("short digit run was not filtered")
	}
	if !CredentialPatternCanMatch(digits, []byte("ab123")) {
		t.Fatal("eligible digit run was filtered")
	}
}
