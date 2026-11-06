package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/usagetime"
)

type providerInput struct {
	Name        string         `json:"name"`
	Platform    string         `json:"platform"`
	Type        string         `json:"type"`
	BaseURL     string         `json:"base_url"`
	APIKey      string         `json:"api_key"`
	Status      string         `json:"status"`
	Models      []string       `json:"models"`
	Pricing     map[string]any `json:"pricing"`
	Concurrency int            `json:"concurrency"`
}

func (p providerInput) validate(requireKey bool) (string, error) {
	if p.Name == "" || len(p.Name) > 100 || p.Platform != "openai" || p.Type != "api_key" || (p.Status != "active" && p.Status != "disabled") || (requireKey && p.APIKey == "") || len(p.APIKey) > 512 {
		return "", errors.New("invalid provider")
	}
	raw := p.BaseURL
	if raw == "" {
		raw = "https://api.openai.com/v1"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" || strings.Contains(u.Path, "..") || strings.Contains(u.Path, "%") {
		return "", errors.New("invalid provider url")
	}
	host := strings.ToLower(u.Hostname())
	allowed := host == "api.openai.com"
	for _, name := range strings.Split(os.Getenv("BOS_ALLOWED_UPSTREAM_HOSTS"), ",") {
		if strings.EqualFold(strings.TrimSpace(name), host) {
			allowed = true
		}
	}
	if !allowed {
		return "", errors.New("provider host not allowed")
	}
	return "https://" + host + strings.TrimRight(u.Path, "/"), nil
}

func (a *app) createProvider(w http.ResponseWriter, r *http.Request) {
	var input providerInput
	if body(r, &input) != nil {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	base, err := input.validate(true)
	if err != nil {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := provider{ID: nextProviderID(a.data.Providers), Name: input.Name, Platform: "openai", Type: "api_key", Status: input.Status, BaseURL: base, APIKey: input.APIKey, Models: input.Models, Pricing: input.Pricing}
	a.data.Providers = append(a.data.Providers, p)
	if a.save() != nil {
		a.data.Providers = a.data.Providers[:len(a.data.Providers)-1]
		fail(w, 500, errorcode.StorageFailed)
		return
	}
	respond(w, 201, map[string]any{"data": map[string]int{"id": p.ID}})
}

func (a *app) updateProvider(w http.ResponseWriter, r *http.Request, path string) {
	id, err := strconv.Atoi(path)
	if err != nil || id <= 0 {
		fail(w, 404, errorcode.NotFound)
		return
	}
	var input providerInput
	if body(r, &input) != nil {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	base, err := input.validate(false)
	if err != nil {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, previous := range a.data.Providers {
		if previous.ID != id || previous.Type != "api_key" {
			continue
		}
		a.data.Providers[i].Name = input.Name
		a.data.Providers[i].Status = input.Status
		a.data.Providers[i].BaseURL = base
		a.data.Providers[i].Models = input.Models
		a.data.Providers[i].Pricing = input.Pricing
		if input.APIKey != "" {
			a.data.Providers[i].APIKey = input.APIKey
		}
		if a.save() != nil {
			a.data.Providers[i] = previous
			fail(w, 500, errorcode.StorageFailed)
			return
		}
		respond(w, 200, map[string]any{"data": map[string]int{"id": id}})
		return
	}
	fail(w, 404, errorcode.NotFound)
}

func (a *app) direct(ctx context.Context, p provider, method, path string, payload []byte) (*http.Response, error) {
	endpoint, err := providerEndpoint(p, path)
	if err != nil {
		observeUpstreamResult(ctx, p, nil, err)
		logging.Printf(ctx, "ERROR", "gateway.upstream.request_build_failed", "provider_id=%d path=%s err=%v", p.ID, path, err)
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		observeUpstreamResult(ctx, p, nil, err)
		logging.Printf(ctx, "ERROR", "gateway.upstream.request_build_failed", "provider_id=%d path=%s err=%v", p.ID, path, err)
		return nil, err
	}
	providerHeaders(req.Header, nil, p)
	req.Header.Set("Content-Type", "application/json")
	res, err := a.doUpstream(a.client, req, p, payload, a.client.Timeout)
	observeUpstreamResult(ctx, p, res, err)
	return res, err
}

func (a *app) testProvider(w http.ResponseWriter, r *http.Request, path string) {
	id, err := strconv.Atoi(path)
	if err != nil || id <= 0 {
		fail(w, 404, errorcode.NotFound)
		return
	}
	a.mu.Lock()
	var selected provider
	for _, p := range a.data.Providers {
		if p.ID == id {
			selected = p
			break
		}
	}
	a.mu.Unlock()
	if selected.ID == 0 || selected.Type != "api_key" {
		fail(w, 404, errorcode.NotFound)
		return
	}
	res, err := a.direct(r.Context(), selected, "GET", "/models", nil)
	if err != nil {
		fail(w, 502, errorcode.UpstreamError)
		return
	}
	defer res.Body.Close()
	respond(w, 200, map[string]any{"success": res.StatusCode == 200, "latency_ms": 0})
}

func (a *app) apiKeyGateway(w http.ResponseWriter, r *http.Request, p provider, path string) {
	p = normalizeGeminiProvider(p)
	if p.APIFormat == "anthropic_messages" || p.APIFormat == "gemini" {
		if r.Method == http.MethodPost && (path == "chat/completions" || path == "messages" && p.APIFormat == "gemini") {
			source := "chat"
			if path == "messages" {
				source = "messages"
			}
			a.crossProtocolAPI(w, r, p, source)
			return
		}
		if r.Method == http.MethodPost && (path == "messages/count_tokens" && p.APIFormat == "gemini" || path == "responses" || strings.HasPrefix(path, "responses/") || path == "embeddings") {
			source := "chat"
			if strings.HasPrefix(path, "messages") {
				source = "messages"
			}
			crossFailure(w, r, source, 400, errorcode.UnsupportedConversion, &gatewayinfra.ConversionError{Field: "endpoint", Reason: "this endpoint has no cross-protocol conversion; use the upstream native protocol"})
			return
		}
		a.nativeAPI(w, r, p, path)
		return
	}
	switch {
	case path == "responses" && r.Method == "GET":
		a.responsesWebSocket(w, r, p)
	case path == "messages/count_tokens" && r.Method == "POST":
		crossFailure(w, r, "messages", http.StatusBadRequest, errorcode.UnsupportedConversion,
			&gatewayinfra.ConversionError{Field: "endpoint", Reason: "Messages token counting requires an anthropic_messages upstream"})
	case path == "messages" && r.Method == "POST":
		a.apiMessages(w, r, p)
	case path == "live" && r.Method == "POST":
		a.live(w, r, p)
	case strings.HasPrefix(path, "live/") && r.Method == "GET":
		a.liveSideband(w, r, p, strings.TrimPrefix(path, "live/"))
	case path == "alpha/search" && r.Method == "POST":
		a.search(w, r, p)
	case path == "usage" && r.Method == "GET":
		a.usage(w, r)
	case path == "models" && r.Method == "GET":
		a.models(w, r, p)
	case strings.HasPrefix(path, "models/") && r.Method == "GET":
		a.model(w, r, p, strings.TrimPrefix(path, "models/"))
	case path == "responses" && r.Method == "POST", path == "responses/compact" && r.Method == "POST", path == "responses/input_tokens" && r.Method == "POST", path == "chat/completions" && r.Method == "POST", path == "embeddings" && r.Method == "POST":
		a.proxyAPI(w, r, p, "/"+path)
	default:
		recordGatewayFailure(r, "routing", errorcode.NotFound, "endpoint", "method or endpoint is not supported by this provider")
		fail(w, 404, errorcode.NotFound)
	}
}

func containsConfiguredModel(models []string, wanted string) bool {
	return slices.Contains(models, wanted)
}

func (a *app) usage(w http.ResponseWriter, r *http.Request) {
	hash := a.authenticatedKeyHash(r)
	a.mu.Lock()
	current := a.data.Usage[hash]
	a.mu.Unlock()
	key, _ := a.authenticatedKey(r)
	today := current.Daily[time.Now().In(usagetime.Location()).Format("2006-01-02")]
	models := make([]map[string]any, 0, len(current.Models))
	for model, stats := range current.Models {
		models = append(models, map[string]any{
			"model": model, "requests": stats.Requests, "successes": stats.Successes,
			"errors": stats.Errors, "input_tokens": stats.InputTokens,
			"output_tokens": stats.OutputTokens, "total_tokens": stats.TotalTokens,
			"cost": stats.Cost,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i]["model"].(string) < models[j]["model"].(string) })
	respond(w, 200, map[string]any{"data": map[string]any{
		"today": map[string]any{"requests": today.Requests, "successes": today.Successes, "errors": today.Errors, "duration_ms": today.DurationMS, "input_tokens": today.InputTokens, "output_tokens": today.OutputTokens, "total_tokens": today.InputTokens + today.OutputTokens, "cost": today.Cost},
		"total": map[string]any{"requests": current.Requests, "successes": current.Successes, "errors": current.Errors, "duration_ms": current.DurationMS, "input_tokens": current.InputTokens, "output_tokens": current.OutputTokens, "total_tokens": current.InputTokens + current.OutputTokens, "cost": current.Cost},
		"quota": map[string]any{
			"amount": key.QuotaAmount, "tokens": key.QuotaTokens,
			"used_amount": key.QuotaUsedAmount, "used_tokens": key.QuotaUsedTokens,
		},
		"rate_limits": map[string]any{"5h": key.RateLimit5H, "1d": key.RateLimit1D, "7d": key.RateLimit7D},
		"models":      models,
	}})
}

func (a *app) configuredModels(w http.ResponseWriter, r *http.Request, p provider) {
	items := make([]map[string]any, 0, len(p.Models))
	seen := make(map[string]bool)
	for _, id := range p.Models {
		if id == "" || id == "*" || seen[id] {
			continue
		}
		seen[id] = true
		items = append(items, map[string]any{
			"id":       id,
			"slug":     id,
			"name":     id,
			"object":   "model",
			"created":  1735689600,
			"owned_by": p.Platform,
		})
	}
	logging.Printf(r.Context(), "INFO", "gateway.models.response", "format=openai data_field=true models_field=true raw_catalog_count=%d models_count=%d", len(p.Models), len(items))
	respond(w, 200, map[string]any{"object": "list", "data": items, "models": items})
}

func (a *app) proxyAPI(w http.ResponseWriter, r *http.Request, p provider, path string) {
	if r.Method == http.MethodGet && path == "/models" {
		a.models(w, r, p)
		return
	}
	var payload []byte
	if r.Method == "POST" {
		var err error
		payload, err = io.ReadAll(http.MaxBytesReader(w, r.Body, requestBodyLimit(r)))
		if err != nil || !json.Valid(payload) {
			recordRequestBodyFailure(r, err)
			fail(w, 400, errorcode.InvalidRequest)
			return
		}
		if p.ModelMapping != nil {
			payload = rewriteModelInJSON(payload, p.ModelMapping)
		}
		payload = rewriteQwenReasoningEffort(payload, path)
		payload = requestChatStreamUsage(payload, path)
	}
	res, err := a.direct(r.Context(), p, r.Method, path, payload)
	if err != nil {
		fail(w, upstreamErrorStatus(r.Context(), err), upstreamErrorCode(r.Context(), err))
		return
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		writeUpstreamLimitError(w, res, time.Now())
		return
	}
	if res.StatusCode >= 400 {
		fail(w, upstreamStatus(res.StatusCode), upstreamCode(res.StatusCode))
		return
	}
	a.forwardInspectedBody(w, r, res.Body, res.StatusCode, res.Header.Get("Content-Type"))
}

func rewriteModelInJSON(payload []byte, mapping map[string]string) []byte {
	if len(payload) == 0 || len(mapping) == 0 {
		return payload
	}
	var doc map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return payload
	}
	if modelVal, ok := doc["model"].(string); ok && modelVal != "" {
		if target, has := mapping[modelVal]; has && target != "" {
			doc["model"] = target
			if rewritten, err := json.Marshal(doc); err == nil {
				return rewritten
			}
		}
	}
	return payload
}

func appendUsageTail(current, chunk []byte) []byte {
	const maxUsageBody = 1 << 20
	if len(chunk) >= maxUsageBody {
		return append(current[:0], chunk[len(chunk)-maxUsageBody:]...)
	}
	if overflow := len(current) + len(chunk) - maxUsageBody; overflow > 0 {
		current = append(current[:0], current[overflow:]...)
	}
	return append(current, chunk...)
}

func upstreamErrorStatus(ctx context.Context, err error) int {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

func upstreamErrorCode(ctx context.Context, err error) errorcode.Code {
	if upstreamErrorStatus(ctx, err) == http.StatusGatewayTimeout {
		return errorcode.UpstreamTimeout
	}
	return errorcode.ProviderUnavailable
}

func upstreamStatus(status int) int {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return http.StatusBadGateway
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return http.StatusGatewayTimeout
	case http.StatusNotFound:
		return http.StatusNotFound
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return http.StatusBadGateway
	default:
		if status >= 400 {
			return http.StatusBadGateway
		}
		return status
	}
}

func upstreamCode(status int) errorcode.Code {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return errorcode.ProviderAuthFailed
	case http.StatusTooManyRequests:
		return errorcode.RateLimited
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return errorcode.UpstreamTimeout
	case http.StatusNotFound:
		return errorcode.ModelNotAllowed
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return errorcode.ProviderUnavailable
	default:
		return errorcode.UpstreamError
	}
}

// writeUpstreamLimitError classifies safe provider error identifiers without
// exposing provider messages. A 429 status alone does not prove budget exhaustion.
func writeUpstreamLimitError(w http.ResponseWriter, response *http.Response, now time.Time) {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if response.Body != nil {
		_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&envelope)
	}
	code, reason := errorcode.RateLimited, "upstream_rate_limit"
	for _, identifier := range []string{envelope.Error.Code, envelope.Error.Type} {
		switch identifier {
		case "insufficient_quota", "quota_exceeded", "billing_hard_limit_reached":
			code, reason = errorcode.QuotaExceeded, "upstream_quota"
		}
	}
	retry := time.Duration(0)
	header := response.Header.Get("Retry-After")
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/time.Second) {
		retry = time.Duration(seconds) * time.Second
		if retry == 0 {
			retry = time.Second
		}
	} else if reset, err := http.ParseTime(header); err == nil {
		retry = reset.Sub(now)
		if retry <= 0 {
			retry = time.Second
		}
	}
	if retry <= 0 && code == errorcode.RateLimited {
		retry = time.Second
	}
	writeGatewayLimitError(w, code, reason, retry, "")
}
