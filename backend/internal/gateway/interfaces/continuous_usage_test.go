package interfaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type continuousUsageCounts struct {
	Input  int64 `json:"prompt_tokens"`
	Output int64 `json:"completion_tokens"`
	Total  int64 `json:"total_tokens"`
}

type continuousUsageChunk struct {
	Choices []struct {
		Finish *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *continuousUsageCounts `json:"usage"`
}

func TestGatewayContinuousUsageStats(t *testing.T) {
	for _, target := range []string{"anthropic_messages", "gemini", "codex"} {
		for _, tc := range []struct {
			name, options       string
			include, continuous bool
		}{
			{"enabled", `{"include_usage":true,"continuous_usage_stats":true}`, true, true},
			{"disabled", `{"include_usage":true,"continuous_usage_stats":false}`, true, false},
			{"omitted", `{"include_usage":true}`, true, false},
			{"include disabled", `{"include_usage":false,"continuous_usage_stats":true}`, false, false},
			{"include omitted", `{"continuous_usage_stats":true}`, false, false},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				a, key := continuousUsageTestApp(t, target)
				request := strings.TrimSuffix(crossChatRequest, "}") + `,"stream":true,"stream_options":` + tc.options + `}`
				r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(request))
				r.Header.Set("Authorization", "Bearer "+key)
				w := httptest.NewRecorder()
				a.routes().ServeHTTP(w, r)
				if w.Code != http.StatusOK || !w.Flushed || !strings.Contains(w.Body.String(), "data: [DONE]") {
					t.Fatalf("unfinished response: status=%d body=%s", w.Code, w.Body.String())
				}
				assertContinuousUsageChunks(t, w.Body.String(), target, tc.include, tc.continuous)
				assertContinuousUsageAccounting(t, a, target)
			})
		}
	}
}

func continuousUsageTestApp(t *testing.T, target string) (*app, string) {
	t.Helper()
	a, key := crossTestApp(t, target)
	response := crossClaudeStream
	if target == "gemini" {
		response = "data: " + `{"responseId":"gem_1","candidates":[{"content":{"parts":[{"text":"first"}]}}],"usageMetadata":{"promptTokenCount":17,"cachedContentTokenCount":4,"candidatesTokenCount":2,"thoughtsTokenCount":1}}` + "\n\n" +
			"data: " + crossGeminiResponse + "\n\n"
	}
	if target == "codex" {
		a.data.Providers[0] = provider{ID: 1, Type: "oauth", Platform: "openai", APIFormat: "openai_responses", Status: "active", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), Models: []string{"actual"}, ModelMapping: map[string]string{"alias": "actual"}}
		response = codexMetadataStream
	}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "stream_options") || strings.Contains(string(raw), "continuous_usage_stats") {
			t.Fatalf("Chat stream options leaked to native upstream: %s", raw)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(byteFragmentReader{strings.NewReader(response)})}, nil
	})}
	return a, key
}

func assertContinuousUsageChunks(t *testing.T, body, target string, include, continuous bool) {
	t.Helper()
	usageChunks, contentChunks := 0, 0
	totals := make(map[int64]bool)
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk continuousUsageChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatal(err)
		}
		if len(chunk.Choices) == 0 {
			usageChunks++
			if chunk.Usage == nil || *chunk.Usage != finalContinuousUsageCounts(target) {
				t.Fatalf("incorrect final usage: %s", data)
			}
			continue
		}
		contentChunks++
		known := target != "codex" || chunk.Choices[0].Finish != nil
		if (chunk.Usage != nil) != (continuous && known) {
			t.Fatalf("unexpected continuous usage: %s", data)
		}
		if chunk.Usage != nil {
			totals[chunk.Usage.Total] = true
			if chunk.Usage.Input != finalContinuousUsageCounts(target).Input || chunk.Usage.Total != chunk.Usage.Input+chunk.Usage.Output {
				t.Fatalf("incorrect usage snapshot: %s", data)
			}
		}
	}
	wantUsageChunks := 0
	if include {
		wantUsageChunks = 1
	}
	if contentChunks == 0 || usageChunks != wantUsageChunks {
		t.Fatalf("content chunks=%d usage chunks=%d want usage chunks=%d: %s", contentChunks, usageChunks, wantUsageChunks, body)
	}
	if continuous && target != "codex" && len(totals) < 2 {
		t.Fatalf("cumulative usage did not advance: %s", body)
	}
}

func finalContinuousUsageCounts(target string) continuousUsageCounts {
	if target == "codex" {
		return continuousUsageCounts{Input: 15, Output: 5, Total: 20}
	}
	return continuousUsageCounts{Input: 17, Output: 7, Total: 24}
}

func assertContinuousUsageAccounting(t *testing.T, a *app, target string) {
	t.Helper()
	select {
	case event := <-a.usageQueue:
		want := finalContinuousUsageCounts(target)
		if !event.Metrics.Known || event.Metrics.InputTokens != want.Input || event.Metrics.OutputTokens != want.Output || event.Metrics.TotalTokens != want.Total || !event.IsStream {
			t.Fatalf("cumulative usage changed accounting: %+v", event)
		}
	default:
		t.Fatal("missing usage event")
	}
}
