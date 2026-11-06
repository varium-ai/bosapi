package totp

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPGenerateAndValidate(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret failed: %v", err)
	}
	if len(secret) < 26 {
		t.Fatalf("Secret too short: %s", secret)
	}

	now := time.Now().UTC()
	code, err := GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("GenerateCode failed: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("Expected 6-digit code, got %s", code)
	}

	// Should validate at current time
	if !Validate(code, secret, now, 1) {
		t.Fatalf("Validate failed for current time")
	}

	// Should validate within 30s drift (+1 step)
	if !Validate(code, secret, now.Add(25*time.Second), 1) {
		t.Fatalf("Validate failed for +25s drift")
	}

	// Should validate within -30s drift (-1 step)
	if !Validate(code, secret, now.Add(-25*time.Second), 1) {
		t.Fatalf("Validate failed for -25s drift")
	}

	// Should fail for large drift (e.g. 90s)
	if Validate(code, secret, now.Add(90*time.Second), 1) {
		t.Fatalf("Validate succeeded for +90s drift, expected failure")
	}

	// Should fail for wrong code
	wrongCode := "000000"
	if code == "000000" {
		wrongCode = "999999"
	}
	if Validate(wrongCode, secret, now, 1) {
		t.Fatalf("Validate succeeded for incorrect code")
	}
}

func TestTOTPUri(t *testing.T) {
	uri := BuildURI("BOS API", "admin@bosapi.local", "JBSWY3DPEHPK3PXP")
	if !strings.HasPrefix(uri, "otpauth://totp/BOS%20API:admin@bosapi.local?") {
		t.Fatalf("Unexpected URI prefix: %s", uri)
	}
	if !strings.Contains(uri, "secret=JBSWY3DPEHPK3PXP") {
		t.Fatalf("URI missing secret: %s", uri)
	}
}

func TestRFC6238Vectors(t *testing.T) {
	// RFC 6238 specifies test secret "12345678901234567890" (20 bytes ASCII)
	// In Base32: GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // gitleaks:allow -- Synthetic test credential.

	// RFC 6238 test vectors for SHA1, 6 digits:
	// T=59 (Unix time 59) -> 287082
	code59, err := GenerateCode(secret, time.Unix(59, 0))
	if err != nil || code59 != "287082" {
		t.Fatalf("RFC 6238 test T=59 expected 287082, got %s (err: %v)", code59, err)
	}

	// T=1111111109 -> 081804
	code1111111109, err := GenerateCode(secret, time.Unix(1111111109, 0))
	if err != nil || code1111111109 != "081804" {
		t.Fatalf("RFC 6238 test T=1111111109 expected 081804, got %s (err: %v)", code1111111109, err)
	}

	// T=1111111111 -> 050471
	code1111111111, err := GenerateCode(secret, time.Unix(1111111111, 0))
	if err != nil || code1111111111 != "050471" {
		t.Fatalf("RFC 6238 test T=1111111111 expected 050471, got %s (err: %v)", code1111111111, err)
	}

	// T=1234567890 -> 005924
	code1234567890, err := GenerateCode(secret, time.Unix(1234567890, 0))
	if err != nil || code1234567890 != "005924" {
		t.Fatalf("RFC 6238 test T=1234567890 expected 005924, got %s (err: %v)", code1234567890, err)
	}

	// T=2000000000 -> 279037
	code2000000000, err := GenerateCode(secret, time.Unix(2000000000, 0))
	if err != nil || code2000000000 != "279037" {
		t.Fatalf("RFC 6238 test T=2000000000 expected 279037, got %s (err: %v)", code2000000000, err)
	}
}
