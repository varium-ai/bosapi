// Package securityevidence defines the only content-derived data allowed in
// security event queues, storage and administrative responses.
package securityevidence

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MaxItems        = 8
	MaxPathBytes    = 256
	Retention       = 7 * 24 * time.Hour
	CleanupInterval = time.Hour
)

// Evidence contains a structural location and bounded, redacted context.
// Coordinates are one-based Unicode positions; zero means field-only.
type Evidence struct {
	Source      string `json:"source"`
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
	ReasonCode  string `json:"reason_code"`
	Category    string `json:"category"`
	Trigger     string `json:"trigger,omitempty"`
	Snippet     string `json:"snippet"`
	Visibility  string `json:"visibility"`
}

type View struct {
	Evidence
	Reason string `json:"reason"`
}

type Record struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	EventID   uint64
	Ordinal   int
	Evidence  `gorm:"embedded"`
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (Record) TableName() string { return "security_event_evidence" }

// Field names from customer JSON must never be used in paths unless they are
// known protocol/schema names. Unknown names are represented by fields[index].
var pathNames = map[string]bool{}

func init() {
	for _, name := range strings.Fields("body stream tool name arguments result messages content text choices delta message input output prompt instructions system role type function tool_calls tool_use tool_result function_call function_call_output parameters command code query url headers data password passwd pwd secret token credential client_secret access_token refresh_token api_key apikey x-api-key authorization secret_key private_key fields key value") {
		pathNames[name] = true
	}
}

func PathField(name string, index int) string {
	if pathNames[name] {
		return name
	}
	return "fields[" + strconv.Itoa(index) + "]"
}

var pathPart = regexp.MustCompile(`^([a-z_][a-z_0-9-]*)(?:\[[0-9]{1,10}\])*$`)

func SafePath(path string) string {
	if len(path) == 0 || len(path) > MaxPathBytes {
		return "body"
	}
	for _, part := range strings.Split(path, ".") {
		match := pathPart.FindStringSubmatch(part)
		if len(match) == 0 || !pathNames[match[1]] {
			return "body"
		}
	}
	return path
}

var codeTriggers = map[string]bool{
	"<script": true, "javascript:": true, "data:text/html": true,
	"powershell -enc": true, "invoke-expression": true, "curl | sh": true,
	"wget | sh": true, "rm -rf /": true, "__import__(": true,
	"subprocess.": true, "os.system(": true,
}

// Normalize validates locations and reasons, rebuilds legacy snippets, and
// defensively redacts explicitly marked context on queue and read boundaries.
func Normalize(items []Evidence) []Evidence {
	out := make([]Evidence, 0, min(len(items), MaxItems))
	for _, item := range items {
		if len(out) == MaxItems {
			break
		}
		switch item.Source {
		case "prompt", "model_output", "tool_arguments", "tool_result", "tool_name":
		default:
			continue
		}
		path := SafePath(item.Path)
		if path != item.Path {
			item.StartLine, item.StartColumn, item.EndLine, item.EndColumn = 0, 0, 0, 0
		}
		item.Path = path
		context := ""
		if item.Visibility == ContextVisibility {
			context = normalizeContext(item.Snippet)
		}
		item.Category, item.Snippet, item.Visibility = "credential", "[Credential hidden]", "redacted"
		switch item.ReasonCode {
		case "credential_assignment", "credential_format", "credential_marker":
			item.Trigger = ""
		case "prompt_injection", "system_override", "exfil_directive", "role_override",
			"hidden_instruction", "credential_solicitation", "markdown_link_exfil",
			"markdown_link_value_exfil", "markdown_link_follow_exfil",
			"system_prompt_disclosure", "credential_path_directive", "covert_action",
			"instruction_boundary":
			item.Category, item.Snippet, item.Visibility = "injection", "", "hidden"
		case "code_marker":
			if !codeTriggers[item.Trigger] {
				continue
			}
			item.Category, item.Snippet = "code_marker", "[omitted] "+item.Trigger+" [omitted]"
		case "regex_match":
			item.Category, item.Trigger, item.Snippet, item.Visibility = "configured_pattern", "", "", "hidden"
		default:
			continue
		}
		if context != "" && item.ReasonCode != "code_marker" {
			item.Snippet, item.Visibility = context, ContextVisibility
		}
		if item.StartLine < 1 || item.StartColumn < 1 || item.EndLine < item.StartLine || item.EndColumn < 1 ||
			(item.EndLine == item.StartLine && item.EndColumn < item.StartColumn) {
			item.StartLine, item.StartColumn, item.EndLine, item.EndColumn = 0, 0, 0, 0
		}
		out = append(out, item)
	}
	return out
}

func Explanation(code string) string {
	switch code {
	case "credential_assignment":
		return "Text resembles a credential assignment. The value is hidden; a rule match does not confirm a real credential leak. Check its source and context."
	case "credential_format":
		return "Text matches a known credential format. The value is hidden; nearby redacted context helps explain the match."
	case "credential_marker":
		return "Text contains a credential-related marker. This does not confirm that a valid credential was exposed."
	case "code_marker":
		return "Text contains a code marker and triggered the code inspection rule. This does not establish malicious intent."
	case "regex_match":
		return "Decoded tool content matches the regular expression configured for this rule version. Matched content is hidden; nearby redacted context is shown when available."
	case "prompt_injection", "system_override", "exfil_directive", "role_override",
		"hidden_instruction", "credential_solicitation", "markdown_link_exfil",
		"markdown_link_value_exfil", "markdown_link_follow_exfil",
		"system_prompt_disclosure", "credential_path_directive", "covert_action",
		"instruction_boundary":
		return "Text matched a deterministic injection or exfiltration signature. Matched content is hidden; a rule match does not prove execution or disclosure."
	}
	return ""
}
