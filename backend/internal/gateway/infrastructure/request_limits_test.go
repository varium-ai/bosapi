package infrastructure

import (
	"testing"

	"bosapi/backend/internal/gateway/domain"
)

func TestToolInspectionConfiguredPayloadLimit(t *testing.T) {
	payload := []byte(`{"model":"test","messages":[{"role":"user","content":"hello"}]}`)
	for _, limit := range []int64{int64(len(payload)) - 1, int64(len(payload)), 256 << 20} {
		parser := NewToolCallParser(domain.ToolCallContext{Direction: domain.ToolDirectionRequest}, nil, limit)
		_, err := parser.Parse(payload)
		if (err != nil) != (limit < int64(len(payload))) {
			t.Fatalf("parser limit %d: %v", limit, err)
		}
		stream := NewToolCallStream(domain.ToolCallContext{Direction: domain.ToolDirectionRequest}, nil, limit)
		_, err = stream.FeedJSON(payload)
		if (err != nil) != (limit < int64(len(payload))) {
			t.Fatalf("stream limit %d: %v", limit, err)
		}
	}
	for _, limit := range []int64{0, -1} {
		parser := NewToolCallParser(domain.ToolCallContext{}, nil, limit)
		if parser.maxPayloadBytes != maxToolPayload {
			t.Fatalf("invalid optional limit %d did not use default", limit)
		}
	}
}
