package interfaces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/logging"
	"gorm.io/gorm"
)

const protocolDetectionTTL = 24 * time.Hour

type protocolDetection struct {
	APIFormat   string    `json:"api_format"`
	Model       string    `json:"model"`
	Fingerprint string    `json:"fingerprint"`
	CheckedAt   time.Time `json:"checked_at"`
}

type protocolProbe struct {
	done   chan struct{}
	result protocolDetection
}

// Configuration and model scope prevent an observation from changing unrelated routes.
func protocolFingerprint(p provider) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(p.BaseURL, "/") + "\x00" + p.APIFormat + "\x00" + p.APIKey))
	return hex.EncodeToString(sum[:])
}

func detectedProviderProtocol(p provider, model string) string {
	if p.Type != "api_key" || p.APIFormat != "openai_responses" || model == "" {
		return ""
	}
	actual := model
	if mapped := p.ModelMapping[model]; mapped != "" {
		actual = mapped
	}
	raw, _ := json.Marshal(p.Capabilities["protocol_detection"])
	var result protocolDetection
	now := time.Now()
	if json.Unmarshal(raw, &result) != nil || result.Model != actual || result.Fingerprint != protocolFingerprint(p) || result.CheckedAt.After(now) || now.Sub(result.CheckedAt) >= protocolDetectionTTL {
		return ""
	}
	return result.APIFormat
}

func effectiveProviderProtocol(p provider, model string) provider {
	if detectedProviderProtocol(p, model) == "openai_chat" {
		p.APIFormat = "openai_chat"
	}
	return p
}

func responsesInputStringValidation(raw []byte) bool {
	// Require both an input location and a schema validation error. Arbitrary 400s
	// (model access, quota, authentication) never establish protocol support.
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return false
	}
	var matches func(any) bool
	matches = func(value any) bool {
		switch v := value.(type) {
		case map[string]any:
			location, _ := json.Marshal(v["loc"])
			var parts []string
			_ = json.Unmarshal(location, &parts)
			path, _ := v["loc"].(string)
			if len(parts) >= 2 && parts[0] == "body" && parts[1] == "input" {
				path = "body.input"
			}
			message, _ := v["msg"].(string)
			if strings.HasPrefix(path, "body.input") && strings.EqualFold(message, "Input should be a valid string") {
				return true
			}
			// Some relays embed Pydantic's Python representation in error.message.
			text, _ := v["message"].(string)
			if (strings.Contains(text, "'loc': 'body.input'") || strings.Contains(text, "'loc': ('body', 'input', 'str')")) && strings.Contains(text, "'type': 'string_type'") && strings.Contains(text, "Input should be a valid string") {
				return true
			}
			for _, child := range v {
				if matches(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if matches(child) {
					return true
				}
			}
		}
		return false
	}
	return matches(root)
}

// Probe only synthetic text before inference. User history is sent exactly once
// after a usable protocol is established, through the ordinary security pipeline.
func (a *app) detectProviderProtocol(ctx context.Context, p provider, model string) provider {
	if p.Type != "api_key" || p.APIFormat != "openai_responses" || model == "" {
		return p
	}
	switch detectedProviderProtocol(p, model) {
	case "openai_chat":
		p.APIFormat = "openai_chat"
		return p
	case "openai_responses":
		return p
	}
	actual := model
	if mapped := p.ModelMapping[model]; mapped != "" {
		actual = mapped
	}
	cacheKey := protocolFingerprint(p) + "\x00" + actual
	a.protocolProbeMu.Lock()
	if a.protocolProbes == nil {
		a.protocolProbes = make(map[string]*protocolProbe)
	}
	probe := a.protocolProbes[cacheKey]
	if probe != nil {
		select {
		case <-probe.done:
			ttl := protocolDetectionTTL
			if probe.result.APIFormat == "" {
				ttl = time.Minute
			}
			if time.Since(probe.result.CheckedAt) >= ttl {
				delete(a.protocolProbes, cacheKey)
				probe = nil
			}
		default:
		}
	}
	if probe == nil {
		// Bound observations retained for models that are no longer used.
		if len(a.protocolProbes) >= 256 {
			for key, entry := range a.protocolProbes {
				select {
				case <-entry.done:
					delete(a.protocolProbes, key)
				default:
				}
			}
		}
		probe = &protocolProbe{done: make(chan struct{})}
		a.protocolProbes[cacheKey] = probe
		a.protocolProbeMu.Unlock()
		result := a.probeProviderProtocol(ctx, p, actual)
		a.protocolProbeMu.Lock()
		probe.result = result
		close(probe.done)
		// Failed probes retry after a short cooldown, successful observations expire.
		a.protocolProbeMu.Unlock()
	} else {
		a.protocolProbeMu.Unlock()
		select {
		case <-ctx.Done():
			return p
		case <-probe.done:
		}
	}
	a.protocolProbeMu.Lock()
	result := probe.result
	ttl := protocolDetectionTTL
	if result.APIFormat == "" {
		ttl = time.Minute
	}
	if time.Since(result.CheckedAt) >= ttl {
		delete(a.protocolProbes, cacheKey)
	}
	a.protocolProbeMu.Unlock()
	if result.APIFormat == "openai_chat" && time.Since(result.CheckedAt) < ttl {
		p.APIFormat = "openai_chat"
	}
	return p
}

func (a *app) probeProviderProtocol(ctx context.Context, p provider, model string) protocolDetection {
	result := protocolDetection{Model: model, Fingerprint: protocolFingerprint(p), CheckedAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	responses, _ := json.Marshal(map[string]any{"model": model, "stream": false, "max_output_tokens": 16, "input": []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Reply OK."}}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "OK"}}},
		map[string]any{"role": "user", "content": "Reply OK again."},
	}})
	status, raw := a.protocolProbeRequest(ctx, p, "responses", responses)
	if status == 200 {
		var body struct {
			Object string            `json:"object"`
			Output []json.RawMessage `json:"output"`
		}
		if json.Unmarshal(raw, &body) == nil && body.Object == "response" && len(body.Output) > 0 {
			result.APIFormat = "openai_responses"
		}
	} else if (status == 400 || status == 422) && responsesInputStringValidation(raw) {
		chat := p
		chat.APIFormat = "openai_chat"
		payload, _ := json.Marshal(map[string]any{"model": model, "stream": false, "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "Reply OK."}}})
		status, raw = a.protocolProbeRequest(ctx, chat, "chat/completions", payload)
		if status == 200 {
			if reply, err := gatewayinfra.ParseRelayReply(raw, "openai_chat", "probe"); err == nil && len(reply.Blocks) > 0 {
				result.APIFormat = "openai_chat"
			}
		}
	}
	if result.APIFormat != "" {
		a.persistProtocolDetection(ctx, p, result)
		logging.Printf(ctx, "INFO", "gateway.provider.protocol_detected", "provider_id=%d detected_api_format=%s", p.ID, result.APIFormat)
	}
	return result
}

func (a *app) protocolProbeRequest(ctx context.Context, p provider, resource string, payload []byte) (int, []byte) {
	// Endpoint-only URLs must be reduced to their shared base before probing a sibling.
	if parsed, err := url.Parse(p.BaseURL); err == nil {
		parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/responses")
		parsed.RawPath = ""
		p.BaseURL = parsed.String()
	}
	endpoint, err := providerEndpoint(p, resource)
	if err != nil {
		return 0, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return 0, nil
	}
	providerHeaders(request.Header, nil, p)
	request.Header.Set("Content-Type", "application/json")
	client := *a.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return 0, nil
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return 0, nil
	}
	return response.StatusCode, raw
}

func (a *app) persistProtocolDetection(ctx context.Context, p provider, result protocolDetection) {
	raw, _ := json.Marshal(result)
	if a.database != nil {
		// Merge one JSON field atomically. A concurrent settings or credential edit
		// must invalidate the observation rather than overwrite newer configuration.
		query := a.database.WithContext(ctx).Table("accounts").Where("id = ? AND TRIM(TRAILING '/' FROM base_url) = ? AND api_format = ?", p.ID, strings.TrimRight(p.BaseURL, "/"), p.APIFormat)
		if !p.ConfigurationVersion.IsZero() {
			query = query.Where("updated_at = ?", p.ConfigurationVersion)
		}
		if err := query.UpdateColumn("capabilities_json", gorm.Expr("JSON_SET(COALESCE(capabilities_json, JSON_OBJECT()), '$.protocol_detection', CAST(? AS JSON))", string(raw))).Error; err != nil {
			logging.Printf(ctx, "WARN", "gateway.provider.protocol_detection_persist_failed", "provider_id=%d", p.ID)
		}
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.data.Providers {
		stored := &a.data.Providers[i]
		if stored.ID != p.ID || protocolFingerprint(*stored) != result.Fingerprint {
			continue
		}
		capabilities := make(map[string]any, len(stored.Capabilities)+1)
		for key, value := range stored.Capabilities {
			capabilities[key] = value
		}
		capabilities["protocol_detection"] = result
		stored.Capabilities = capabilities
		if err := a.save(); err != nil {
			logging.Printf(ctx, "WARN", "gateway.provider.protocol_detection_persist_failed", "provider_id=%d", p.ID)
		}
		return
	}
}
