package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type gatewayFirstTokenKey struct{}

type firstTokenTiming struct {
	started time.Time
	now     func() time.Time
	millis  atomic.Pointer[uint32]
}

func firstTokenState(ctx context.Context) *firstTokenTiming {
	timing, _ := ctx.Value(gatewayFirstTokenKey{}).(*firstTokenTiming)
	return timing
}

func (timing *firstTokenTiming) elapsedMillis() *uint32 {
	if timing == nil {
		return nil
	}
	return timing.millis.Load()
}

func (timing *firstTokenTiming) observe() {
	if timing == nil || timing.started.IsZero() || timing.elapsedMillis() != nil {
		return
	}
	now := time.Now
	if timing.now != nil {
		now = timing.now
	}
	millis := uint32(max(0, min(now().Sub(timing.started).Milliseconds(), int64(^uint32(0)))))
	timing.millis.CompareAndSwap(nil, &millis)
}

// observeFirstTokenBody measures upstream output before conversion or tool
// inspection can buffer it. Only the elapsed time reaches the usage record.
func observeFirstTokenBody(ctx context.Context, response *http.Response) {
	timing := firstTokenState(ctx)
	if timing == nil || timing.elapsedMillis() != nil || response == nil || response.Body == nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}
	reader := &firstTokenBody{ReadCloser: response.Body, timing: timing}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		// Billing has its own parser; sharing its buffer would consume frames twice.
		reader.stream = &usageMetricsState{}
	}
	response.Body = reader
}

type firstTokenBody struct {
	io.ReadCloser
	timing *firstTokenTiming
	stream *usageMetricsState
}

func (body *firstTokenBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	if body.timing.elapsedMillis() != nil {
		return n, err
	}
	if body.stream == nil {
		if len(bytes.TrimSpace(buffer[:n])) > 0 {
			body.timing.observe()
		}
		return n, err
	}
	observeSSEUsage(body.stream, buffer[:n], err != nil, func(value map[string]any, event string) {
		if firstTokenEventOutput(value, event) {
			body.timing.observe()
		}
	})
	if body.timing.elapsedMillis() != nil {
		body.stream = nil
	}
	return n, err
}

func observeFirstTokenFrame(ctx context.Context, payload []byte) {
	timing := firstTokenState(ctx)
	if timing == nil || timing.elapsedMillis() != nil {
		return
	}
	var value map[string]any
	if json.Unmarshal(payload, &value) == nil && firstTokenEventOutput(value, "") {
		timing.observe()
	}
}

func firstTokenEventOutput(value map[string]any, event string) bool {
	if kind, _ := value["type"].(string); kind != "" {
		event = kind
	}
	return firstTokenObjectOutput(value, event)
}

func hasFirstTokenOutput(value any) bool {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			if hasFirstTokenOutput(item) {
				return true
			}
		}
	case map[string]any:
		return firstTokenEventOutput(value, "")
	case string:
		return value != ""
	}
	return false
}

func firstTokenObjectOutput(value map[string]any, kind string) bool {
	if value["error"] != nil || kind == "error" || kind == "response.failed" || kind == "ping" {
		return false
	}
	switch kind {
	case "response.output_text.delta", "response.text.delta", "response.refusal.delta", "response.reasoning.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta", "response.audio.delta", "response.output_audio.delta", "response.audio_transcript.delta", "response.output_audio_transcript.delta":
		text, _ := value["delta"].(string)
		return text != ""
	case "tool_use", "server_tool_use", "function_call":
		name, _ := value["name"].(string)
		if name != "" {
			return true
		}
	}
	for _, field := range []string{"text", "thinking", "reasoning", "reasoning_content", "partial_json", "refusal", "b64_json"} {
		if text, ok := value[field].(string); ok && text != "" {
			return true
		}
	}
	for _, field := range []string{"function", "functionCall"} {
		if function, ok := value[field].(map[string]any); ok {
			if name, _ := function["name"].(string); name != "" {
				return true
			}
			if arguments, _ := function["arguments"].(string); arguments != "" {
				return true
			}
		}
	}
	for _, field := range []string{"audio", "inlineData"} {
		if media, ok := value[field].(map[string]any); ok {
			if data, _ := media["data"].(string); data != "" {
				return true
			}
		}
	}
	for _, field := range []string{"choices", "message", "content", "content_block", "candidates", "parts", "response", "output", "item", "tool_calls"} {
		if hasFirstTokenOutput(value[field]) {
			return true
		}
	}
	if delta, ok := value["delta"].(map[string]any); ok {
		return firstTokenEventOutput(delta, "")
	}
	return false
}
