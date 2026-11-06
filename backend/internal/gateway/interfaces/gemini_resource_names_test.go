package interfaces

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoogleResourceNamesRemainUsableWithoutResynchronizing(t *testing.T) {
	a, key := geminiTestApp(t)
	a.data.Providers[0].Models = []string{"models/actual"}
	a.data.Providers[0].ModelMapping = map[string]string{"models/alias": "models/actual"}
	a.data.Providers[0].Pricing = map[string]any{"models": map[string]any{"models/actual": map[string]any{"input": 2.0, "output": 4.0, "cache_read": 0.5}}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := `{"name":"models/actual","supportedGenerationMethods":["generateContent","countTokens"]}`
		if r.Method == "POST" {
			if r.URL.Path != "/v1beta/models/actual:generateContent" {
				t.Fatalf("bad model URL: %s", r.URL.Path)
			}
			response = `{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`
		} else if r.URL.Path != "/v1beta/models/actual" {
			t.Fatalf("bad catalog URL: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	get := httptest.NewRequest("GET", "/v1/v1beta/models?key="+key, nil)
	catalog := httptest.NewRecorder()
	a.routes().ServeHTTP(catalog, get)
	if catalog.Code != 200 || !strings.Contains(catalog.Body.String(), `"name":"models/actual"`) || !strings.Contains(catalog.Body.String(), `"name":"models/alias"`) || strings.Contains(catalog.Body.String(), "models/models/") {
		t.Fatalf("%d %s", catalog.Code, catalog.Body.String())
	}
	for _, name := range []string{"alias", "actual", "unauthorized"} {
		r := httptest.NewRequest("POST", "/v1/v1beta/models/"+name+":generateContent?key="+key, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if name == "unauthorized" {
			if w.Code != 403 {
				t.Fatalf("model permission bypassed: %d", w.Code)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatalf("inference %d %s", w.Code, w.Body.String())
		}
		event := <-a.usageQueue
		if event.Model != name || event.Metrics.TotalTokens != 15 || event.Metrics.Cost != 40.0/1_000_000 {
			t.Fatalf("model/pricing changed: %+v", event)
		}
	}
	if a.data.Providers[0].Models[0] != "models/actual" || a.data.Providers[0].ModelMapping["models/alias"] != "models/actual" {
		t.Fatal("stored model configuration mutated")
	}
	// Slash-containing IDs for other providers are not Gemini resource names.
	p := normalizeGeminiProvider(provider{APIFormat: "openai_chat", Models: []string{"models/actual"}})
	if p.Models[0] != "models/actual" {
		t.Fatal("OpenAI namespace changed")
	}
}
