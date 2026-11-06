package infrastructure

import (
	"bytes"
	"encoding/json"
	"strings"

	"bosapi/backend/internal/gateway/domain"
)

type pendingToolCall struct {
	protocol      domain.ToolProtocol
	id            string
	name          string
	input         any
	arguments     []byte
	metadataBytes int
}

// ToolCallStream assembles bounded SSE or WebSocket deltas before inspecting
// parameters. Each instance belongs to one request direction and one connection.
type ToolCallStream struct {
	parser  *ToolCallParser
	pending map[string]*pendingToolCall
	order   []string
	emitted map[string]bool
	buffer  []byte
	data    []byte
	event   string
	size    int
	failed  bool
}

// NewToolCallStream applies the optional payload limit to connection inspection.
func NewToolCallStream(caller domain.ToolCallContext, known []domain.ToolCall, limits ...int64) *ToolCallStream {
	return &ToolCallStream{parser: NewToolCallParser(caller, known, limits...), pending: make(map[string]*pendingToolCall), emitted: make(map[string]bool)}
}

// Reset resumes parsing after a failed WebSocket request without losing bounded
// invocation correlation. Failed deltas never carry over into the next request.
func (stream *ToolCallStream) Reset() {
	known := make([]domain.ToolCall, 0, len(stream.parser.known))
	for _, call := range stream.parser.known {
		known = append(known, call)
	}
	*stream = *NewToolCallStream(stream.parser.caller, known, stream.parser.maxPayloadBytes)
}

// Remember correlates results with invocations observed in the opposite direction.
// The caller must serialize access when a stream is used by multiple goroutines.
func (stream *ToolCallStream) Remember(calls []domain.ToolCall) {
	for _, call := range calls {
		stream.parser.remember(call)
	}
}

func (stream *ToolCallStream) start(key string, protocol domain.ToolProtocol) (*pendingToolCall, error) {
	if len(key) > 1024 {
		return nil, stream.parser.invalid(protocol)
	}
	if call := stream.pending[key]; call != nil {
		return call, nil
	}
	if len(stream.pending) >= maxToolCalls {
		return nil, stream.parser.invalid(protocol)
	}
	call := &pendingToolCall{protocol: protocol}
	stream.pending[key] = call
	stream.order = append(stream.order, key)
	return call, nil
}

func (stream *ToolCallStream) setMetadata(call *pendingToolCall, id, name string, input any) error {
	cost := len(id) + len(name) + len(toolJSON(input))
	if stream.size-call.metadataBytes+cost > int(stream.parser.maxPayloadBytes) {
		return stream.parser.invalid(call.protocol)
	}
	stream.size += cost - call.metadataBytes
	call.id, call.name, call.input, call.metadataBytes = id, name, input, cost
	return nil
}

func (stream *ToolCallStream) appendArguments(call *pendingToolCall, fragment string) error {
	if stream.size+len(fragment) > int(stream.parser.maxPayloadBytes) {
		return stream.parser.invalid(call.protocol)
	}
	call.arguments = append(call.arguments, fragment...)
	stream.size += len(fragment)
	return nil
}

func (stream *ToolCallStream) finish(key string) ([]domain.ToolCall, error) {
	call := stream.pending[key]
	if call == nil {
		return nil, nil
	}
	delete(stream.pending, key)
	stream.size -= len(call.arguments) + call.metadataBytes
	for index, candidate := range stream.order {
		if candidate == key {
			stream.order = append(stream.order[:index], stream.order[index+1:]...)
			break
		}
	}
	if call.id != "" && stream.emitted[toolCorrelationKey(domain.ToolCall{Protocol: call.protocol, ID: call.id})+":"+string(domain.ToolCallInvocation)] {
		return nil, nil
	}
	var calls []domain.ToolCall
	input, encoded := call.input, false
	if len(call.arguments) > 0 || input == nil {
		input, encoded = string(call.arguments), true
	}
	if err := stream.parser.invocation(call.protocol, call.id, call.name, input, encoded, &calls); err != nil {
		return nil, err
	}
	return stream.unique(calls), nil
}

func (stream *ToolCallStream) unique(calls []domain.ToolCall) []domain.ToolCall {
	result := make([]domain.ToolCall, 0, len(calls))
	for _, call := range calls {
		key := toolCorrelationKey(call) + ":" + string(call.Kind)
		if call.ID != "" && stream.emitted[key] {
			continue
		}
		// Bound deduplication and correlation state for long-lived connections.
		if len(stream.emitted) >= maxToolCalls {
			stream.emitted = make(map[string]bool)
			stream.parser.known = make(map[string]domain.ToolCall)
			stream.parser.knownBytes = 0
		}
		if call.ID != "" {
			stream.emitted[key] = true
		}
		stream.parser.remember(call)
		result = append(result, call)
	}
	return result
}

func (stream *ToolCallStream) flush(prefix string) ([]domain.ToolCall, error) {
	var calls []domain.ToolCall
	var failure error
	for _, key := range append([]string(nil), stream.order...) {
		if strings.HasPrefix(key, prefix) {
			completed, err := stream.finish(key)
			calls = append(calls, completed...)
			if err != nil {
				failure = err
			}
		}
	}
	return calls, failure
}

// FeedJSON handles a complete WebSocket frame or SSE data value.
func (stream *ToolCallStream) FeedJSON(payload []byte) (result []domain.ToolCall, failure error) {
	defer func() {
		if failure != nil {
			stream.failed = true
			stream.buffer, stream.data, stream.pending, stream.order = nil, nil, nil, nil
		}
	}()
	if stream.failed {
		return nil, nil
	}
	if len(payload) > int(stream.parser.maxPayloadBytes) || !json.Valid(payload) {
		return nil, stream.parser.invalid("")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		calls, err := stream.parser.Parse(payload)
		return stream.unique(calls), err
	}
	typeName := stringField(value, "type")
	switch typeName {
	case "content_block_start":
		block := object(value["content_block"])
		if block["type"] != "tool_use" {
			return nil, nil
		}
		call, err := stream.start("anthropic:"+toolID(value["index"]), domain.ToolProtocolAnthropic)
		if err != nil {
			return nil, err
		}
		return nil, stream.setMetadata(call, stringField(block, "id"), stringField(block, "name"), block["input"])
	case "content_block_delta":
		delta := object(value["delta"])
		if delta["type"] == "input_json_delta" {
			if call := stream.pending["anthropic:"+toolID(value["index"])]; call != nil {
				return nil, stream.appendArguments(call, stringField(delta, "partial_json"))
			}
		}
		return nil, nil
	case "content_block_stop":
		return stream.finish("anthropic:" + toolID(value["index"]))
	case "message_stop":
		return stream.flush("anthropic:")
	case "response.output_item.added":
		item := object(value["item"])
		if item["type"] != "function_call" {
			return nil, nil
		}
		call, err := stream.start("responses:"+stringField(item, "id"), domain.ToolProtocolOpenAI)
		if err != nil {
			return nil, err
		}
		if err := stream.setMetadata(call, stringField(item, "call_id"), stringField(item, "name"), nil); err != nil {
			return nil, err
		}
		return nil, stream.appendArguments(call, stringField(item, "arguments"))
	case "response.function_call_arguments.delta":
		call, err := stream.start("responses:"+stringField(value, "item_id"), domain.ToolProtocolOpenAI)
		if err != nil {
			return nil, err
		}
		return nil, stream.appendArguments(call, stringField(value, "delta"))
	case "response.function_call_arguments.done":
		key := "responses:" + stringField(value, "item_id")
		call, err := stream.start(key, domain.ToolProtocolOpenAI)
		if err != nil {
			return nil, err
		}
		name, id := call.name, call.id
		if part := stringField(value, "name"); part != "" {
			name = part
		}
		if part := stringField(value, "call_id"); part != "" {
			id = part
		}
		if err := stream.setMetadata(call, id, name, nil); err != nil {
			return nil, err
		}
		stream.size -= len(call.arguments)
		call.arguments = nil
		if err := stream.appendArguments(call, stringField(value, "arguments")); err != nil {
			return nil, err
		}
		return stream.finish(key)
	case "response.output_item.done":
		item := object(value["item"])
		key := "responses:" + stringField(item, "id")
		if call := stream.pending[key]; call != nil {
			stream.size -= len(call.arguments)
			call.arguments = nil
			if err := stream.setMetadata(call, stringField(item, "call_id"), stringField(item, "name"), nil); err != nil {
				return nil, err
			}
			if err := stream.appendArguments(call, stringField(item, "arguments")); err != nil {
				return nil, err
			}
			return stream.finish(key)
		}
		var calls []domain.ToolCall
		if err := stream.parser.item(item, &calls); err != nil {
			return nil, err
		}
		return stream.unique(calls), nil
	}
	var calls []domain.ToolCall
	if choices, ok := value["choices"].([]any); ok {
		for _, item := range choices {
			choice := object(item)
			prefix := "chat:" + stringField(value, "id") + ":" + toolID(choice["index"]) + ":"
			delta := object(choice["delta"])
			if entries, ok := delta["tool_calls"].([]any); ok {
				for _, entry := range entries {
					part := object(entry)
					call, err := stream.start(prefix+toolID(part["index"]), domain.ToolProtocolOpenAI)
					if err != nil {
						return nil, err
					}
					id := call.id
					if partID := stringField(part, "id"); partID != "" {
						id = partID
					}
					function := object(part["function"])
					if err := stream.setMetadata(call, id, call.name+stringField(function, "name"), nil); err != nil {
						return nil, err
					}
					if err := stream.appendArguments(call, stringField(function, "arguments")); err != nil {
						return nil, err
					}
				}
			}
			if choice["finish_reason"] != nil {
				completed, err := stream.flush(prefix)
				calls = append(calls, completed...)
				if err != nil {
					return calls, err
				}
			}
		}
	}
	parsed, err := stream.parser.Parse(payload)
	if err != nil {
		return calls, err
	}
	calls = append(calls, stream.unique(parsed)...)
	if typeName == "response.completed" || typeName == "response.done" {
		// Full terminal output is authoritative and may duplicate previous deltas.
		completed, err := stream.flush("responses:")
		calls = append(calls, completed...)
		return calls, err
	}
	return calls, nil
}

// WriteSSE tolerates arbitrary HTTP chunks, CRLF, multiline data and a final
// unterminated event. Oversized events stop parsing without changing forwarded bytes.
func (stream *ToolCallStream) WriteSSE(chunk []byte, final bool) ([]domain.ToolCall, error) {
	if stream.failed {
		return nil, nil
	}
	stream.buffer = append(stream.buffer, chunk...)
	var calls []domain.ToolCall
	var failure error
	for {
		end := bytes.IndexByte(stream.buffer, '\n')
		if end < 0 {
			if !final || len(stream.buffer) == 0 {
				break
			}
			end = len(stream.buffer)
		}
		line := bytes.TrimSuffix(stream.buffer[:end], []byte("\r"))
		if end == len(stream.buffer) {
			stream.buffer = nil
		} else {
			stream.buffer = stream.buffer[end+1:]
		}
		if len(line) == 0 {
			completed, err := stream.block()
			calls = append(calls, completed...)
			if err != nil {
				failure = err
			}
		} else if bytes.HasPrefix(line, []byte("data:")) {
			if len(stream.data) > 0 {
				stream.data = append(stream.data, '\n')
			}
			stream.data = append(stream.data, bytes.TrimPrefix(line[5:], []byte(" "))...)
		} else if bytes.HasPrefix(line, []byte("event:")) {
			stream.event = strings.TrimSpace(string(line[6:]))
		}
		if len(stream.data)+len(stream.buffer) > int(stream.parser.maxPayloadBytes) {
			stream.failed = true
			stream.buffer, stream.data, stream.pending, stream.order = nil, nil, nil, nil
			return calls, stream.parser.invalid("")
		}
	}
	if len(stream.data)+len(stream.buffer) > int(stream.parser.maxPayloadBytes) {
		stream.failed = true
		stream.buffer, stream.data, stream.pending, stream.order = nil, nil, nil, nil
		return calls, stream.parser.invalid("")
	}
	if final {
		completed, err := stream.block()
		calls = append(calls, completed...)
		if err != nil {
			failure = err
		}
		completed, err = stream.flush("")
		calls = append(calls, completed...)
		if err != nil {
			failure = err
		}
	}
	return calls, failure
}

func (stream *ToolCallStream) block() ([]domain.ToolCall, error) {
	payload := stream.data
	stream.data = nil
	event := stream.event
	stream.event = ""
	if len(payload) == 0 {
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		return stream.flush("")
	}
	if event != "" {
		var value map[string]json.RawMessage
		if json.Unmarshal(payload, &value) == nil && value != nil && len(value["type"]) == 0 {
			value["type"], _ = json.Marshal(event)
			payload, _ = json.Marshal(value)
		}
	}
	return stream.FeedJSON(payload)
}
