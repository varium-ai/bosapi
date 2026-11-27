package interfaces

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf16"

	"bosapi/backend/internal/platform/logging"
)

// Keep the body attribution and HTTP identity on the same tested CLI version.
const claudeOAuthVersion = "2.1.321"

var claudeBillingVersion = regexp.MustCompile(`cc_version=(\d{1,6}\.\d{1,6}\.\d{1,6})(?:[.; ])`)

func anthropicOAuthMessages(p provider, path string) bool {
	return p.Type == "oauth" && p.Platform == "anthropic" && p.APIFormat == "anthropic_messages" && (path == "messages" || path == "messages/count_tokens")
}

// RawMessage prevents tool arguments, opaque signatures and future fields from
// passing through float64 or a reduced protocol schema.
func prepareAnthropicOAuth(r *http.Request, p provider, payload []byte) ([]byte, bool, error) {
	var body map[string]json.RawMessage
	if json.Unmarshal(payload, &body) != nil || body == nil {
		return nil, false, errors.New("invalid Anthropic OAuth request")
	}
	metadata := map[string]json.RawMessage{}
	if raw := body["metadata"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &metadata) != nil || metadata == nil {
			return nil, false, errors.New("invalid Anthropic OAuth metadata")
		}
	}
	var callerID string
	_ = json.Unmarshal(metadata["user_id"], &callerID)
	var blocks []json.RawMessage
	var system string
	if raw := body["system"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &system) == nil {
			block, _ := json.Marshal(map[string]string{"type": "text", "text": system})
			blocks = append(blocks, block)
		} else if json.Unmarshal(raw, &blocks) != nil {
			return nil, false, errors.New("invalid Anthropic OAuth system instruction")
		}
	}
	billing := false
	for _, raw := range blocks {
		var block struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &block)
		billing = billing || strings.HasPrefix(block.Text, "x-anthropic-billing-header:")
	}
	ua := r.Header.Get("User-Agent")
	native := callerID != "" && (strings.HasPrefix(ua, "claude-cli/") || strings.HasPrefix(ua, "claude-code/") || billing)
	if native {
		return payload, true, nil
	}
	key, _ := r.Context().Value(gatewayAPIKeyRequestKey{}).(apiKey)
	client := sha256.Sum256([]byte(fmt.Sprintf("bos/claude/client/%d/%s/%d", p.ID, p.AccountID, key.ID)))
	// The first user turn stays fixed as a conversation grows. An explicit caller
	// identity further separates sessions without sending it verbatim upstream.
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(body["messages"], &messages)
	var first json.RawMessage
	for _, message := range messages {
		if message.Role == "user" {
			first = message.Content
			break
		}
	}
	seed, _ := json.Marshal([]any{fmt.Sprintf("%x", client), callerID, string(first)})
	session := sha256.Sum256(seed)
	sessionID := claudeUUID(session[:16])
	identity, _ := json.Marshal(map[string]string{"device_id": fmt.Sprintf("%x", client), "account_uuid": p.AccountID, "session_id": sessionID})
	metadata["user_id"], _ = json.Marshal(string(identity))
	body["metadata"], _ = json.Marshal(metadata)
	// Retain caller instructions as system blocks, including cache controls. Do
	// not demote them to fabricated user/assistant turns or change tool history.
	attribution, _ := json.Marshal(map[string]string{"type": "text", "text": "x-anthropic-billing-header: cc_version=" + claudeOAuthVersion + "." + claudeAttributionFingerprint(first) + "; cc_entrypoint=cli;"})
	identityBlock := json.RawMessage(`{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."}`)
	body["system"], _ = json.Marshal(append([]json.RawMessage{attribution, identityBlock}, blocks...))
	out, err := json.Marshal(body)
	return out, false, err
}

func claudeUUID(raw []byte) string {
	var id [16]byte
	copy(id[:], raw)
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}

func applyAnthropicOAuthHeaders(req, incoming *http.Request, p provider, payload []byte, native, streaming bool) error {
	version := claudeOAuthVersion
	ua := req.Header.Get("User-Agent")
	if !native || !(strings.HasPrefix(ua, "claude-cli/") || strings.HasPrefix(ua, "claude-code/")) {
		if native {
			// A proxy may replace the CLI UA. Recover its declared version from
			// the billing block without rewriting the native body/cache prefix.
			var body struct {
				System []struct {
					Text string `json:"text"`
				} `json:"system"`
			}
			_ = json.Unmarshal(payload, &body)
			for _, block := range body.System {
				if strings.HasPrefix(block.Text, "x-anthropic-billing-header:") {
					if match := claudeBillingVersion.FindStringSubmatch(block.Text); len(match) > 1 {
						version = match[1]
					}
				}
			}
		}
		// An arbitrary SDK's version/runtime must not contradict CLI attribution.
		for name := range req.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-stainless-") {
				req.Header.Del(name)
			}
		}
		for name, value := range map[string]string{
			"User-Agent": "claude-cli/" + version + " (external, cli)",
			"X-App":      "cli", "X-Stainless-Lang": "js", "X-Stainless-Package-Version": "0.94.0",
			"X-Stainless-OS": "Linux", "X-Stainless-Arch": "arm64", "X-Stainless-Runtime": "node",
			"X-Stainless-Runtime-Version": "v24.3.0", "X-Stainless-Retry-Count": "0", "X-Stainless-Timeout": "600",
			"Anthropic-Dangerous-Direct-Browser-Access": "true",
		} {
			req.Header.Set(name, value)
		}
	}
	if !native {
		for _, beta := range []string{"interleaved-thinking-2025-05-14", "prompt-caching-scope-2026-01-05", "effort-2025-11-24", "context-management-2025-06-27", "thinking-binding-controls-2026-08-01", "mid-conversation-output-config-2026-07-01", "extended-cache-ttl-2025-04-11"} {
			appendAnthropicBeta(req.Header, beta)
		}
		req.Header.Set("Accept", "application/json")
	}
	if strings.HasSuffix(req.URL.Path, "/count_tokens") {
		appendAnthropicBeta(req.Header, "token-counting-2024-11-01")
	}
	if streaming {
		req.Header.Set("X-Stainless-Helper-Method", "stream")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	req.Header.Set("X-Client-Request-Id", claudeUUID(id[:]))
	mode := "adapted"
	if native {
		mode = "native"
	}
	logging.Printf(incoming.Context(), "INFO", "gateway.anthropic_oauth.compatibility", "provider_id=%d mode=%s compatibility_version=%s beta_query=%t account_identity_present=%t stream=%t", p.ID, mode, claudeOAuthVersion, req.URL.Query().Get("beta") == "true", p.AccountID != "", streaming)
	return nil
}

// CLI billing attribution samples JavaScript UTF-16 code units, not Go bytes or
// Unicode code points. It is a protocol fingerprint, never an authentication key.
func claudeAttributionFingerprint(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) != nil {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(content, &blocks)
		for _, block := range blocks {
			if block.Type == "text" {
				text = block.Text
				break
			}
		}
	}
	units := make([]uint16, 0, 22)
	for _, r := range text {
		units = utf16.AppendRune(units, r)
		if len(units) > 20 {
			break
		}
	}
	sample := []uint16{'0', '0', '0'}
	for i, index := range []int{4, 7, 20} {
		if index < len(units) {
			sample[i] = units[index]
		}
	}
	digest := sha256.Sum256([]byte("59cf53e54c78" + string(utf16.Decode(sample)) + claudeOAuthVersion))
	return fmt.Sprintf("%x", digest)[:3]
}

func appendAnthropicBeta(headers http.Header, beta string) {
	for _, current := range strings.Split(headers.Get("Anthropic-Beta"), ",") {
		if strings.TrimSpace(current) == beta {
			return
		}
	}
	value := headers.Get("Anthropic-Beta")
	if value != "" {
		value += ","
	}
	headers.Set("Anthropic-Beta", value+beta)
}
