package securityevidence

import (
	"bytes"
	"encoding/json"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CredentialPattern couples a token shape with preconditions proved by its
// grammar. A precondition is a necessary condition, so skipping a candidate
// can only avoid impossible regex work and must never weaken detection.
type CredentialPattern struct {
	Regex          *regexp.Regexp
	RequiresEquals bool
	MinDigits      uint8
}

// CredentialPatterns require a complete token shape, rather than a provider
// prefix. The two service-account shapes fill known gaps without relaxing
// existing matches.
var CredentialPatterns = []CredentialPattern{
	credentialPattern(`(?i)\b(?:pk_(?:live|test)_|whsec_|hf_|r8_|gsk_|pplx-|jina_|ntn_|xapp-)[A-Za-z0-9_-]{16,}\b`),
	credentialPattern(`\bya29\.[A-Za-z0-9_-]{20,}\b`),
	credentialPattern(`(?i)hooks\.slack\.com/services/[A-Za-z0-9/_-]{20,}`),
	credentialPattern(`(?s)-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----.*`),
	credentialPattern(`(?i)\b(?:AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}\b`),
	credentialPattern(`(?i)\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
	credentialPattern(`(?i)\bgl(?:pat|d[a-z]{1,2})-[A-Za-z0-9_-]{16,}\b`),
	credentialPattern(`(?i)\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	credentialPattern(`(?i)\bAIza[A-Za-z0-9_-]{30,}\b`),
	credentialPattern(`(?i)\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`),
	credentialPattern(`(?i)\bsk-(?:proj-|ant-|svcacct-)?[A-Za-z0-9_-]{20,}\b`),
	credentialPattern(`(?i)\b(?:npm_[A-Za-z0-9]{36}|pypi-[A-Za-z0-9_-]{50,})\b`),
	credentialPattern(`\b(?:shpat|shpca|shppa|shpss)_[a-fA-F0-9]{32,}\b`),
	credentialPattern(`\bSG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{32,}\b`),
	credentialPattern(`\bAC[a-fA-F0-9]{32}\b`),
	credentialPattern(`\bdapi[a-fA-F0-9]{32,}\b`),
	credentialPattern(`(?i)\b(?:glpat-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,})\b`),
	credentialPattern(`(?i)\b(?:discord|slack)\.com/api/(?:webhooks|hooks)/[A-Za-z0-9._/-]{20,}`),
	credentialPattern(`(?i)\b(?:mongodb(?:\+srv)?|postgres(?:ql)?|mysql|mariadb|redis|rediss|amqps?)://[^\s:@/]+:[^\s@/]+@`),
	credentialPattern(`(?s)-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`),
	credentialPattern(`(?i)\b(?:x-api-key|authorization)\s*[:=]\s*(?:bearer\s+)?[A-Za-z0-9._~+/-]{16,}={0,2}`),
	// GCP service-account JSON is a provider-issued marker even when fields
	// are formatted without a credential-like value on the same line.
	credentialPattern(`(?s)"type"\s*:\s*"service_account"`),
	credentialPattern(`(?i)\b(?:aws_secret_access_key|secret[_-]?access[_-]?key)\s*["'=:\s]{1,5}\s*[A-Za-z0-9/+=]{40}\b`),
}

// credentialPattern compiles a credential regex once and records only match
// requirements that the grammar itself proves. Uncertain grammar metadata
// remains false or zero so scanning falls back to the full regex.
func credentialPattern(regex string) CredentialPattern {
	pattern := CredentialPattern{Regex: regexp.MustCompile(regex)}
	tree, err := syntax.Parse(regex, syntax.Perl)
	if err != nil {
		return pattern
	}
	pattern.RequiresEquals = regexpTreeRequiresEquals(tree)
	pattern.MinDigits = regexpTreeMinDigits(tree)
	return pattern
}

// regexpTreeRequiresEquals proves literal '=' for every possible match.
// Concatenation needs one mandatory child; alternation needs every branch.
func regexpTreeRequiresEquals(tree *syntax.Regexp) bool {
	if tree == nil {
		return false
	}
	switch tree.Op {
	case syntax.OpLiteral:
		return strings.ContainsRune(string(tree.Rune), '=')
	case syntax.OpCapture, syntax.OpPlus:
		return len(tree.Sub) == 1 && regexpTreeRequiresEquals(tree.Sub[0])
	case syntax.OpRepeat:
		return tree.Min > 0 && len(tree.Sub) == 1 && regexpTreeRequiresEquals(tree.Sub[0])
	case syntax.OpConcat:
		for _, child := range tree.Sub {
			if regexpTreeRequiresEquals(child) {
				return true
			}
		}
	case syntax.OpAlternate:
		if len(tree.Sub) == 0 {
			return false
		}
		for _, child := range tree.Sub {
			if !regexpTreeRequiresEquals(child) {
				return false
			}
		}
		return true
	}
	return false
}

// regexpTreeMinDigits counts only requirements proven to contain ASCII digits.
// Mixed character classes prove nothing; this conservative floor is safe to use
// before regex execution.
func regexpTreeMinDigits(tree *syntax.Regexp) uint8 {
	if tree == nil {
		return 0
	}
	switch tree.Op {
	case syntax.OpLiteral:
		minimum := 0
		for _, r := range tree.Rune {
			if r >= '0' && r <= '9' {
				minimum++
				if minimum > 255 {
					return ^uint8(0)
				}
			}
		}
		return uint8(minimum)
	case syntax.OpCharClass:
		if len(tree.Rune) == 0 || len(tree.Rune)%2 != 0 {
			return 0
		}
		for i := 0; i < len(tree.Rune); i += 2 {
			if tree.Rune[i] != '0' || tree.Rune[i+1] != '9' {
				return 0
			}
		}
		return 1
	case syntax.OpCapture:
		if len(tree.Sub) == 1 {
			return regexpTreeMinDigits(tree.Sub[0])
		}
	case syntax.OpPlus:
		if len(tree.Sub) == 1 {
			return regexpTreeMinDigits(tree.Sub[0])
		}
	case syntax.OpRepeat:
		if tree.Min > 0 && (tree.Max < 0 || tree.Max >= tree.Min) && len(tree.Sub) == 1 {
			child := regexpTreeMinDigits(tree.Sub[0])
			if child == 0 {
				return 0
			}
			if tree.Min > int(^uint8(0))/int(child) {
				return ^uint8(0)
			}
			return child * uint8(tree.Min)
		}
	case syntax.OpConcat:
		var minimum int
		for _, child := range tree.Sub {
			minimum += int(regexpTreeMinDigits(child))
			if minimum > 255 {
				return ^uint8(0)
			}
		}
		return uint8(minimum)
	case syntax.OpAlternate:
		if len(tree.Sub) == 0 {
			return 0
		}
		minimum := 255
		for _, child := range tree.Sub {
			minimum = min(minimum, int(regexpTreeMinDigits(child)))
		}
		return uint8(minimum)
	}
	return 0
}

// CredentialPatternCanMatch reports whether a value satisfies pattern
// preconditions. It is a scanner optimization, never a replacement for regex.
func CredentialPatternCanMatch(pattern CredentialPattern, value []byte) bool {
	if pattern.RequiresEquals && !bytes.ContainsRune(value, '=') {
		return false
	}
	return hasMinimumASCIIDigits(string(value), pattern.MinDigits)
}

// hasMinimumASCIIDigits stops counting once the proven floor is met.
func hasMinimumASCIIDigits(value string, minimum uint8) bool {
	if minimum == 0 {
		return true
	}
	digits := 0
	for i := 0; i < len(value); i++ {
		if value[i] >= '0' && value[i] <= '9' {
			digits++
			if uint8(digits) == minimum {
				return true
			}
		}
	}
	return false
}

var CredentialAssignmentPattern = regexp.MustCompile(`(?i)(\b(?:password|passwd|pwd|secret|token|credential|client[_-]?secret|access[_-]?token|access[_-]?key[_-]?secret|refresh[_-]?token|api[_-]?key|apikey|x-api-key|authorization|secret[_-]?key|auth[_-]?token|private[_-]?key)["']?(?:\s*[:=]\s*|\s+is\s+)(?:bearer\s+)?["']?)([^\s"']{4,})`)
var CredentialFieldName = regexp.MustCompile(`(?i)^(?:password|passwd|pwd|secret|token|credential|client[_-]?secret|access[_-]?token|access[_-]?key[_-]?secret|refresh[_-]?token|api[_-]?key|apikey|x-api-key|authorization|secret[_-]?key|auth[_-]?token|private[_-]?key)$`)

var credentialStatementPrefix = regexp.MustCompile(`(?i)\s+is\s+(?:bearer\s+)?["']?$`)

// These words describe a credential's state rather than disclose its value.
// Explicit assignments and quoted values remain candidates, e.g. a password
// literally set to "required" must still be detected and redacted.
var credentialDescriptions = map[string]bool{
	"required": true, "optional": true, "mandatory": true,
	"secure": true, "insecure": true, "strong": true, "weak": true,
	"correct": true, "incorrect": true, "valid": true, "invalid": true,
	"missing": true, "empty": true, "unknown": true, "important": true,
	"hidden": true, "redacted": true, "encrypted": true, "protected": true,
	"enabled": true, "disabled": true, "expired": true, "unchanged": true,
}

// These require a value made entirely from regexp syntax, with at least one
// explicit regexp operation. A password containing punctuation and literal text
// remains a credential; a quoted regexp-shaped password also remains one.
var credentialRegexpValue = regexp.MustCompile(`(?i)^/?(?:(?:\\[sdwb][*+?]?|\[(?:\[:[a-z]+:\]|[^\]\r\n])+\](?:[*+?]|\{[0-9]+(?:,[0-9]*)?\})?|\.(?:[*+?]|\{[0-9]+(?:,[0-9]*)?\}))|[\^$()|])+(?:/[gimsuy]*)?[,;]?$`)
var credentialRegexpOperation = regexp.MustCompile(`(?i)\\[sdwb]|\[\[:[a-z]+:\]\]|\.(?:[*+?]|\{[0-9]+(?:,[0-9]*)?\})|\](?:[*+?]|\{[0-9]+(?:,[0-9]*)?\})`)
var credentialBareSchema = regexp.MustCompile(`(?i)^\{\s*type\s*:\s*(?:string|object|array|number|integer|boolean|null)\s*\}`)

var credentialPlaceholders = []string{
	"[Password Hide]", "[Credential hidden]", "[凭证已隐藏]",
	"[Match hidden]", "[命中内容已隐藏]",
}

// IsCredentialPlaceholder identifies complete markers emitted by redaction. It
// must not treat a marker followed by a real token suffix as already redacted.
func IsCredentialPlaceholder(value string) bool {
	value = strings.TrimSpace(value)
	for _, placeholder := range credentialPlaceholders {
		if strings.EqualFold(value, placeholder) {
			return true
		}
	}
	return false
}

func startsWithCredentialPlaceholder(value string) bool {
	for _, placeholder := range credentialPlaceholders {
		if len(value) < len(placeholder) || !strings.EqualFold(value[:len(placeholder)], placeholder) {
			continue
		}
		rest := value[len(placeholder):]
		if credentialValueBoundary(rest) {
			return true
		}
	}
	return false
}

func credentialValueBoundary(rest string) bool {
	if rest == "" {
		return true
	}
	char, _ := utf8.DecodeRuneInString(rest)
	return strings.ContainsRune(`"',;)}]`, char) || unicode.IsSpace(char)
}

func startsWithCredentialStructure(value string) bool {
	if value == "" || value[0] != '{' && value[0] != '[' {
		return false
	}
	// Only a complete bare schema is an example. A matching prefix followed
	// by literal secret text must still be detected as a credential value.
	if match := credentialBareSchema.FindStringIndex(value); match != nil && credentialValueBoundary(value[match[1]:]) {
		return true
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return false
	}
	switch decoded.(type) {
	case map[string]any, []any:
		rest := value[decoder.InputOffset():]
		return credentialValueBoundary(rest)
	}
	return false
}

// CredentialAssignments excludes empty assignment chains, already redacted
// markers and unquoted regexp/schema syntax that supplies no credential value.
func CredentialAssignments(text []byte, limit int) [][]int {
	matches := CredentialAssignmentPattern.FindAllSubmatchIndex(text, limit)
	source := string(text)
	out := matches[:0]
	for _, match := range matches {
		value := source[match[4]:match[5]]
		// The regexp stops at whitespace inside e.g. [Password Hide]. Check
		// the complete source value before deciding whether it is a marker.
		tail := source[match[4]:]
		if startsWithCredentialPlaceholder(tail) {
			continue
		}
		if strings.HasSuffix(value, "=") && CredentialFieldName.MatchString(strings.TrimSuffix(value, "=")) {
			continue
		}
		quoted := match[4] > 0 && (text[match[4]-1] == '"' || text[match[4]-1] == '\'')
		if !quoted {
			if credentialStatementPrefix.MatchString(source[match[2]:match[3]]) && credentialDescriptions[strings.ToLower(strings.TrimRight(value, ".,;!?"))] {
				continue
			}
			pattern := strings.Trim(value, "`")
			if credentialRegexpValue.MatchString(pattern) && credentialRegexpOperation.MatchString(pattern) || startsWithCredentialStructure(tail) {
				continue
			}
		}
		out = append(out, match)
	}
	return out
}
