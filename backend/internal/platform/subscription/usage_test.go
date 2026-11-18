package subscription

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSubscriptionUsageDistinguishesMissingAndExhaustedWindows(t *testing.T) {
	for _, tc := range []struct {
		platform, endpoint, body, label string
		method                          string
		used, remaining                 float64
	}{
		{"openai", "chatgpt.com/backend-api/wham/usage", `{"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_at":1893456000},"secondary_window":null}}`, "", http.MethodGet, 100, 0},
		{"anthropic", "api.anthropic.com/api/oauth/usage", `{"five_hour":{},"seven_day":null,"seven_day_sonnet":{"utilization":0,"resets_at":"2030-01-01T00:00:00Z"}}`, "Sonnet", http.MethodGet, 0, 100},
		{"gemini", "cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota", `{"buckets":[{"modelId":"gemini-pro","remainingFraction":0,"resetTime":"2030-01-01T00:00:00Z"},{"modelId":"gemini-flash","remainingFraction":null}]}`, "gemini-pro", http.MethodPost, 100, 0},
		{"antigravity", "cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary", `{"groups":[{"displayName":"Gemini","buckets":[{"window":"weekly","remainingFraction":0.75,"resetTime":"2030-01-01T00:00:00Z"},{"window":"5h"}]}]}`, "Gemini", http.MethodPost, 25, 75},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			client := Client{HTTP: &http.Client{Transport: catalogRoundTripper(func(req *http.Request) (*http.Response, error) {
				if req.Method != tc.method || req.URL.Scheme != "https" || req.URL.Host+req.URL.Path != tc.endpoint || req.Header.Get("Authorization") != "Bearer test-access" {
					t.Fatalf("unexpected quota request: %s %s", req.Method, req.URL)
				}
				if tc.platform == "openai" && req.Header.Get("ChatGPT-Account-ID") != "test-account" {
					t.Fatal("missing account header")
				}
				if tc.method == http.MethodPost {
					raw, _ := io.ReadAll(req.Body)
					if string(raw) != `{"project":"test-project"}` {
						t.Fatalf("unexpected Google request body: %s", raw)
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			got, err := client.Usage(context.Background(), tc.platform, Credentials{
				AccessToken: "test-access", AccountID: "test-account", ProjectID: "test-project",
			})
			if err != nil || len(got.Windows) != 1 || got.CheckedAt.IsZero() {
				t.Fatalf("expected one quota window, got %+v, err=%v", got, err)
			}
			w := got.Windows[0]
			if w.Label != tc.label || w.UsedPercent != tc.used || w.RemainingPercent != tc.remaining || w.ResetsAt == nil || w.ResetsAt.Unix() != 1893456000 {
				t.Fatalf("unexpected usage window: %+v", w)
			}
		})
	}
}

func TestAntigravityUsageFallsBackToModelQuota(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: catalogRoundTripper(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1internal:retrieveUserQuotaSummary":
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		case "/v1internal:fetchAvailableModels":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"models":{"gemini":{"quotaInfo":{"remainingFraction":0.5}},"claude":{"quotaInfo":{}}}}`,
			))}, nil
		default:
			t.Fatalf("unexpected endpoint %s", req.URL)
			return nil, nil
		}
	})}}
	got, err := client.Usage(context.Background(), "antigravity", Credentials{AccessToken: "test-access", ProjectID: "test-project"})
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Label != "gemini" || got.Windows[0].RemainingPercent != 50 {
		t.Fatalf("unexpected model quota: %+v, err=%v", got, err)
	}
}
