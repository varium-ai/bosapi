package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bosapi/backend/internal/admin/domain"
	"bosapi/backend/internal/platform/logging"
	"bosapi/backend/internal/platform/subscription"
	"bosapi/backend/internal/platform/upstreamurl"
)

// ErrProviderDiscovery identifies a safe, sanitized provider discovery failure.
var ErrProviderDiscovery = errors.New("provider discovery failed")
var ErrProviderAuthentication = errors.New("upstream rejected authentication")
var ErrProviderAuthorization = errors.New("upstream denied access")
var ErrProviderRateLimited = errors.New("upstream rate limited")
var ErrProviderTimeout = errors.New("upstream timed out")
var ErrProviderProtocol = errors.New("upstream protocol or model endpoint unavailable")

// Discover fetches supported model metadata and atomically updates the provider catalog.
func (service *ProviderService) Discover(ctx context.Context, id uint64) ([]domain.ProviderModel, error) {
	if service.models == nil || service.decrypt == nil || id == 0 {
		return nil, ErrProviderDiscovery
	}
	provider, credential, err := service.Credential(ctx, id)
	if err != nil {
		return nil, err
	}
	var items []domain.ProviderModel
	if provider.AccountType == "oauth" {
		var credentials subscription.Credentials
		credentials, err = subscription.Decode(credential, provider.Platform)
		if err != nil {
			return nil, err
		}
		var raw []byte
		raw, err = service.oauthClient.Models(ctx, provider.Platform, credentials)
		if err != nil {
			return nil, err
		}
		if provider.Platform == "gemini" {
			// Code Assist has no public model catalog; preserve the explicit list.
			items, _, err = service.models.ListProviderModels(ctx, id, "active", 1, 1000)
			return items, err
		}
		items, err = parseDiscoveredProviderModels(ctx, provider, raw, service.metadata)
	} else {
		items, err = fetchProviderModels(ctx, provider, credential, service.metadata)
	}
	if err != nil {
		return nil, err
	}
	if err := service.models.UpsertProviderModels(ctx, id, items); err != nil {
		return nil, fmt.Errorf("persist discovered provider models: %w", err)
	}
	return items, nil
}

// Test checks provider model connectivity without returning upstream response details.
func (service *ProviderService) Test(ctx context.Context, id uint64) (result error) {
	if service.decrypt == nil || id == 0 {
		return ErrProviderDiscovery
	}
	provider, credential, err := service.Credential(ctx, id)
	if err != nil {
		return err
	}
	// Record only the tested configuration, including a failed retest.
	if recorder, ok := service.repository.(interface {
		RecordProviderVerification(context.Context, uint64, time.Time, bool) error
	}); ok {
		defer func() {
			if err := recorder.RecordProviderVerification(ctx, id, provider.UpdatedAt, result == nil); err != nil && result == nil {
				result = err
			}
		}()
	}
	if provider.AccountType == "oauth" {
		credentials, err := subscription.Decode(credential, provider.Platform)
		if err != nil {
			return err
		}
		_, err = service.oauthClient.Models(ctx, provider.Platform, credentials)
		return err
	}
	_, err = requestProviderModels(ctx, provider, credential, false)
	return err
}

type discoveredModels struct {
	Data   []discoveredModel `json:"data"`
	Models []discoveredModel `json:"models"`
}

type discoveredModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func fetchProviderModels(ctx context.Context, provider domain.Provider, credential string, metadataRepo domain.ModelMetadataRepository) ([]domain.ProviderModel, error) {
	body, err := requestProviderModels(ctx, provider, credential, true)
	if err != nil {
		return nil, err
	}
	return parseDiscoveredProviderModels(ctx, provider, body, metadataRepo)
}

func parseDiscoveredProviderModels(ctx context.Context, provider domain.Provider, body []byte, metadataRepo domain.ModelMetadataRepository) ([]domain.ProviderModel, error) {
	var payload discoveredModels
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrProviderDiscovery
	}
	models := payload.Data
	if len(models) == 0 {
		models = payload.Models
	}
	if len(models) == 0 {
		return nil, ErrProviderDiscovery
	}
	prices := provider.Pricing
	capabilities := provider.Capabilities
	result := make([]domain.ProviderModel, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.ID)
		if name == "" {
			name = strings.TrimSpace(model.Slug)
		}
		if name == "" {
			name = strings.TrimSpace(model.Name)
		}
		storedName := name
		name = upstreamurl.ModelID(provider.APIFormat, name)
		if name == "" || len(name) > 191 {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		inputPrice := number(prices["input"])
		outputPrice := number(prices["output"])
		cacheReadPrice := number(prices["cache_read"])
		cacheWritePrice := number(prices["cache_write"])
		if modelPricing, ok := prices["models"].(map[string]any); ok {
			specific, ok := modelPricing[storedName].(map[string]any)
			if !ok {
				specific, ok = modelPricing[name].(map[string]any)
			}
			if ok {
				if v, exists := specific["input"]; exists {
					inputPrice = number(v)
				}
				if v, exists := specific["output"]; exists {
					outputPrice = number(v)
				}
				if v, exists := specific["cache_read"]; exists {
					cacheReadPrice = number(v)
				}
				if v, exists := specific["cache_write"]; exists {
					cacheWritePrice = number(v)
				}
			}
		}
		var meta *domain.ModelMetadata
		if metadataRepo != nil {
			meta, _ = metadataRepo.LookupCapabilities(ctx, name)
		}
		modelCaps := resolveDiscoveredCapabilities(name, provider.Platform, provider.APIFormat, capabilities, meta)
		result = append(result, domain.ProviderModel{
			ProviderID:      provider.ID,
			ModelName:       storedName,
			Capabilities:    modelCaps,
			PriceInput:      inputPrice,
			PriceOutput:     outputPrice,
			PriceCacheRead:  cacheReadPrice,
			PriceCacheWrite: cacheWritePrice,
			Status:          "active",
		})
	}
	if len(result) == 0 {
		return nil, ErrProviderDiscovery
	}
	return result, nil
}

// requestProviderModels checks the upstream response. Connectivity tests do not
// depend on the provider exposing a model catalog in a particular JSON shape.
func requestProviderModels(ctx context.Context, provider domain.Provider, credential string, readBody bool) ([]byte, error) {
	endpoint, err := discoveryURL(provider)
	if err != nil {
		return nil, ErrProviderDiscovery
	}
	parsed, _ := url.Parse(endpoint)
	transport := &http.Transport{DialContext: func(dialCtx context.Context, _, address string) (net.Conn, error) {
		return dialPublicHost(dialCtx, address, parsed.Hostname())
	}}
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrProviderDiscovery
	}
	if credential != "" {
		switch provider.APIFormat {
		case "anthropic_messages":
			request.Header.Set("x-api-key", credential)
			request.Header.Set("anthropic-version", "2023-06-01")
		case "gemini":
			request.Header.Set("x-goog-api-key", credential)
		default:
			request.Header.Set("Authorization", "Bearer "+credential)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		logging.Printf(ctx, "ERROR", "admin.provider_discovery.transport_failed", "provider_id=%d", provider.ID)
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrProviderTimeout
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, ErrProviderTimeout
		}
		return nil, ErrProviderDiscovery
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		logging.Printf(ctx, "WARN", "admin.provider_discovery.bad_status", "provider_id=%d upstream_status=%d", provider.ID, response.StatusCode)
	}
	return readProviderModelResponse(response, readBody)
}

func readProviderModelResponse(response *http.Response, readBody bool) ([]byte, error) {
	switch response.StatusCode {
	case 401:
		return nil, ErrProviderAuthentication
	case 403:
		return nil, ErrProviderAuthorization
	case 429:
		return nil, ErrProviderRateLimited
	case 404, 405, 415:
		return nil, ErrProviderProtocol
	case 408, 504:
		return nil, ErrProviderTimeout
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrProviderDiscovery
	}
	if !readBody {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return nil, ErrProviderDiscovery
	}
	return body, nil
}

func discoveryURL(provider domain.Provider) (string, error) {
	base, err := url.ParseRequestURI(provider.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", ErrProviderDiscovery
	}
	endpoint, err := upstreamurl.BuildEndpoint(provider.BaseURL, provider.APIFormat, "/models")
	if err != nil {
		return "", ErrProviderDiscovery
	}
	return endpoint, nil
}

func dialPublicHost(ctx context.Context, address, expectedHost string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || !strings.EqualFold(host, expectedHost) {
		return nil, ErrProviderDiscovery
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, ErrProviderDiscovery
	}
	var lastError error
	for _, candidate := range ips {
		ip, ok := netip.AddrFromSlice(candidate.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() || ip.String() == "100.100.100.200" {
			return nil, ErrProviderDiscovery
		}
		connection, dialErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastError = dialErr
	}
	if lastError != nil {
		return nil, lastError
	}
	return nil, ErrProviderDiscovery
}

func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	default:
		return 0
	}
}

func resolveDiscoveredCapabilities(modelName, platform, apiFormat string, baseCaps map[string]any, meta ...*domain.ModelMetadata) map[string]any {
	caps := make(map[string]any)
	for k, v := range baseCaps {
		caps[k] = v
	}

	var metadata *domain.ModelMetadata
	if len(meta) > 0 {
		metadata = meta[0]
	}

	if metadata != nil {
		if _, ok := caps["vision"]; !ok {
			caps["vision"] = metadata.SupportsVision
		}
		if _, ok := caps["video"]; !ok {
			caps["video"] = metadata.SupportsVideo
		}
		if _, ok := caps["tools"]; !ok {
			caps["tools"] = metadata.SupportsTools
		}
		if _, ok := caps["reasoning"]; !ok {
			caps["reasoning"] = metadata.SupportsReasoning
		}
		if _, ok := caps["max_input_tokens"]; !ok && metadata.ContextWindow > 0 {
			caps["max_input_tokens"] = metadata.ContextWindow
		}
		if _, ok := caps["modalities"]; !ok && len(metadata.InputModalities) > 0 {
			caps["modalities"] = metadata.InputModalities
		}
	}

	lower := strings.ToLower(modelName)

	isEmbedding := strings.Contains(lower, "embed")
	isImageGen := strings.Contains(lower, "dall-e") || strings.Contains(lower, "image") || strings.Contains(lower, "flux") || strings.Contains(lower, "midjourney") || strings.Contains(lower, "stable-diffusion") || strings.Contains(lower, "sdxl")
	isVideoGen := strings.Contains(lower, "sora") ||
		strings.Contains(lower, "kling") ||
		strings.Contains(lower, "cogvideox") ||
		strings.Contains(lower, "runway") ||
		strings.Contains(lower, "luma") ||
		strings.Contains(lower, "pika") ||
		strings.Contains(lower, "vidu") ||
		strings.Contains(lower, "wanx") ||
		strings.Contains(lower, "hailuo") ||
		strings.Contains(lower, "minimax-video") ||
		strings.Contains(lower, "hunyuan-video") ||
		strings.Contains(lower, "mochi")
	isVideoUnderstanding := strings.Contains(lower, "gemini-1.5") ||
		strings.Contains(lower, "gemini-2.0") ||
		strings.Contains(lower, "gemini-exp") ||
		strings.Contains(lower, "qwen2-vl") ||
		strings.Contains(lower, "qwen2.5-vl") ||
		strings.Contains(lower, "internvl2") ||
		strings.Contains(lower, "cogvlm2-video") ||
		strings.Contains(lower, "video-llava") ||
		strings.Contains(lower, "videollama")
	isVideo := isVideoGen || isVideoUnderstanding || (strings.Contains(lower, "video") && !strings.Contains(lower, "text"))
	isAudio := strings.Contains(lower, "whisper") || strings.Contains(lower, "tts") || strings.Contains(lower, "audio")
	isRealtime := strings.Contains(lower, "realtime")

	if _, ok := caps["endpoints"]; !ok {
		endpoints := make([]string, 0, 2)
		switch {
		case isEmbedding:
			endpoints = append(endpoints, "embeddings")
		case isImageGen:
			endpoints = append(endpoints, "images/generations")
		case isVideoGen:
			endpoints = append(endpoints, "videos/generations")
		case isAudio:
			if strings.Contains(lower, "whisper") || strings.Contains(lower, "transcription") {
				endpoints = append(endpoints, "audio/transcriptions")
			} else {
				endpoints = append(endpoints, "audio/speech")
			}
		case isRealtime:
			endpoints = append(endpoints, "realtime")
		default:
			if apiFormat == "anthropic_messages" {
				endpoints = append(endpoints, "messages")
			} else {
				endpoints = append(endpoints, "chat.completions")
			}
		}
		caps["endpoints"] = endpoints
	}

	if _, ok := caps["vision"]; !ok {
		isVision := strings.Contains(lower, "vision") ||
			strings.Contains(lower, "vl") ||
			strings.Contains(lower, "4o") ||
			strings.Contains(lower, "gemini") ||
			strings.Contains(lower, "claude-3") ||
			strings.Contains(lower, "internvl") ||
			strings.Contains(lower, "glm-5") ||
			strings.Contains(lower, "glm-4v") ||
			strings.Contains(lower, "glm-4.5v") ||
			strings.Contains(lower, "qwenvl") ||
			strings.Contains(lower, "qwen-vl") ||
			strings.Contains(lower, "minicpm-v") ||
			strings.Contains(lower, "pixtral") ||
			strings.Contains(lower, "llava") ||
			strings.Contains(lower, "omni")
		caps["vision"] = isVision
	}

	if _, ok := caps["video"]; !ok {
		caps["video"] = isVideo
	}

	if _, ok := caps["modalities"]; !ok {
		modalities := []string{"text"}
		if isAudio {
			modalities = append(modalities, "audio")
		}
		if isImageGen || caps["vision"] == true {
			modalities = append(modalities, "image")
		}
		if isVideo {
			modalities = append(modalities, "video")
		}
		caps["modalities"] = modalities
	} else if mods, ok := caps["modalities"].([]string); ok {
		hasImg := false
		hasVid := false
		for _, m := range mods {
			if m == "image" {
				hasImg = true
			}
			if m == "video" {
				hasVid = true
			}
		}
		if (caps["vision"] == true || isImageGen) && !hasImg {
			mods = append(mods, "image")
		}
		if caps["video"] == true && !hasVid {
			mods = append(mods, "video")
		}
		caps["modalities"] = mods
	}

	if _, ok := caps["stream"]; !ok {
		caps["stream"] = !isEmbedding && !isImageGen && !isVideoGen
	}

	if _, ok := caps["tools"]; !ok {
		caps["tools"] = !isEmbedding && !isImageGen && !isVideoGen && !isAudio
	}

	if _, ok := caps["reasoning"]; !ok {
		isReasoning := strings.Contains(lower, "r1") ||
			strings.HasPrefix(lower, "o1") ||
			strings.Contains(lower, "-o1") ||
			strings.HasPrefix(lower, "o3") ||
			strings.Contains(lower, "-o3") ||
			strings.Contains(lower, "qwq") ||
			strings.Contains(lower, "reasoning") ||
			strings.Contains(lower, "thinking")
		caps["reasoning"] = isReasoning
	}

	if _, ok := caps["max_input_tokens"]; !ok {
		switch {
		case strings.Contains(lower, "gemini-1.5") || strings.Contains(lower, "gemini-2.0") || strings.Contains(lower, "gemini-exp"):
			caps["max_input_tokens"] = 1000000
		case strings.Contains(lower, "claude-3") || strings.Contains(lower, "claude-3-5") || strings.Contains(lower, "claude-3-7"):
			caps["max_input_tokens"] = 200000
		case strings.Contains(lower, "4o") || strings.Contains(lower, "gpt-4-turbo"):
			caps["max_input_tokens"] = 128000
		case strings.Contains(lower, "glm-5"):
			caps["max_input_tokens"] = 200000
		case strings.Contains(lower, "deepseek"):
			caps["max_input_tokens"] = 64000
		case strings.Contains(lower, "qwen2.5"):
			caps["max_input_tokens"] = 128000
		case isEmbedding:
			caps["max_input_tokens"] = 8192
		default:
			caps["max_input_tokens"] = 32000
		}
	}

	return caps
}
