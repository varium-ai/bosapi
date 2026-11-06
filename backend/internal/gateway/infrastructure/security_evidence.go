package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"bosapi/backend/internal/gateway/domain"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/securitycontract"
	"bosapi/backend/internal/platform/securityevidence"
)

// The decoded inspection document retains a span for every JSON field. Matching
// across fields remains supported, without losing the origin of each match.
type inspectionField struct {
	start, end     int
	path, source   string
	kind           string
	hiddenLocation bool
	credentialName string
}

type inspectionDocument struct {
	text   []byte
	fields []inspectionField
}

// fieldKind maps the fine-grained inspection source to the three coarse kinds
// used by the deterministic scanner. Tool names and results are untrusted tool
// content for scanning purposes, while evidence keeps its original source.
func fieldKind(source string) string {
	switch source {
	case "prompt":
		return "prompt"
	case "model_output":
		return "model_output"
	case "tool_arguments", "tool_result", "tool_name":
		return "tool_arguments"
	default:
		return ""
	}
}

// fieldKindSupports reports whether a field is eligible for the scanner of
// the given kind. This is metadata only for now; later milestones use it to
// assign the immutable injection signatures to request, response, and tool
// content without weakening the existing evidence source.
func fieldKindSupports(kind, fieldKind string) bool {
	return kind == fieldKind
}

func (doc *inspectionDocument) appendField(text, path, source string) {
	start := len(doc.text)
	doc.text = append(doc.text, text...)
	safePath := securityevidence.SafePath(path)
	doc.fields = append(doc.fields, inspectionField{start: start, end: len(doc.text), path: safePath, source: source, kind: fieldKind(source), hiddenLocation: safePath != path})
}

func childPath(path, part string) string {
	if path == "" {
		return part
	}
	return path + "." + part
}

func (doc *inspectionDocument) appendJSON(value any, path, source string, depth int) {
	if depth > 64 {
		// Preserve detection coverage while withholding overly deep locations.
		doc.appendField(string(toolJSON(value)), "body", source)
		return
	}
	switch value := value.(type) {
	case string:
		// OpenAI carries function arguments as a JSON-encoded string.
		if strings.HasSuffix(path, "arguments") {
			var decoded any
			if json.Unmarshal([]byte(value), &decoded) == nil {
				doc.appendJSON(decoded, path, "tool_arguments", depth+1)
				return
			}
		}
		doc.appendField(value, path, source)
	case []any:
		for index, item := range value {
			doc.appendJSON(item, path+"["+strconv.Itoa(index)+"]", source, depth+1)
			doc.text = append(doc.text, '\n')
		}
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			fieldPath := childPath(path, securityevidence.PathField(key, index))
			fieldSource := source
			if key == "arguments" {
				fieldSource = "tool_arguments"
			}
			doc.appendField(key, fieldPath+".key", fieldSource)
			doc.text = append(doc.text, '=')
			firstValue := len(doc.fields)
			doc.appendJSON(value[key], fieldPath, fieldSource, depth+1)
			if _, ok := value[key].(string); ok && credentialFieldName.MatchString(key) && len(doc.fields) == firstValue+1 {
				doc.fields[firstValue].credentialName = strings.ToLower(key)
			}
			doc.text = append(doc.text, '\n')
		}
	default:
		doc.appendField(string(toolJSON(value)), path, source)
	}
}

// Protocol schemas describe available tools/output formats, rather than messages.
// Inspection and redaction must agree on this boundary to preserve definitions.
func isPromptSchemaField(key string, value any) bool {
	switch key {
	case "tools", "functions", "tool_choice", "response_format":
		return true
	case "text":
		_, object := value.(map[string]any)
		return object // Responses output-format schema; string text is input.
	}
	return false
}

func contentInspectionDocument(kind string, payload []byte) inspectionDocument {
	doc := inspectionDocument{}
	source, root := "model_output", "output"
	if kind == "prompt" {
		source, root = "prompt", "body"
	}
	if kind == "model_output_stream" {
		root = "stream"
	}
	var value any
	if json.Unmarshal(payload, &value) == nil {
		if kind == "prompt" {
			mapCurrentPrompt(value, func(selected any, path string) (any, bool) {
				doc.appendJSON(selected, path, source, 0)
				return selected, false
			})
			return doc
		}
		path := ""
		if _, ok := value.(map[string]any); !ok {
			path = root
		}
		doc.appendJSON(value, path, source, 0)
		return doc
	}
	// Decode complete SSE data frames; never mistake window-relative locations
	// for absolute positions in the entire model response.
	if bytes.Contains(payload, []byte("data:")) {
		frames := 0
		for _, line := range bytes.Split(payload, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			if json.Unmarshal(bytes.TrimSpace(line[5:]), &value) == nil {
				doc.appendJSON(value, "stream["+strconv.Itoa(frames)+"]", source, 0)
				doc.text = append(doc.text, '\n')
				frames++
			}
		}
		if frames > 0 {
			return doc
		}
	}
	doc.appendField(string(payload), root, source)
	return doc
}

func toolInspectionDocument(call domain.ToolCall) inspectionDocument {
	doc := inspectionDocument{}
	doc.appendField(call.Name, "tool.name", "tool_name")
	for index, payload := range []json.RawMessage{call.Arguments, call.Result} {
		if len(payload) == 0 {
			continue
		}
		doc.text = append(doc.text, '\n')
		path, source := "arguments", "tool_arguments"
		if index == 1 {
			path, source = "result", "tool_result"
		}
		var value any
		if json.Unmarshal(payload, &value) == nil {
			doc.appendJSON(value, path, source, 0)
		} else {
			doc.appendField(string(payload), path, source)
		}
	}
	return doc
}

func textPosition(text []byte, offset int) (line, column int) {
	offset = min(max(offset, 0), len(text))
	line = bytes.Count(text[:offset], []byte("\n")) + 1
	lastLine := bytes.LastIndexByte(text[:offset], '\n') + 1
	column = utf8.RuneCount(text[lastLine:offset]) + 1
	return
}

func (doc inspectionDocument) evidence(start, end int, reason, trigger string) securityevidence.Evidence {
	item := securityevidence.Evidence{ReasonCode: reason, Trigger: trigger}
	// Prefer a value over its key when a match covers an assignment.
	var best *inspectionField
	for index := range doc.fields {
		field := &doc.fields[index]
		if start == end && start >= field.start && start <= field.end {
			best = field
			break
		}
		if start >= field.end || end <= field.start {
			continue
		}
		if best == nil || strings.HasSuffix(best.path, ".key") && !strings.HasSuffix(field.path, ".key") {
			best = field
		}
		if start >= field.start && end <= field.end {
			best = field
			break
		}
	}
	if best == nil {
		return item
	}
	item.Source, item.Path = best.source, best.path
	if best.hiddenLocation || strings.HasSuffix(best.path, ".key") || reason == "regex_match" && (start < best.start || end > best.end) {
		// A regular expression spanning multiple fields has a structural
		// location only; do not invent character coordinates for it.
		return item
	}
	value := doc.text[best.start:best.end]
	item.StartLine, item.StartColumn = textPosition(value, max(start-best.start, 0))
	item.EndLine, item.EndColumn = textPosition(value, min(end-best.start, len(value)))
	if reason != "code_marker" && start >= best.start && end <= best.end && start < end {
		if best.credentialName != "" {
			// A custom regex may match only a few characters of a password.
			// The rest of that credential field must still be fully hidden.
			item.Snippet = best.credentialName + "=" + securityevidence.CredentialHidden
		} else {
			item.Snippet = securityevidence.RedactedSnippet(string(value), start-best.start, end-best.start, reason)
		}
		item.Visibility = securityevidence.ContextVisibility
	}
	return item
}

type contentHit struct {
	start, end, priority int
	reason, trigger      string
}

// injectionScanTimeout bounds the synchronous deterministic scanner so hostile
// payloads cannot hold the request path while regex and decode work run.
const injectionScanTimeout = 50 * time.Millisecond

func inspectDocument(kind string, doc inspectionDocument) domain.SecurityInspectionResult {
	ctx, cancel := context.WithTimeout(context.Background(), injectionScanTimeout)
	defer cancel()
	return inspectDocumentWithAdvanced(ctx, kind, doc, securitycontract.NoopAdvancedDetector{})
}

func inspectDocumentWithDeadline(ctx context.Context, kind string, doc inspectionDocument) domain.SecurityInspectionResult {
	return inspectDocumentWithAdvanced(ctx, kind, doc, securitycontract.NoopAdvancedDetector{})
}

// inspectDocumentWithAdvanced runs the open-source core checks and merges
// optional proprietary findings through the public detector contract.
func inspectDocumentWithAdvanced(ctx context.Context, kind string, doc inspectionDocument, advanced securitycontract.AdvancedDetector) domain.SecurityInspectionResult {
	hits := make([]contentHit, 0)
	advancedSignals := []string{}
	decodedFragment := false
	if advanced != nil && len(doc.fields) > 0 {
		request := securitycontract.AdvancedInspectRequest{Kind: kind, Fields: make([]securitycontract.AdvancedInspectField, 0, len(doc.fields))}
		for _, field := range doc.fields {
			request.Fields = append(request.Fields, securitycontract.AdvancedInspectField{
				Path:   field.path,
				Source: field.source,
				Kind:   field.kind,
				Value:  doc.text[field.start:field.end],
			})
		}
		result, err := advanced.Inspect(ctx, request)
		if err != nil {
			logging.Printf(ctx, "ERROR", "gateway.advanced_security.inspection_failed", "kind=%s err=%v", kind, err)
		} else {
			decodedFragment = result.Decoded
			advancedSignals = append(advancedSignals, result.Signals...)
			for _, hit := range result.Hits {
				if hit.FieldIndex < 0 || hit.FieldIndex >= len(doc.fields) {
					continue
				}
				field := doc.fields[hit.FieldIndex]
				fieldLength := field.end - field.start
				if hit.Start < 0 || hit.End <= hit.Start || hit.End > fieldLength {
					continue
				}
				hits = append(hits, contentHit{
					field.start + hit.Start,
					field.start + hit.End,
					1,
					hit.Reason,
					hit.RuleName,
				})
			}
		}
	}
	if ctx.Err() != nil {
		return domain.SecurityInspectionResult{Allowed: true, Action: "allow"}
	}
	if kind == "prompt" {
		// Inspect actual field values independently. Schema keys and separators
		// must not manufacture assignments such as password=type=string.
		for _, field := range doc.fields {
			if strings.HasSuffix(field.path, ".key") {
				continue
			}
			value := doc.text[field.start:field.end]
			if field.credentialName != "" && len(strings.TrimSpace(string(value))) >= 4 && !securityevidence.IsCredentialPlaceholder(string(value)) {
				hits = append(hits, contentHit{field.start, field.end, 0, "credential_assignment", ""})
				continue
			}
			for _, index := range securityevidence.CredentialAssignments(value, 1024) {
				hits = append(hits, contentHit{field.start + index[0], field.start + index[1], 0, "credential_assignment", ""})
			}
			for _, pattern := range promptSecretPatterns {
				if !securityevidence.CredentialPatternCanMatch(pattern, value) {
					continue
				}
				for _, index := range pattern.Regex.FindAllIndex(value, 1024) {
					hits = append(hits, contentHit{field.start + index[0], field.start + index[1], 1, "credential_format", ""})
				}
			}
		}
	}
	if ctx.Err() != nil {
		return domain.SecurityInspectionResult{Allowed: true, Action: "allow"}
	}
	markers, reason := outputCodeMarkers, "code_marker"
	if kind == "prompt" {
		markers = nil
	}

	// ASCII-only folding keeps byte offsets valid for Unicode customer text.
	lower := append([]byte(nil), doc.text...)
	for i, char := range lower {
		if char >= 'A' && char <= 'Z' {
			lower[i] = char + ('a' - 'A')
		}
	}
	for _, marker := range markers {
		needle := []byte(strings.ToLower(marker))
		for offset, count := 0, 0; offset < len(lower) && count < 1024; count++ {
			index := bytes.Index(lower[offset:], needle)
			if index < 0 {
				break
			}
			start := offset + index
			hits = append(hits, contentHit{start, start + len(needle), 2, reason, marker})
			offset = start + len(needle)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].start != hits[j].start {
			return hits[i].start < hits[j].start
		}
		if hits[i].priority != hits[j].priority {
			return hits[i].priority < hits[j].priority
		}
		return hits[i].end > hits[j].end
	})
	result := domain.SecurityInspectionResult{Allowed: true, Action: "allow"}
	lastEnd := -1
	for _, hit := range hits {
		if hit.start < lastEnd {
			continue
		}
		lastEnd = hit.end
		result.HitCount++
		if len(result.Evidence) < securityevidence.MaxItems {
			result.Evidence = append(result.Evidence, doc.evidence(hit.start, hit.end, hit.reason, hit.trigger))
		}
		if result.HitCount == 1024 {
			break
		}
	}
	if result.HitCount > 0 {
		for _, hit := range hits {
			if securityevidence.IsInjectionReason(hit.reason) {
				result.RuleID, result.RuleName, result.Action = hit.reason, hit.trigger, "observe"
				break
			}
		}
		if result.RuleID == "" {
			result.RuleID, result.RuleName, result.Action = "model_output_code", "Model output code marker check", "observe"
			if kind == "prompt" {
				result.RuleID, result.RuleName = "prompt_secret", "Prompt credential check"
			}
		}
		result.Evidence = securityevidence.Normalize(result.Evidence)
		if len(result.Evidence) > 0 {
			result.MaskedMatch = result.Evidence[0].Snippet
		}
	}

	signals := []string{}
	if securityevidence.IsInjectionReason(result.RuleID) {
		signals = domain.AppendTriggerSignal(signals, domain.TriggerObserveHit)
	}
	for _, field := range doc.fields {
		if field.source == "tool_arguments" {
			signals = domain.AppendTriggerSignal(signals, domain.TriggerUntrustedUpstream)
			break
		}
	}
	if decodedFragment {
		signals = domain.AppendTriggerSignal(signals, domain.TriggerEncodedFragment)
	}
	for _, signal := range advancedSignals {
		signals = domain.AppendTriggerSignal(signals, signal)
	}
	result.Signals = signals
	return result
}
