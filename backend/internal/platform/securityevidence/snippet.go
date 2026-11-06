package securityevidence

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ContextRunes      = 32
	MaxSnippetRunes   = 160 // Fits the existing varchar(160) evidence column.
	ContextVisibility = "masked_context"
	CredentialHidden  = "[Credential hidden]"
	MatchHidden       = "[Match hidden]"
)

// Also remove common identifying information and unlabelled long tokens from
// neighbouring text. Redaction happens on the full field BEFORE windowing, so
// cutting through a neighbouring secret cannot expose its prefix or suffix.
var contextPrivatePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9.-]+\.[a-z]{2,}`),
	regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	regexp.MustCompile(`\b(?:\+?\d[\d ()-]{8,}\d)\b`),
	regexp.MustCompile(`[A-Za-z0-9_./+~-]{24,}={0,2}`),
}

type redactionSpan struct{ start, end int }

func privateSpans(text string) []redactionSpan {
	var spans []redactionSpan
	for _, match := range CredentialAssignments([]byte(text), -1) {
		spans = append(spans, redactionSpan{match[4], match[5]})
	}
	for _, pattern := range CredentialPatterns {
		if !CredentialPatternCanMatch(pattern, []byte(text)) {
			continue
		}
		for _, match := range pattern.Regex.FindAllStringIndex(text, -1) {
			spans = append(spans, redactionSpan{match[0], match[1]})
		}
	}
	for _, pattern := range contextPrivatePatterns {
		for _, match := range pattern.FindAllStringIndex(text, -1) {
			spans = append(spans, redactionSpan{match[0], match[1]})
		}
	}
	return spans
}

func mergeSpans(spans []redactionSpan) []redactionSpan {
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := make([]redactionSpan, 0, len(spans))
	for _, span := range spans {
		if len(merged) > 0 && span.start < merged[len(merged)-1].end {
			merged[len(merged)-1].end = max(merged[len(merged)-1].end, span.end)
		} else {
			merged = append(merged, span)
		}
	}
	return merged
}

// RedactedSnippet retains a short window around a match, with the entire match
// value and all other recognised secrets removed. Only this result is queued.
func RedactedSnippet(text string, start, end int, reason string) string {
	if start < 0 || end <= start || end > len(text) {
		return ""
	}
	if reason == "credential_assignment" {
		if match := CredentialAssignmentPattern.FindStringSubmatchIndex(text[start:end]); match != nil {
			start += match[4]
		}
	}
	marker := CredentialHidden
	if reason == "regex_match" {
		marker = MatchHidden
	}
	spans := mergeSpans(append(privateSpans(text), redactionSpan{start, end}))
	var masked strings.Builder
	var markers []redactionSpan // Rune ranges; placeholders must stay intact.
	last, focusStart, focusEnd := 0, 0, 0
	runeCount := 0
	for _, span := range spans {
		plain := text[last:span.start]
		masked.WriteString(plain)
		runeCount += utf8.RuneCountInString(plain)
		markerStart := runeCount
		if span.start <= start && span.end >= end {
			focusStart = runeCount
			masked.WriteString(marker)
			runeCount += utf8.RuneCountInString(marker)
			focusEnd = runeCount
		} else {
			masked.WriteString(CredentialHidden)
			runeCount += utf8.RuneCountInString(CredentialHidden)
		}
		markers = append(markers, redactionSpan{markerStart, runeCount})
		last = span.end
	}
	masked.WriteString(text[last:])
	runes := []rune(masked.String())
	left, right := max(0, focusStart-ContextRunes), min(len(runes), focusEnd+ContextRunes)
	for _, span := range markers {
		if left > span.start && left < span.end {
			left = span.end
		}
		if right > span.start && right < span.end {
			right = span.start
		}
	}
	snippet := string(runes[left:right])
	if left > 0 {
		snippet = "…" + snippet
	}
	if right < len(runes) {
		snippet += "…"
	}
	return cleanSnippet(snippet)
}

func cleanSnippet(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		// Strip bidi controls that could disguise the visible explanation.
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, text)
}

// normalizeContext defensively redacts stored/context snippets on every boundary.
// Legacy snippets without the explicit context visibility are never accepted.
func normalizeContext(text string) string {
	if len([]rune(text)) > MaxSnippetRunes || (!strings.Contains(text, CredentialHidden) && !strings.Contains(text, MatchHidden)) {
		return ""
	}
	// Protect fixed placeholders while scanning assignments such as password=[…].
	parts := strings.NewReplacer(CredentialHidden, "\x00", MatchHidden, "\x01").Replace(text)
	spans := mergeSpans(privateSpans(parts))
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		parts = parts[:span.start] + CredentialHidden + parts[span.end:]
	}
	parts = strings.NewReplacer("\x00", CredentialHidden, "\x01", MatchHidden).Replace(parts)
	parts = cleanSnippet(parts)
	if len([]rune(parts)) > MaxSnippetRunes {
		return ""
	}
	return parts
}
