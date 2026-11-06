package infrastructure

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strconv"
	"strings"

	"bosapi/backend/internal/gateway/domain"
)

type deliveryFrame struct {
	payload []byte
	prefix  string
	sse     bool
}

// ToolDeliveryFilter withholds executable tool bytes until complete arguments have a verdict.
// Normal text before a tool streams immediately. The remainder of that turn is bounded
// in memory so protocol indices and terminal duplicate output can be repaired together.
type ToolDeliveryFilter struct {
	observeOnly  bool
	caller       domain.ToolCallContext
	known        []domain.ToolCall
	stream       *ToolCallStream
	inspect      func(domain.ToolCall) bool
	failure      func(error)
	frames       []deliveryFrame
	bytes        int
	buffer       []byte
	seen         map[[32]byte]bool
	blocked      map[string]bool
	refs         map[string]string
	indices      map[string]map[int]bool
	errors       map[string]bool
	errorIndices map[string]int
	nextChoice   int
	holding      bool
	unsafe       bool
}

// SetObserveOnly keeps bounded inspection and audit callbacks while forwarding
// original bytes immediately, even when tools cannot be parsed or authorized.
func (filter *ToolDeliveryFilter) SetObserveOnly(observe bool) { filter.observeOnly = observe }

func (filter *ToolDeliveryFilter) observe(calls []domain.ToolCall, err error) {
	if err != nil {
		filter.report(err)
	}
	filter.inspectCalls(calls)
}

// NewToolDeliveryFilter keeps inspection callbacks independent of transport and persistence.
func NewToolDeliveryFilter(caller domain.ToolCallContext, known []domain.ToolCall, inspect func(domain.ToolCall) bool, failure func(error)) *ToolDeliveryFilter {
	filter := &ToolDeliveryFilter{caller: caller, known: known, inspect: inspect, failure: failure}
	filter.reset()
	return filter
}

func (filter *ToolDeliveryFilter) reset() {
	if filter.stream != nil {
		filter.known = nil
		for _, call := range filter.stream.parser.known {
			filter.known = append(filter.known, call)
		}
	}
	filter.stream = NewToolCallStream(filter.caller, filter.known)
	filter.blocked, filter.refs = make(map[string]bool), make(map[string]string)
	filter.seen = make(map[[32]byte]bool)
	filter.indices = make(map[string]map[int]bool)
	filter.errors, filter.errorIndices, filter.nextChoice = make(map[string]bool), make(map[string]int), 0
	filter.frames, filter.bytes, filter.holding, filter.unsafe = nil, 0, false, false
}

// Remember preserves bounded invocation correlation across WebSocket response turns.
// The connection owner must serialize this with Frame; parameters stay in memory.
func (filter *ToolDeliveryFilter) Remember(calls []domain.ToolCall) {
	filter.stream.Remember(calls)
}

func (filter *ToolDeliveryFilter) report(err error) {
	if !filter.unsafe && filter.failure != nil {
		filter.failure(err)
	}
	filter.unsafe = true
}

func (filter *ToolDeliveryFilter) inspectCalls(calls []domain.ToolCall) {
	for _, call := range calls {
		digest := sha256.Sum256(toolJSON([]any{call.Protocol, call.ID, call.IDIsNumber, call.Name, call.Kind, call.Arguments, call.Result}))
		if filter.seen[digest] {
			continue
		}
		if len(filter.seen) >= 256 {
			filter.report(filter.stream.parser.invalid(call.Protocol))
			return
		}
		filter.seen[digest] = true
		if !filter.inspect(call) && call.Kind == domain.ToolCallInvocation {
			filter.blocked[deliveryCallKey(call)] = true
		}
	}
}

// JSON checks a complete response before any invocation is delivered.
func (filter *ToolDeliveryFilter) JSON(payload []byte) []byte {
	calls, err := filter.stream.parser.Parse(payload)
	if err != nil {
		filter.report(err)
	}
	filter.inspectCalls(calls)
	if filter.observeOnly {
		return payload
	}
	return filter.rewrite(payload)
}

// Frame returns zero or more safe WebSocket messages while keeping the connection open.
func (filter *ToolDeliveryFilter) Frame(payload []byte) [][]byte {
	if filter.observeOnly {
		value, valid := deliveryObject(payload)
		filter.observeFrame(payload, value)
		if valid && terminalDelivery(value) {
			filter.reset()
		}
		return [][]byte{payload}
	}
	return filter.feed(deliveryFrame{payload: append([]byte(nil), payload...)})
}

func (filter *ToolDeliveryFilter) feed(frame deliveryFrame) [][]byte {
	value, valid := deliveryObject(frame.payload)
	if (valid && value["jsonrpc"] == "2.0") || (bytes.HasPrefix(bytes.TrimSpace(frame.payload), []byte("[")) && !bytes.Equal(bytes.TrimSpace(frame.payload), []byte("[DONE]"))) {
		// Internal RPC messages are complete independent exchanges, not model
		// argument streams. Preserve correlation but clear per-exchange verdicts.
		payload := filter.JSON(frame.payload)
		filter.reset()
		if len(payload) == 0 {
			return nil
		}
		return [][]byte{filter.encode(frame, payload)}
	}
	if !valid {
		if bytes.Equal(bytes.TrimSpace(frame.payload), []byte("[DONE]")) {
			return filter.finish(frame)
		}
		filter.report(filter.stream.parser.invalid(""))
		// SSE comments/keepalives have no JSON payload and are handled by SSE.
		return nil
	}
	// Pings carry no executable content. Deliver them even while a tool's
	// incomplete arguments are held for validation, so idle detection stays live.
	if frame.sse && value["type"] == "ping" {
		return [][]byte{filter.encode(frame, frame.payload)}
	}
	filter.observeFrame(frame.payload, value)
	tool := hasDeliveryTools(value)
	if tool {
		filter.holding = true
	}
	if !filter.holding {
		return [][]byte{filter.encode(frame, filter.rewrite(frame.payload))}
	}
	if filter.bytes+len(frame.payload)+len(frame.prefix) > maxToolPayload || len(filter.frames) >= 4096 {
		filter.report(filter.stream.parser.invalid(""))
		// Release buffered text while dropping all unverified calls, then retain no payload.
		out := filter.flush()
		filtered := filter.rewrite(frame.payload)
		if len(filtered) > 0 {
			out = append(out, filter.encode(frame, filtered))
		}
		if terminalDelivery(value) {
			filter.reset()
		}
		return out
	}
	filter.frames = append(filter.frames, frame)
	filter.bytes += len(frame.payload) + len(frame.prefix)
	// Gemini events contain complete function calls; release after verdict.
	if value["candidates"] != nil && len(filter.stream.pending) == 0 {
		out := filter.flush()
		filter.holding = false
		return out
	}
	if value["type"] == "content_block_stop" && len(filter.stream.pending) == 0 {
		out := filter.flush()
		filter.holding = false
		return out
	}
	if terminalDelivery(value) && len(filter.stream.pending) == 0 {
		out := filter.flush()
		filter.reset()
		return out
	}
	return nil
}

func (filter *ToolDeliveryFilter) observeFrame(payload []byte, value map[string]any) {
	filter.track(value)
	calls, err := filter.stream.FeedJSON(payload)
	if err != nil {
		filter.report(err)
	}
	filter.inspectCalls(calls)
	// Recheck authoritative final output even if the stream parser deduplicated its ID.
	finalCalls, finalErr := filter.stream.parser.Parse(payload)
	if value["type"] == "response.output_item.done" {
		finalErr = filter.stream.parser.item(object(value["item"]), &finalCalls)
	}
	if finalErr != nil {
		filter.report(finalErr)
	}
	filter.inspectCalls(finalCalls)
}

func (filter *ToolDeliveryFilter) finish(frame deliveryFrame) [][]byte {
	calls, err := filter.stream.flush("")
	if err != nil {
		filter.report(err)
	}
	filter.inspectCalls(calls)
	out := filter.flush()
	if len(frame.payload) > 0 {
		out = append(out, filter.encode(frame, frame.payload))
	}
	filter.reset()
	return out
}

// Finish drains a complete HTTP stream; incomplete calls are never released unchecked.
func (filter *ToolDeliveryFilter) Finish() [][]byte {
	if filter.observeOnly {
		calls, err := filter.stream.flush("")
		filter.observe(calls, err)
		filter.reset()
		return nil
	}
	return filter.finish(deliveryFrame{})
}

func (filter *ToolDeliveryFilter) flush() [][]byte {
	filter.removedIndices()
	var output [][]byte
	for _, frame := range filter.frames {
		payload := filter.rewrite(frame.payload)
		if len(payload) > 0 {
			output = append(output, filter.encode(frame, payload))
		}
	}
	filter.frames, filter.bytes = nil, 0
	return output
}

func (filter *ToolDeliveryFilter) encode(frame deliveryFrame, payload []byte) []byte {
	if len(payload) == 0 {
		return nil
	}
	if !frame.sse {
		return payload
	}
	return []byte(frame.prefix + "data: " + string(payload) + "\n\n")
}

// SSE parses complete events instead of scanning already-forwarded byte chunks.
func (filter *ToolDeliveryFilter) SSE(chunk []byte, final bool) [][]byte {
	if filter.observeOnly {
		calls, err := filter.stream.WriteSSE(chunk, final)
		filter.observe(calls, err)
		if final {
			filter.reset()
		}
		if len(chunk) == 0 {
			return nil
		}
		return [][]byte{chunk}
	}
	filter.buffer = append(filter.buffer, chunk...)
	var output [][]byte
	for {
		end, separator := bytes.Index(filter.buffer, []byte("\n\n")), 2
		if crlf := bytes.Index(filter.buffer, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
			end, separator = crlf, 4
		}
		if end < 0 {
			break
		}
		block := append([]byte(nil), filter.buffer[:end]...)
		filter.buffer = filter.buffer[end+separator:]
		output = append(output, filter.sseBlock(block)...)
	}
	if len(filter.buffer) > maxToolPayload {
		filter.buffer = nil
		filter.report(filter.stream.parser.invalid(""))
	}
	if final {
		if len(filter.buffer) > 0 {
			output = append(output, filter.sseBlock(filter.buffer)...)
			filter.buffer = nil
		}
		output = append(output, filter.Finish()...)
	}
	return output
}

func (filter *ToolDeliveryFilter) sseBlock(block []byte) [][]byte {
	var data []string
	var prefix, event string
	for _, line := range strings.Split(strings.ReplaceAll(string(block), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		} else {
			prefix += line + "\n"
			if strings.HasPrefix(line, "event:") {
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
		}
	}
	if len(data) == 0 {
		return [][]byte{append(append([]byte(nil), block...), '\n', '\n')}
	}
	payload := []byte(strings.Join(data, "\n"))
	if event != "" && json.Valid(payload) {
		value, valid := deliveryObject(payload)
		if valid && value["type"] == nil {
			value["type"] = event
			payload = toolJSON(value)
		}
	}
	return filter.feed(deliveryFrame{payload: payload, prefix: prefix, sse: true})
}

func terminalDelivery(value map[string]any) bool {
	switch value["type"] {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "message_stop":
		return true
	}
	if choices, ok := value["choices"].([]any); ok {
		for _, entry := range choices {
			if object(entry)["finish_reason"] != nil {
				return true
			}
		}
	}
	return false
}

func hasDeliveryTools(value map[string]any) bool {
	if candidates, ok := value["candidates"].([]any); ok {
		for _, candidate := range candidates {
			parts, _ := object(object(candidate)["content"])["parts"].([]any)
			for _, part := range parts {
				if object(part)["functionCall"] != nil {
					return true
				}
			}
		}
	}
	typeName := stringField(value, "type")
	if strings.Contains(typeName, "function_call_arguments") {
		return true
	}
	if object(value["item"])["type"] == "function_call" || object(value["content_block"])["type"] == "tool_use" || object(value["delta"])["type"] == "input_json_delta" {
		return true
	}
	if value["type"] == "function_call" || value["type"] == "tool_use" {
		return true
	}
	if calls, ok := value["tool_calls"].([]any); ok && len(calls) > 0 {
		return true
	}
	for _, field := range []string{"output", "content", "choices"} {
		if list, ok := value[field].([]any); ok {
			for _, entry := range list {
				if hasDeliveryTools(object(entry)) {
					return true
				}
			}
		}
	}
	for _, field := range []string{"response", "message", "delta"} {
		if nested := object(value[field]); nested != nil && hasDeliveryTools(nested) {
			return true
		}
	}
	return false
}

func deliveryIndex(value any) int {
	number, ok := value.(json.Number)
	if !ok {
		return 0
	}
	index, err := strconv.Atoi(number.String())
	if err != nil {
		return 0
	}
	return index
}

func deliveryObject(payload []byte) (map[string]any, bool) {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || !json.Valid(payload) {
		return nil, false
	}
	return value, value != nil
}

func (filter *ToolDeliveryFilter) track(value map[string]any) {
	if len(filter.refs) >= 512 {
		filter.report(filter.stream.parser.invalid(""))
		return
	}
	item := object(value["item"])
	if item["type"] == "function_call" {
		filter.refs["responses:"+stringField(item, "id")] = "openai:" + responseCallID(item)
		filter.refs["output:"+toolIndex(value["output_index"])] = "openai:" + responseCallID(item)
	}
	block := object(value["content_block"])
	if block["type"] == "tool_use" {
		filter.refs["anthropic:"+toolIndex(value["index"])] = "anthropic:" + stringField(block, "id")
	}
	if choices, ok := value["choices"].([]any); ok {
		for _, entry := range choices {
			choice := object(entry)
			if index := deliveryIndex(choice["index"]); index >= filter.nextChoice {
				filter.nextChoice = index + 1
			}
			if calls, ok := object(choice["delta"])["tool_calls"].([]any); ok {
				for _, entry := range calls {
					call := object(entry)
					if id := stringField(call, "id"); id != "" {
						filter.refs["chat:"+toolIndex(choice["index"])+":"+toolIndex(call["index"])] = "openai:" + id
					}
				}
			}
		}
	}
}

func toolIndex(value any) string { encoded := toolJSON(value); return string(encoded) }

func deliveryCallKey(call domain.ToolCall) string {
	if call.Protocol == domain.ToolProtocolMCP {
		return toolCorrelationKey(call)
	}
	return string(call.Protocol) + ":" + call.ID
}

func (filter *ToolDeliveryFilter) denied(key string) bool {
	return filter.unsafe || key == "" || filter.blocked[key]
}

func (filter *ToolDeliveryFilter) removedIndices() {
	for ref, identity := range filter.refs {
		parts := strings.Split(ref, ":")
		// A rejected, well-formed tool occupies its original result slot.
		// Only unverifiable calls are removed outright.
		if !filter.denied(identity) || (parts[0] != "chat" && !filter.unsafe && filter.blocked[identity]) {
			continue
		}
		if parts[0] == "responses" {
			continue
		}
		group := parts[0]
		if group == "chat" {
			group += ":" + parts[1]
		}
		var index int
		if json.Unmarshal([]byte(parts[len(parts)-1]), &index) != nil {
			continue
		}
		if filter.indices[group] == nil {
			filter.indices[group] = make(map[int]bool)
		}
		filter.indices[group][index] = true
	}
}

func (filter *ToolDeliveryFilter) reindex(group string, index int) int {
	shift := 0
	for removed := range filter.indices[group] {
		if removed < index {
			shift++
		}
	}
	return index - shift
}

func (filter *ToolDeliveryFilter) rewrite(payload []byte) []byte {
	if len(filter.blocked) == 0 && !filter.unsafe {
		return payload
	}
	value, valid := deliveryObject(payload)
	if !valid {
		// Internal JSON-RPC batches have no model response envelope.
		var batch []map[string]any
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		if decoder.Decode(&batch) == nil && batch != nil && json.Valid(payload) {
			var output []any
			for _, item := range batch {
				if rewritten := filter.rewrite(toolJSON(item)); len(rewritten) > 0 {
					var result any
					decoder := json.NewDecoder(bytes.NewReader(rewritten))
					decoder.UseNumber()
					_ = decoder.Decode(&result)
					output = append(output, result)
				}
			}
			if len(output) > 0 {
				return toolJSON(output)
			}
		}
		return nil
	}
	if value["jsonrpc"] == "2.0" && value["method"] == "tools/call" {
		_, numeric := value["id"].(json.Number)
		key := deliveryCallKey(domain.ToolCall{Protocol: domain.ToolProtocolMCP, ID: toolID(value["id"]), IDIsNumber: numeric})
		if filter.denied(key) {
			if _, exists := value["id"]; !exists { // JSON-RPC notifications have no reply.
				return nil
			}
			return toolJSON(mcpToolError(value["id"]))
		}
	}
	if value["type"] == "function_call" && filter.denied("openai:"+responseCallID(value)) {
		if filter.unsafe {
			return nil
		}
		return toolJSON(responsesToolError(responseCallID(value)))
	}
	if value["type"] == "tool_use" && filter.denied("anthropic:"+stringField(value, "id")) {
		if filter.unsafe {
			return nil
		}
		return toolJSON(anthropicToolError(stringField(value, "id")))
	}
	if !filter.rewriteEvent(value) {
		return nil
	}
	filter.rewriteEnvelope(value)
	return toolJSON(value)
}

func (filter *ToolDeliveryFilter) rewriteEvent(value map[string]any) bool {
	typeName := stringField(value, "type")
	if strings.HasPrefix(typeName, "response.function_call_arguments.") {
		if filter.denied(filter.refs["responses:"+stringField(value, "item_id")]) {
			return false
		}
	}
	if item := object(value["item"]); item["type"] == "function_call" && filter.denied("openai:"+responseCallID(item)) {
		if filter.unsafe {
			return false
		}
		result := responsesToolError(responseCallID(item))
		if item["id"] != nil {
			result["id"] = item["id"]
		}
		value["item"] = result
	}
	if strings.HasPrefix(typeName, "content_block_") {
		key := "anthropic:" + toolIndex(value["index"])
		if identity, exists := filter.refs[key]; exists && filter.denied(identity) {
			if filter.unsafe {
				return false
			}
			if typeName == "content_block_delta" {
				return false
			}
			if typeName == "content_block_start" {
				value["content_block"] = anthropicToolError(strings.TrimPrefix(identity, "anthropic:"))
			}
		}
		if typeName == "content_block_delta" && object(value["delta"])["type"] == "input_json_delta" && filter.denied(filter.refs[key]) {
			return false
		}
		value["index"] = filter.reindex("anthropic", deliveryIndex(value["index"]))
	}
	if index, exists := value["output_index"]; exists {
		value["output_index"] = filter.reindex("output", deliveryIndex(index))
	}
	if choices, ok := value["choices"].([]any); ok {
		var results []any
		for _, entry := range choices {
			results = append(results, filter.rewriteChatDelta(object(entry))...)
		}
		value["choices"] = append(choices, results...)
	}
	if typeName == "message_delta" && object(value["delta"])["stop_reason"] == "tool_use" && !filter.hasAllowedRef("anthropic:") {
		object(value["delta"])["stop_reason"] = "end_turn"
	}
	return true
}

func (filter *ToolDeliveryFilter) rewriteChatDelta(choice map[string]any) []any {
	var results []any
	delta := object(choice["delta"])
	if calls, ok := delta["tool_calls"].([]any); ok {
		kept := make([]any, 0, len(calls))
		group := "chat:" + toolIndex(choice["index"])
		for _, entry := range calls {
			call := object(entry)
			identity := filter.refs[group+":"+toolIndex(call["index"])]
			if filter.denied(identity) {
				if !filter.unsafe && !filter.errors[identity] {
					filter.errors[identity] = true
					index := filter.nextChoice
					filter.nextChoice++
					filter.errorIndices[identity] = index
					results = append(results, map[string]any{"index": index, "delta": openAIToolError(strings.TrimPrefix(identity, "openai:")), "finish_reason": nil})
				}
				continue
			}
			call["index"] = filter.reindex(group, deliveryIndex(call["index"]))
			kept = append(kept, call)
		}
		if len(kept) == 0 {
			delete(delta, "tool_calls")
		} else {
			delta["tool_calls"] = kept
		}
	}
	if delta != nil && choice["finish_reason"] == "tool_calls" && !filter.hasAllowedRef("chat:"+toolIndex(choice["index"])+":") {
		choice["finish_reason"] = "stop"
	}
	if choice["finish_reason"] != nil {
		// Each synthetic tool message has a normal terminal chunk.
		for ref, identity := range filter.refs {
			if strings.HasPrefix(ref, "chat:"+toolIndex(choice["index"])+":") {
				if index, exists := filter.errorIndices[identity]; exists {
					results = append(results, map[string]any{"index": index, "delta": map[string]any{}, "finish_reason": "stop"})
					delete(filter.errorIndices, identity)
				}
			}
		}
	}
	return results
}

func (filter *ToolDeliveryFilter) hasAllowedRef(prefix string) bool {
	for ref, identity := range filter.refs {
		if strings.HasPrefix(ref, prefix) && !filter.denied(identity) {
			return true
		}
	}
	return false
}

func (filter *ToolDeliveryFilter) rewriteEnvelope(value map[string]any) {
	if candidates, ok := value["candidates"].([]any); ok {
		for _, candidate := range candidates {
			content := object(object(candidate)["content"])
			parts, ok := content["parts"].([]any)
			if !ok {
				continue
			}
			kept := make([]any, 0, len(parts))
			for _, entry := range parts {
				part := object(entry)
				if raw, exists := part["functionCall"]; exists && filter.denied("gemini:"+geminiCallID(object(raw))) {
					kept = append(kept, map[string]any{"text": "Tool invocation blocked by gateway policy."})
					continue
				}
				kept = append(kept, entry)
			}
			content["parts"] = kept
		}
	}

	for _, field := range []string{"messages", "input", "output", "content"} {
		items, ok := value[field].([]any)
		if !ok {
			continue
		}
		value[field] = filter.rewriteItems(items)
	}
	if calls, ok := value["tool_calls"].([]any); ok {
		kept := make([]any, 0, len(calls))
		for _, entry := range calls {
			if !filter.denied("openai:" + stringField(object(entry), "id")) {
				kept = append(kept, entry)
			}
		}
		if len(kept) == 0 {
			delete(value, "tool_calls")
		} else {
			value["tool_calls"] = kept
		}
	}
	if choices, ok := value["choices"].([]any); ok {
		var results []any
		next := 0
		for _, entry := range choices {
			if index := deliveryIndex(object(entry)["index"]); index >= next {
				next = index + 1
			}
		}
		for _, entry := range choices {
			choice := object(entry)
			message := object(choice["message"])
			if calls, ok := message["tool_calls"].([]any); ok && !filter.unsafe {
				for _, entry := range calls {
					id := stringField(object(entry), "id")
					if filter.blocked["openai:"+id] {
						results = append(results, map[string]any{"index": next, "message": openAIToolError(id), "finish_reason": "stop"})
						next++
					}
				}
			}
			filter.rewriteEnvelope(message)
			if choice["finish_reason"] == "tool_calls" && message != nil && message["tool_calls"] == nil {
				choice["finish_reason"] = "stop"
			}
		}
		value["choices"] = append(choices, results...)
	}
	if response := object(value["response"]); response != nil {
		filter.rewriteEnvelope(response)
	}
	if value["stop_reason"] == "tool_use" {
		found := false
		if content, ok := value["content"].([]any); ok {
			for _, item := range content {
				if object(item)["type"] == "tool_use" {
					found = true
				}
			}
		}
		if !found {
			value["stop_reason"] = "end_turn"
		}
	}
}

func (filter *ToolDeliveryFilter) rewriteItems(items []any) []any {
	kept := make([]any, 0, len(items))
	for _, entry := range items {
		item := object(entry)
		protocol, id := "", ""
		if item["type"] == "function_call" {
			protocol, id = "openai:", responseCallID(item)
		}
		if item["type"] == "tool_use" {
			protocol, id = "anthropic:", stringField(item, "id")
		}
		if protocol != "" && filter.denied(protocol+id) {
			if !filter.unsafe {
				if protocol == "openai:" {
					result := responsesToolError(id)
					if item["id"] != nil {
						result["id"] = item["id"]
					}
					kept = append(kept, result)
				} else {
					kept = append(kept, anthropicToolError(id))
				}
			}
			continue
		}
		filter.rewriteEnvelope(item)
		kept = append(kept, entry)
	}
	return kept
}
