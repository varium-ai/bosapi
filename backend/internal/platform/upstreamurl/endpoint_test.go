package upstreamurl

import "testing"

func TestBuildEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		apiFormat string
		resource  string
		want      string
	}{
		{
			name:      "root gets default openai version",
			baseURL:   "https://deepseek.pinoyjobs.ph",
			apiFormat: "openai_responses",
			resource:  "/responses",
			want:      "https://deepseek.pinoyjobs.ph/v1/responses",
		},
		{
			name:      "custom prefix gets default openai version",
			baseURL:   "https://aisrv3.pinoyjobs.ph/api-llm/glm5/",
			apiFormat: "openai_chat",
			resource:  "/responses",
			want:      "https://aisrv3.pinoyjobs.ph/api-llm/glm5/v1/responses",
		},
		{
			name:      "existing version is preserved",
			baseURL:   "https://example.com/proxy/v2/",
			apiFormat: "openai_chat",
			resource:  "chat/completions",
			want:      "https://example.com/proxy/v2/chat/completions",
		},
		{
			name:      "version suffix with beta number is preserved",
			baseURL:   "https://example.com/gateway/v1beta1",
			apiFormat: "gemini",
			resource:  "/models",
			want:      "https://example.com/gateway/v1beta1/models",
		},
		{
			name:      "gemini gets default version after custom prefix",
			baseURL:   "https://example.com/gateway",
			apiFormat: "gemini",
			resource:  "/models",
			want:      "https://example.com/gateway/v1beta/models",
		},
		{
			name:      "custom format does not add a version",
			baseURL:   "https://example.com/custom-api",
			apiFormat: "custom",
			resource:  "/invoke",
			want:      "https://example.com/custom-api/invoke",
		},
		{
			name:      "complete resource path is not duplicated",
			baseURL:   "https://example.com/api/v1/responses",
			apiFormat: "openai_responses",
			resource:  "/responses",
			want:      "https://example.com/api/v1/responses",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := BuildEndpoint(test.baseURL, test.apiFormat, test.resource)
			if err != nil || got != test.want {
				t.Fatalf("BuildEndpoint() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestBuildEndpointRejectsInvalidURLs(t *testing.T) {
	for _, test := range []struct {
		baseURL  string
		resource string
	}{
		{baseURL: "ftp://example.com", resource: "/responses"},
		{baseURL: "https://example.com", resource: "https://attacker.example/responses"},
		{baseURL: "https://example.com", resource: "/responses?key=secret"},
		{baseURL: "https://example.com", resource: "/"},
	} {
		if _, err := BuildEndpoint(test.baseURL, "openai_chat", test.resource); err == nil {
			t.Fatalf("BuildEndpoint(%q, %q) unexpectedly succeeded", test.baseURL, test.resource)
		}
	}
}
