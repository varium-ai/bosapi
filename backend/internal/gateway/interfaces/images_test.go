package interfaces

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImageGenerationAliases(t *testing.T) {
	a := testApp(t)
	key := "sk-gw-" + random(24)
	a.data.Providers = []provider{{ID: 1, Platform: "openai", Type: "api_key", Status: "active", BaseURL: "https://api.openai.com/v1", APIKey: "upstream-key", Models: []string{"gpt-image-1"}}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.openai.com/v1/images/generations" || r.Header.Get("Authorization") != "Bearer upstream-key" {
			t.Fatalf("unexpected upstream request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"url":"https://example.com/image.png"}]}`)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}
	for _, path := range []string{"/v1/images/generations", "/images/generations"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"gpt-image-1","prompt":"a tree"}`))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "image.png") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestAsyncImageTaskOwnerAndResult(t *testing.T) {
	t.Setenv("BOS_IMAGE_TASK_DIR", t.TempDir())
	a := testApp(t)
	key := "sk-gw-" + random(24)
	a.data.Providers = []provider{{ID: 1, Platform: "openai", Type: "api_key", Status: "active", BaseURL: "https://api.openai.com/v1", APIKey: "upstream-key"}}
	a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"abc"}]}`)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}
	r := httptest.NewRequest("POST", "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"a tree"}`))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	other := httptest.NewRequest("GET", path, nil)
	other.Header.Set("Authorization", "Bearer sk-gw-other")
	denied := httptest.NewRecorder()
	a.routes().ServeHTTP(denied, other)
	if denied.Code != 401 {
		t.Fatalf("other key: %d", denied.Code)
	}
	for i := 0; i < 100; i++ {
		read := httptest.NewRequest("GET", path, nil)
		read.Header.Set("Authorization", "Bearer "+key)
		got := httptest.NewRecorder()
		a.routes().ServeHTTP(got, read)
		if got.Code == 200 && strings.Contains(got.Body.String(), `"b64_json":"abc"`) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("async result was not available")
}

// Exercise the real middleware and settlement event, with image data larger
// than the generic JSON/SSE usage buffers and usage on either side of the data.
func TestImageUsageSettlement(t *testing.T) {
	usage := `"usage":{"input_tokens":98,"output_tokens":343,"total_tokens":441}`
	image := `"data":[{"b64_json":"` + strings.Repeat("A", 2<<20) + `"}]`
	for _, tc := range []struct {
		name, path, body string
		streaming        bool
	}{
		{"usage_after_image", "images/generations", "{" + image + "," + usage + "}", false},
		{"usage_before_image", "images/edits", "{" + usage + "," + image + "}", false},
		{"large_completed_event", "images/generations", "event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\"," + image + "," + usage + "}\n\n", true},
		{"no_usage", "images/generations", "{" + image + "}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			key := "sk-gw-" + random(24)
			a.data.Providers = []provider{{ID: 1, Platform: "openai", Type: "api_key", Status: "active", BaseURL: "https://example.com/v1", APIKey: "upstream-key", Models: []string{"gpt-image-2"}, Pricing: map[string]any{"input": 100.0, "output": 100.0}}}
			a.data.Keys = []apiKey{{ID: 1, Hash: digest(key), Status: "active", ProviderID: 1}}
			a.usageQueue = make(chan usageEvent, 1)
			a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				kind := "application/json"
				if tc.streaming {
					kind = "text/event-stream"
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Content-Type": []string{kind}}}, nil
			})}
			payload := `{"model":"gpt-image-2","prompt":"a cat","stream":false}`
			if tc.streaming {
				payload = `{"model":"gpt-image-2","prompt":"a cat","stream":true}`
			}
			r := httptest.NewRequest("POST", "/v1/"+tc.path, strings.NewReader(payload))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != tc.body {
				t.Fatalf("response changed: status=%d bytes=%d", w.Code, w.Body.Len())
			}
			select {
			case event := <-a.usageQueue:
				m := event.Metrics
				if tc.name == "no_usage" {
					if m.Known || m.Cost != 0 {
						t.Fatalf("invented usage: %+v", m)
					}
					return
				}
				if !m.Known || m.InputTokens != 98 || m.OutputTokens != 343 || m.TotalTokens != 441 || m.Cost != 0.0441 {
					t.Fatalf("incorrect settlement: %+v", m)
				}
				if event.IsStream != tc.streaming {
					t.Fatalf("stream=%t", event.IsStream)
				}
			default:
				t.Fatal("missing settlement event")
			}
		})
	}
}

func TestImageUsageChunkBoundaries(t *testing.T) {
	// Escaped quotes and backslashes must not terminate the discarded image
	// string or make text inside it look like authoritative usage.
	body := `{"data":[{"b64_json":"` + strings.Repeat("A", 300) + `\\\"usage\\\":{\"input_tokens\":999}"}],"usage":{"input_tokens":98,"output_tokens":343,"total_tokens":441}}`
	for _, size := range []int{1, 7, 257} {
		state := &usageMetricsState{}
		o := &imageUsageObserver{state: state}
		for offset := 0; offset < len(body); offset += size {
			end := min(offset+size, len(body))
			o.write([]byte(body[offset:end]))
		}
		o.finish()
		if !state.metrics.Known || state.metrics.TotalTokens != 441 {
			t.Fatalf("chunk size %d: %+v", size, state.metrics)
		}
	}
}
