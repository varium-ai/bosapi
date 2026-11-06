package infrastructure

import (
	"bytes"
	"testing"

	"bosapi/backend/internal/gateway/domain"
)

func TestObserveFilterPreservesMalformedToolsAndAuditsFailures(t *testing.T) {
	payload := []byte(`{"choices":[{"message":{"tool_calls":[{"id":"bad","type":"function","function":{"name":"lookup","arguments":""}}]}}]}`)
	for _, transport := range []string{"json", "websocket", "sse"} {
		t.Run(transport, func(t *testing.T) {
			failures := 0
			filter := NewToolDeliveryFilter(domain.ToolCallContext{Direction: domain.ToolDirectionResponse}, nil, func(domain.ToolCall) bool { return false }, func(error) { failures++ })
			filter.SetObserveOnly(true)
			want := payload
			var got []byte
			switch transport {
			case "json":
				got = filter.JSON(payload)
			case "websocket":
				got = bytes.Join(filter.Frame(payload), nil)
			case "sse":
				want = append(append([]byte("data: "), payload...), []byte("\r\n\r\ndata: [DONE]\r\n\r\n")...)
				for _, chunk := range [][]byte{want[:17], want[17:]} {
					got = append(got, bytes.Join(filter.SSE(chunk, false), nil)...)
					if !bytes.Equal(got, want[:len(got)]) {
						t.Fatal("observe delayed or rewrote a chunk")
					}
				}
				got = append(got, bytes.Join(filter.SSE(nil, true), nil)...)
			}
			if !bytes.Equal(got, want) || failures == 0 {
				t.Fatalf("preserved=%v failures=%d output=%s", bytes.Equal(got, want), failures, got)
			}
		})
	}
}

func TestObserveFilterPreservesBlockedTools(t *testing.T) {
	payload := []byte(`{"output":[{"type":"function_call","call_id":"call","name":"shell","arguments":"{}"}]}`)
	inspections := 0
	filter := NewToolDeliveryFilter(domain.ToolCallContext{}, nil, func(domain.ToolCall) bool { inspections++; return false }, nil)
	filter.SetObserveOnly(true)
	if got := filter.JSON(payload); !bytes.Equal(got, payload) || inspections != 1 {
		t.Fatalf("output=%s inspections=%d", got, inspections)
	}
}
