package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"bosapi/backend/internal/admin/domain"
)

func TestDiscoveryURL(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		apiFormat string
		want      string
		wantErr   bool
	}{
		{name: "openai root", baseURL: "https://upstream.example.com/", apiFormat: "openai_chat", want: "https://upstream.example.com/v1/models"},
		{name: "openai versioned", baseURL: "https://api.openai.com/v1", apiFormat: "openai_responses", want: "https://api.openai.com/v1/models"},
		{name: "openai prefixed version", baseURL: "https://example.com/api/v1/", apiFormat: "openai_chat", want: "https://example.com/api/v1/models"},
		{name: "openai custom prefix", baseURL: "https://aisrv3.pinoyjobs.ph/api-llm/glm5", apiFormat: "openai_chat", want: "https://aisrv3.pinoyjobs.ph/api-llm/glm5/v1/models"},
		{name: "openai alternate version", baseURL: "https://example.com/gateway/v2", apiFormat: "openai_chat", want: "https://example.com/gateway/v2/models"},
		{name: "complete models path", baseURL: "https://example.com/api/v1/models", apiFormat: "openai_chat", want: "https://example.com/api/v1/models"},
		{name: "anthropic root", baseURL: "https://api.anthropic.com", apiFormat: "anthropic_messages", want: "https://api.anthropic.com/v1/models"},
		{name: "gemini root", baseURL: "https://generativelanguage.googleapis.com", apiFormat: "gemini", want: "https://generativelanguage.googleapis.com/v1beta/models"},
		{name: "gemini versioned", baseURL: "https://generativelanguage.googleapis.com/v1beta/", apiFormat: "gemini", want: "https://generativelanguage.googleapis.com/v1beta/models"},
		{name: "custom root", baseURL: "https://example.com", apiFormat: "custom", want: "https://example.com/models"},
		{name: "http public IP", baseURL: "http://160.30.1.248", apiFormat: "openai_chat", want: "http://160.30.1.248/v1/models"},
		{name: "http versioned with port", baseURL: "http://example.com:8080/v1/", apiFormat: "openai_chat", want: "http://example.com:8080/v1/models"},
		{name: "reject unsupported scheme", baseURL: "ftp://example.com", apiFormat: "openai_chat", wantErr: true},
		{name: "reject user info", baseURL: "http://user:password@example.com", apiFormat: "openai_chat", wantErr: true},
		{name: "reject query", baseURL: "http://example.com?key=secret", apiFormat: "openai_chat", wantErr: true},
		{name: "reject fragment", baseURL: "http://example.com#models", apiFormat: "openai_chat", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := discoveryURL(domain.Provider{BaseURL: test.baseURL, APIFormat: test.apiFormat})
			if test.wantErr {
				if !errors.Is(err, ErrProviderDiscovery) {
					t.Fatalf("error = %v, want %v", err, ErrProviderDiscovery)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("discoveryURL() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestProviderConnectivityAcceptsSuccessfulNonCatalogResponse(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("not a model catalog"))}
	if _, err := readProviderModelResponse(response, false); err != nil {
		t.Fatalf("successful upstream response should pass connectivity test: %v", err)
	}
}

func TestProviderConnectivityRejectsUpstreamError(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader("sensitive upstream body"))}
	if _, err := readProviderModelResponse(response, false); err != ErrProviderAuthentication {
		t.Fatalf("expected sanitized discovery error, got %v", err)
	}
}

func TestResolveDiscoveredCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		modelName    string
		platform     string
		apiFormat    string
		wantVision   bool
		wantVideo    bool
		wantStream   bool
		wantTools    bool
		wantReason   bool
		wantEndpoint string
	}{
		{
			name:         "gpt-4o vision and stream",
			modelName:    "gpt-4o",
			platform:     "openai",
			apiFormat:    "openai_chat",
			wantVision:   true,
			wantStream:   true,
			wantTools:    true,
			wantReason:   false,
			wantEndpoint: "chat.completions",
		},
		{
			name:         "claude-3-7-sonnet anthropic messages",
			modelName:    "claude-3-7-sonnet",
			platform:     "anthropic",
			apiFormat:    "anthropic_messages",
			wantVision:   true,
			wantStream:   true,
			wantTools:    true,
			wantReason:   false,
			wantEndpoint: "messages",
		},
		{
			name:         "deepseek-r1 reasoning",
			modelName:    "deepseek-r1",
			platform:     "deepseek",
			apiFormat:    "openai_chat",
			wantVision:   false,
			wantStream:   true,
			wantTools:    true,
			wantReason:   true,
			wantEndpoint: "chat.completions",
		},
		{
			name:         "text-embedding-3-small",
			modelName:    "text-embedding-3-small",
			platform:     "openai",
			apiFormat:    "openai_chat",
			wantVision:   false,
			wantStream:   false,
			wantTools:    false,
			wantReason:   false,
			wantEndpoint: "embeddings",
		},
		{
			name:         "dall-e-3 image gen",
			modelName:    "dall-e-3",
			platform:     "openai",
			apiFormat:    "openai_chat",
			wantVision:   false,
			wantVideo:    false,
			wantStream:   false,
			wantTools:    false,
			wantReason:   false,
			wantEndpoint: "images/generations",
		},
		{
			name:         "gemini-1.5-pro multimodal video & vision",
			modelName:    "gemini-1.5-pro",
			platform:     "google",
			apiFormat:    "openai_chat",
			wantVision:   true,
			wantVideo:    true,
			wantStream:   true,
			wantTools:    true,
			wantReason:   false,
			wantEndpoint: "chat.completions",
		},
		{
			name:         "qwen2.5-vl-72b multimodal video & vision",
			modelName:    "qwen2.5-vl-72b",
			platform:     "qwen",
			apiFormat:    "openai_chat",
			wantVision:   true,
			wantVideo:    true,
			wantStream:   true,
			wantTools:    true,
			wantReason:   false,
			wantEndpoint: "chat.completions",
		},
		{
			name:         "cogvideox video generation",
			modelName:    "cogvideox-5b",
			platform:     "zhipu",
			apiFormat:    "openai_chat",
			wantVision:   false,
			wantVideo:    true,
			wantStream:   false,
			wantTools:    false,
			wantReason:   false,
			wantEndpoint: "videos/generations",
		},
		{
			name:         "glm-5.3-flash multimodal vision",
			modelName:    "zai-org/GLM-5.3-Flash",
			platform:     "zhipu",
			apiFormat:    "openai_chat",
			wantVision:   true,
			wantVideo:    false,
			wantStream:   true,
			wantTools:    true,
			wantReason:   false,
			wantEndpoint: "chat.completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := resolveDiscoveredCapabilities(tt.modelName, tt.platform, tt.apiFormat, nil)
			if got, ok := caps["vision"].(bool); !ok || got != tt.wantVision {
				t.Errorf("vision = %v, want %v", caps["vision"], tt.wantVision)
			}
			if got, ok := caps["video"].(bool); !ok || got != tt.wantVideo {
				t.Errorf("video = %v, want %v", caps["video"], tt.wantVideo)
			}
			if got, ok := caps["stream"].(bool); !ok || got != tt.wantStream {
				t.Errorf("stream = %v, want %v", caps["stream"], tt.wantStream)
			}
			if got, ok := caps["tools"].(bool); !ok || got != tt.wantTools {
				t.Errorf("tools = %v, want %v", caps["tools"], tt.wantTools)
			}
			if got, ok := caps["reasoning"].(bool); !ok || got != tt.wantReason {
				t.Errorf("reasoning = %v, want %v", caps["reasoning"], tt.wantReason)
			}
			endpoints, ok := caps["endpoints"].([]string)
			if !ok || len(endpoints) == 0 || endpoints[0] != tt.wantEndpoint {
				t.Errorf("endpoints = %v, want %v", caps["endpoints"], tt.wantEndpoint)
			}
		})
	}
}

func TestResolveDiscoveredCapabilitiesWithMetadata(t *testing.T) {
	meta := &domain.ModelMetadata{
		SupportsVision:    true,
		SupportsVideo:     true,
		SupportsTools:     false,
		SupportsReasoning: true,
		ContextWindow:     500000,
		InputModalities:   []string{"text", "image", "video"},
	}
	caps := resolveDiscoveredCapabilities("custom-unknown-model", "custom", "openai_chat", nil, meta)
	if caps["vision"] != true {
		t.Fatalf("expected vision=true, got %v", caps["vision"])
	}
	if caps["video"] != true {
		t.Fatalf("expected video=true, got %v", caps["video"])
	}
	if caps["tools"] != false {
		t.Fatalf("expected tools=false, got %v", caps["tools"])
	}
	if caps["reasoning"] != true {
		t.Fatalf("expected reasoning=true, got %v", caps["reasoning"])
	}
	if caps["max_input_tokens"] != 500000 {
		t.Fatalf("expected max_input_tokens=500000, got %v", caps["max_input_tokens"])
	}
}

func TestGeminiDiscoveredResourcesKeepStorageAndUseCanonicalPricing(t *testing.T) {
	provider := domain.Provider{ID: 1, Platform: "gemini", APIFormat: "gemini", Pricing: map[string]any{"models": map[string]any{"gemini-flash-latest": map[string]any{"input": 2.0, "output": 4.0}}}}
	models, err := parseDiscoveredProviderModels(context.Background(), provider, []byte(`{"models":[{"name":"models/gemini-flash-latest"}]}`), nil)
	if err != nil || len(models) != 1 || models[0].ModelName != "models/gemini-flash-latest" || models[0].PriceInput != 2 || models[0].PriceOutput != 4 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}

func TestProviderModelHTTPFailuresHaveEvidenceBasedCodes(t *testing.T) {
	for _, item := range []struct {
		status int
		want   error
	}{{401, ErrProviderAuthentication}, {403, ErrProviderAuthorization}, {429, ErrProviderRateLimited}, {404, ErrProviderProtocol}, {405, ErrProviderProtocol}, {415, ErrProviderProtocol}, {408, ErrProviderTimeout}, {504, ErrProviderTimeout}, {500, ErrProviderDiscovery}} {
		response := &http.Response{StatusCode: item.status, Body: io.NopCloser(strings.NewReader("secret upstream response"))}
		_, err := readProviderModelResponse(response, false)
		if err != item.want || strings.Contains(err.Error(), "secret") {
			t.Fatalf("status %d: %v", item.status, err)
		}
	}
}
