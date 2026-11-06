package interfaces

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"bosapi/backend/internal/platform/subscription"
)

func withSubscriptionCredential(p provider, credentials subscription.Credentials) provider {
	p.Type, p.APIKey = "oauth", ""
	p.AccessToken, p.RefreshToken, p.AccountID, p.ProjectID, p.ExpiresAt = credentials.AccessToken, credentials.RefreshToken, credentials.AccountID, credentials.ProjectID, credentials.ExpiresAt
	return p
}

func providerHeaders(dst, src http.Header, p provider) {
	adapterFor(p.APIFormat).headers(dst, src, p.APIKey)
	if p.Type != "oauth" {
		return
	}
	dst.Del("x-api-key")
	dst.Del("x-goog-api-key")
	dst.Set("Authorization", "Bearer "+p.AccessToken)
	if p.Platform == "antigravity" {
		dst.Set("User-Agent", subscription.AntigravityUserAgent)
	}
	if p.Platform == "anthropic" {
		// Preserve native CLI/SDK metadata without forwarding client credentials.
		for name, values := range src {
			lower := strings.ToLower(name)
			if lower == "user-agent" || lower == "x-app" || strings.HasPrefix(lower, "x-stainless-") {
				dst[name] = append([]string(nil), values...)
			}
		}
		betas := []string{}
		seen := map[string]bool{}
		for _, value := range append([]string{dst.Get("anthropic-beta")}, "oauth-2025-04-20", "claude-code-20250219") {
			for _, beta := range strings.Split(value, ",") {
				beta = strings.TrimSpace(beta)
				if beta != "" && !seen[beta] {
					betas = append(betas, beta)
					seen[beta] = true
				}
			}
		}
		dst.Set("anthropic-beta", strings.Join(betas, ","))
	}
}

func codeAssistSubscription(p provider) bool {
	return p.Type == "oauth" && (p.Platform == "gemini" || p.Platform == "antigravity")
}

func geminiSubscriptionRequest(p provider, path string, payload []byte) (string, []byte, error) {
	model, action, ok := geminiModelAction(path)
	if !ok || p.ProjectID == "" || action != "generateContent" && action != "streamGenerateContent" && action != "countTokens" {
		return "", nil, errors.New("unsupported Code Assist operation")
	}
	if action == "countTokens" {
		if p.Platform == "antigravity" {
			return "", nil, errors.New("Antigravity token counting is unavailable")
		}
		return "https://generativelanguage.googleapis.com/v1beta/models/" + model + ":countTokens", payload, nil
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(payload, &request) != nil || request == nil {
		return "", nil, errors.New("invalid Code Assist request")
	}
	delete(request, "model")
	envelope := map[string]any{"model": model, "project": p.ProjectID, "request": request}
	if p.Platform == "antigravity" {
		if err := antigravityIdentity(request); err != nil {
			return "", nil, err
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", nil, err
		}
		id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
		envelope["requestId"] = fmt.Sprintf("agent-%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
		envelope["userAgent"], envelope["requestType"] = "antigravity", "agent"
	}
	raw, err := json.Marshal(envelope)
	// Code Assist generation always streams; non-streaming clients are aggregated.
	return "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent", raw, err
}

// Antigravity's agent identity is prepended without discarding the caller's
// system instructions or decoding opaque tool/signature values as floats.
func antigravityIdentity(request map[string]json.RawMessage) error {
	system := map[string]json.RawMessage{}
	if raw := request["systemInstruction"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &system) != nil || system == nil {
			return errors.New("invalid Antigravity system instruction")
		}
	}
	var parts []json.RawMessage
	if raw := system["parts"]; len(raw) > 0 && json.Unmarshal(raw, &parts) != nil {
		return errors.New("invalid Antigravity system parts")
	}
	for _, raw := range parts {
		var part struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &part) == nil && strings.Contains(part.Text, "You are Antigravity") {
			return nil
		}
	}
	identity := json.RawMessage(`{"text":"You are Antigravity, an AI coding assistant."}`)
	system["parts"], _ = json.Marshal(append([]json.RawMessage{identity}, parts...))
	request["systemInstruction"], _ = json.Marshal(system)
	return nil
}

// A pull-based reader retains cancellation and backpressure without a forwarding
// goroutine. Each Code Assist response is unwrapped before usage/security parsing.
type geminiSubscriptionBody struct {
	source    io.ReadCloser
	reader    *bufio.Reader
	pending   []byte
	streaming bool
	done      bool
	aggregate bool
}

func unwrapGemini(raw []byte) ([]byte, error) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return nil, errors.New("invalid Code Assist response")
	}
	if response := doc["response"]; len(response) > 0 && string(response) != "null" {
		return response, nil
	}
	// countTokens returns an unwrapped result on some Code Assist versions.
	if len(doc["totalTokens"]) > 0 || len(doc["error"]) > 0 {
		return raw, nil
	}
	return nil, errors.New("Code Assist response is missing response")
}

func (b *geminiSubscriptionBody) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for len(b.pending) == 0 {
		if b.done {
			return 0, io.EOF
		}
		if !b.streaming {
			if b.aggregate {
				var err error
				b.pending, err = aggregateGeminiSubscription(b.source)
				if err != nil {
					return 0, err
				}
				b.done = true
				continue
			}
			raw, err := io.ReadAll(io.LimitReader(b.source, (32<<20)+1))
			if err != nil {
				return 0, err
			}
			if len(raw) > 32<<20 {
				return 0, errors.New("Code Assist response too large")
			}
			b.pending, err = unwrapGemini(raw)
			if err != nil {
				return 0, err
			}
			b.done = true
		} else {
			var line []byte
			for {
				part, more, err := b.reader.ReadLine()
				if err != nil {
					return 0, err
				}
				line = append(line, part...)
				if len(line) > 8<<20 {
					return 0, errors.New("Code Assist stream frame too large")
				}
				if !more {
					break
				}
			}
			if bytes.HasPrefix(line, []byte("data:")) {
				data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
				if len(data) > 0 && !bytes.Equal(data, []byte("[DONE]")) {
					unwrapped, err := unwrapGemini(data)
					if err != nil {
						return 0, err
					}
					line = append([]byte("data: "), unwrapped...)
				}
			}
			b.pending = append(line, '\n')
		}
	}
	n := copy(dst, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}
func (b *geminiSubscriptionBody) Close() error { return b.source.Close() }

func aggregateGeminiSubscription(source io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	result := map[string]json.RawMessage{}
	type candidate struct {
		fields   map[string]json.RawMessage
		content  map[string]json.RawMessage
		parts    []json.RawMessage
		finished bool
	}
	candidates := map[int]*candidate{}
	order := []int{}
	size := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		size += len(line)
		if size > 32<<20 {
			return nil, errors.New("Code Assist response too large")
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		data, err := unwrapGemini(data)
		if err != nil {
			return nil, err
		}
		var frame map[string]json.RawMessage
		if json.Unmarshal(data, &frame) != nil || len(frame["error"]) > 0 {
			return nil, errors.New("Code Assist generation failed")
		}
		for key, value := range frame {
			if key != "candidates" {
				result[key] = value
			}
		}
		var items []map[string]json.RawMessage
		if raw := frame["candidates"]; len(raw) > 0 && json.Unmarshal(raw, &items) != nil {
			return nil, errors.New("invalid Code Assist candidates")
		}
		for _, item := range items {
			var index int
			if raw := item["index"]; len(raw) > 0 && json.Unmarshal(raw, &index) != nil {
				return nil, errors.New("invalid Code Assist candidate index")
			}
			current := candidates[index]
			if current == nil {
				current = &candidate{fields: map[string]json.RawMessage{}, content: map[string]json.RawMessage{}}
				candidates[index] = current
				order = append(order, index)
			}
			for key, value := range item {
				if key != "content" {
					current.fields[key] = value
				}
			}
			if reason := item["finishReason"]; len(reason) > 0 && string(reason) != `""` {
				current.finished = true
			}
			if raw := item["content"]; len(raw) > 0 {
				var content map[string]json.RawMessage
				if json.Unmarshal(raw, &content) != nil {
					return nil, errors.New("invalid Code Assist content")
				}
				var parts []json.RawMessage
				if raw := content["parts"]; len(raw) > 0 && json.Unmarshal(raw, &parts) != nil {
					return nil, errors.New("invalid Code Assist parts")
				}
				current.parts = append(current.parts, parts...)
				for key, value := range content {
					if key != "parts" {
						current.content[key] = value
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	items := []map[string]json.RawMessage{}
	for _, index := range order {
		current := candidates[index]
		if !current.finished {
			return nil, errors.New("incomplete Code Assist generation")
		}
		current.content["parts"], _ = json.Marshal(current.parts)
		current.fields["content"], _ = json.Marshal(current.content)
		items = append(items, current.fields)
	}
	if len(items) == 0 && len(result["promptFeedback"]) == 0 {
		return nil, errors.New("empty Code Assist generation")
	}
	result["candidates"], _ = json.Marshal(items)
	return json.Marshal(result)
}
