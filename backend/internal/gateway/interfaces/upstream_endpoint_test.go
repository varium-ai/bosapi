package interfaces

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDirectBuildsSub2APIStyleCustomBaseURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api-llm/glm5/v1/responses" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	a := testApp(t)
	a.client = upstream.Client()
	response, err := a.direct(context.Background(), provider{
		BaseURL:   upstream.URL + "/api-llm/glm5",
		APIFormat: "openai_responses",
	}, http.MethodPost, "/responses", []byte(`{"model":"glm"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, body = %s", response.StatusCode, body)
	}
}

func TestProviderWebSocketEndpointUsesConfiguredBaseURL(t *testing.T) {
	target, err := providerWebSocketEndpoint(provider{
		BaseURL:   "https://aisrv3.pinoyjobs.ph/api-llm/glm5",
		APIFormat: "openai_responses",
	}, "/responses")
	if err != nil {
		t.Fatal(err)
	}
	if target != "wss://aisrv3.pinoyjobs.ph/api-llm/glm5/v1/responses" {
		t.Fatalf("websocket endpoint = %q", target)
	}
}
