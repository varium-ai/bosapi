package interfaces

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
)

type crossStreamBlock struct {
	kind                   string
	tool                   int
	hasArguments           bool
	initialArguments       bool
	arguments              []byte
	thinking               gatewayinfra.RelayBlock
	thinkingText           strings.Builder
	thinkingSignature      strings.Builder
	textOffset, toolOffset int
}

type upstreamStreamFailure struct{ kind string }

func (failure *upstreamStreamFailure) Error() string {
	return "upstream emitted an SSE error event: " + failure.kind
}

func newUpstreamStreamFailure(root map[string]any) error {
	details, _ := root["error"].(map[string]any)
	kind, _ := details["type"].(string)
	switch kind {
	case "overloaded_error", "api_error", "rate_limit_error", "authentication_error", "permission_error", "invalid_request_error":
	default:
		kind = "unknown"
	}
	return &upstreamStreamFailure{kind: kind}
}

type crossStreamRenderer struct {
	source, model       string
	q                   gatewayinfra.RelayRequest
	reply               gatewayinfra.RelayReply
	writer              *toolBodyWriter
	started             bool
	nextBlock, nextTool int
	thinkingOpen        bool
	thinkingIndex       int
	thinkingField       string
	textOpen            bool
	textIndex           int
	textBytes           int
}

func (stream *crossStreamRenderer) emit(event string, value any) error {
	payload := sseJSON(event, value)
	if len(payload) > 8<<20 {
		return responseFieldError("stream", "converted event exceeds delivery limit")
	}
	if !stream.writer.write(payload) {
		return io.ErrClosedPipe
	}
	return nil
}
func (stream *crossStreamRenderer) chat(delta map[string]any, finish any) error {
	return stream.emit("", map[string]any{"id": stream.reply.ID, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": stream.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
}
func (stream *crossStreamRenderer) start() error {
	if stream.started {
		return nil
	}
	stream.started = true
	if stream.source == "chat" {
		return stream.chat(map[string]any{"role": "assistant", "content": ""}, nil)
	}
	return stream.emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": stream.reply.ID, "type": "message", "role": "assistant", "model": stream.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": stream.reply.MessagesUsage()}})
}
func (stream *crossStreamRenderer) text(text string) error {
	if text == "" {
		return nil
	}
	if err := stream.stopThinking(); err != nil {
		return err
	}
	if stream.source == "chat" {
		stream.textBytes += len(text)
		return stream.chat(map[string]any{"content": text}, nil)
	}
	if !stream.textOpen {
		stream.textOpen = true
		stream.textIndex = stream.nextBlock
		stream.nextBlock++
		if err := stream.emit("content_block_start", map[string]any{"type": "content_block_start", "index": stream.textIndex, "content_block": map[string]any{"type": "text", "text": ""}}); err != nil {
			return err
		}
	}
	return stream.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": stream.textIndex, "delta": map[string]any{"type": "text_delta", "text": text}})
}

func (stream *crossStreamRenderer) stopText() error {
	if !stream.textOpen {
		return nil
	}
	stream.textOpen = false
	return stream.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": stream.textIndex})
}

func (stream *crossStreamRenderer) toolStart(block gatewayinfra.RelayBlock) (int, error) {
	if err := stream.stopText(); err != nil {
		return 0, err
	}
	if err := stream.stopThinking(); err != nil {
		return 0, err
	}
	if stream.nextTool >= 128 {
		return 0, responseFieldError("tools", "too many tool invocations")
	}
	index := stream.nextTool
	stream.nextTool++
	if stream.source == "chat" {
		return index, stream.chat(map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": block.ID, "type": "function", "function": map[string]any{"name": block.Name, "arguments": ""}}}}, nil)
	}
	index = stream.nextBlock
	stream.nextBlock++
	return index, stream.emit("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": block.ID, "name": block.Name, "input": map[string]any{}}})
}
func (stream *crossStreamRenderer) toolArguments(index int, args string) error {
	if stream.source == "chat" {
		return stream.chat(map[string]any{"tool_calls": []any{map[string]any{"index": index, "function": map[string]any{"arguments": args}}}}, nil)
	}
	return stream.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": args}})
}
func (stream *crossStreamRenderer) toolStop(index int) error {
	if stream.source == "chat" {
		return nil
	}
	return stream.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}
func (stream *crossStreamRenderer) finish() error {
	if (stream.source == "messages" || stream.q.IncludeUsage) && !stream.reply.HasUsage {
		return responseFieldError("usage", "upstream did not provide required usage metrics")
	}
	if err := stream.stopText(); err != nil {
		return err
	}
	if err := stream.stopThinking(); err != nil {
		return err
	}
	if err := stream.start(); err != nil {
		return err
	}
	if stream.source == "messages" {
		if err := stream.emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stream.reply.MessagesStop(), "stop_sequence": nil}, "usage": stream.reply.MessagesUsage()}); err != nil {
			return err
		}
		return stream.emit("message_stop", map[string]any{"type": "message_stop"})
	}
	if err := stream.chat(map[string]any{}, stream.reply.Stop); err != nil {
		return err
	}
	if stream.q.IncludeUsage && stream.reply.HasUsage {
		if err := stream.emit("", map[string]any{"id": stream.reply.ID, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": stream.model, "choices": []any{}, "usage": stream.reply.ChatUsage()}); err != nil {
			return err
		}
	}
	if !stream.writer.write([]byte("data: [DONE]\n\n")) {
		return io.ErrClosedPipe
	}
	return nil
}
func responseFieldError(field, reason string) error {
	return &gatewayinfra.ConversionError{Field: "response." + field, Reason: reason}
}

func (a *app) crossProtocolStream(w http.ResponseWriter, r *http.Request, body io.Reader, q gatewayinfra.RelayRequest, target, source, model, id string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	writer := &toolBodyWriter{app: a, request: r, writer: w, filter: a.toolDeliveryFilter(r), scanner: securityAuditScanner{app: a, request: r}, isSSE: true, inspectTools: true}
	upstreamUsage := &usageMetricsState{}
	// Billing is based solely on upstream metrics, never synthesized protocol
	// start events or an optional client-facing usage chunk.
	defer func() {
		if state := usageState(r); state != nil {
			state.metrics = upstreamUsage.metrics
		}
	}()
	renderer := &crossStreamRenderer{source: source, model: model, q: q, reply: gatewayinfra.RelayReply{ID: id}, writer: writer}
	chat := &chatStreamRelay{renderer: renderer, calls: map[int]*chatStreamCall{}}
	blocks := map[int]*crossStreamBlock{}
	closed := map[int]bool{}
	seenCalls := map[string]bool{}
	terminal, started := false, false
	argumentBytes := 0
	thinkingBytes := 0
	frameIndex := 0
	emitFailure := func(code errorcode.Code, err error) {
		if state := usageState(r); state != nil {
			state.streamFailure = code
		}
		// Do not flush held executable tool fragments on an incomplete/error turn.
		envelope := conversionEnvelope(source, code, err)
		event := ""
		if source == "messages" {
			event = "error"
		}
		if !writeToolChunks(w, [][]byte{sseJSON(event, envelope)}) {
			recordGatewayFailure(r, "response_write", errorcode.DeliveryInterrupted, "response.body", "downstream response write failed")
		}
	}
	failure := func(err error) {
		if errors.Is(err, io.ErrClosedPipe) {
			recordGatewayFailure(r, "response_write", errorcode.DeliveryInterrupted, "response.body", "downstream response write failed")
			return
		}
		var upstreamFailure *upstreamStreamFailure
		if errors.As(err, &upstreamFailure) {
			recordGatewayFailure(r, "upstream_stream", errorcode.UpstreamError, "response.error", upstreamFailure.Error())
			emitFailure(errorcode.UpstreamError, responseFieldError("error", upstreamFailure.Error()))
			return
		}
		recordConversionFailure(r, errorcode.UnsupportedUpstreamFeature, err)
		emitFailure(errorcode.UnsupportedUpstreamFeature, err)
	}
	handle := func(payload []byte, event string) error {
		if (target == "openai_chat" || target == "") && string(payload) == "[DONE]" {
			if !chat.finished {
				return responseFieldError("stream", "missing Chat finish reason")
			}
			if err := renderer.finish(); err != nil {
				return err
			}
			terminal = true
			return nil
		}
		root, err := gatewayinfra.DecodeProtocolJSON(payload)
		if err != nil {
			return responseFieldError("stream", "invalid SSE JSON")
		}
		streamEvent := event
		if target == "openai_chat" || target == "" {
			streamEvent = "chat.chunk"
		} else if target == "gemini" {
			streamEvent = "gemini.chunk"
		}
		setGatewayStreamDiagnostic(r.Context(), frameIndex, streamEvent, root)
		if terminal {
			return responseFieldError("stream", "events after terminal message")
		}
		if root["error"] != nil {
			return newUpstreamStreamFailure(root)
		}
		if target == "openai_chat" || target == "" {
			if err := gatewayinfra.ValidateChatRelayUsage(root); err != nil {
				return err
			}
		}
		renderer.reply.ObserveUsage(root, target)
		observeUsageValue(upstreamUsage, root, "cross_protocol_usage")
		if target == "openai_chat" || target == "" {
			return chat.handle(root)
		}
		if target == "gemini" {
			parts, reason, err := gatewayinfra.GeminiRelayParts(root, fmt.Sprintf("%s_%d", id, frameIndex))
			if renderer.reply.Stop != "" && len(parts) > 0 {
				return responseFieldError("stream", "content after finish reason")
			}
			if err != nil {
				return err
			}
			if !renderer.started {
				if responseID, ok := root["responseId"].(string); ok && responseID != "" {
					renderer.reply.ID = responseID
				}
			}
			if err := renderer.start(); err != nil {
				return err
			}
			for _, block := range parts {
				if block.Type == "text" {
					if err := renderer.text(block.Text); err != nil {
						return err
					}
					continue
				}
				if seenCalls[block.ID] {
					return responseFieldError("functionCall.id", "duplicate/partial tool invocations cannot be preserved")
				}
				seenCalls[block.ID] = true
				args, _ := json.Marshal(block.Input)
				argumentBytes += len(args)
				if argumentBytes > 8<<20 {
					return responseFieldError("functionCall.args", "tool arguments exceed conversion limit")
				}
				index, err := renderer.toolStart(block)
				if err != nil {
					return err
				}
				if err := renderer.toolArguments(index, string(args)); err != nil {
					return err
				}
				if err := renderer.toolStop(index); err != nil {
					return err
				}
			}
			if reason != "" {
				renderer.reply.Stop, err = geminiStop(reason, len(seenCalls) > 0)
				return err
			}
			return nil
		}
		kind, _ := root["type"].(string)
		if kind == "" {
			kind = event
		}
		switch kind {
		case "ping":
			if source == "messages" {
				return renderer.emit("ping", map[string]any{"type": "ping"})
			}
			if !writeToolChunks(w, [][]byte{[]byte(": ping\n\n")}) {
				return io.ErrClosedPipe
			}
			return nil
		case "message_start":
			if started {
				return responseFieldError("message_start", "duplicate start")
			}
			started = true
			message, ok := root["message"].(map[string]any)
			if !ok {
				return responseFieldError("message_start", "missing message")
			}
			content, ok := message["content"].([]any)
			if !ok || len(content) > 0 {
				return responseFieldError("message_start.content", "expected empty streamed content")
			}
			if messageID, ok := message["id"].(string); ok && messageID != "" {
				renderer.reply.ID = messageID
			}
			return renderer.start()
		case "message_delta":
			delta, _ := root["delta"].(map[string]any)
			if reason, ok := delta["stop_reason"].(string); ok && reason != "" {
				renderer.reply.Stop, err = anthropicStop(reason)
				return err
			}
			return nil
		case "message_stop":
			if !started || len(blocks) > 0 || renderer.reply.Stop == "" {
				return responseFieldError("message_stop", "incomplete message or tool block")
			}
			terminal = true
			return renderer.finish()
		case "error":
			return newUpstreamStreamFailure(root)
		case "content_block_start", "content_block_delta", "content_block_stop":
			if !started {
				return responseFieldError("stream", "content before message_start")
			}
			value, ok := root["index"].(json.Number)
			if !ok {
				return responseFieldError("index", "invalid block index")
			}
			number, err := value.Int64()
			if err != nil || number < 0 || number > 1024 {
				return responseFieldError("index", "invalid block index")
			}
			index := int(number)
			if kind == "content_block_start" {
				for _, open := range blocks {
					if open.kind == "thinking" || open.kind == "redacted_thinking" {
						return responseFieldError("content_block", "thinking block must finish before the next block")
					}
				}
				if blocks[index] != nil || closed[index] {
					return responseFieldError("index", "duplicate block index")
				}
				block, ok := root["content_block"].(map[string]any)
				if !ok {
					return responseFieldError("content_block", "missing block")
				}
				if err := gatewayinfra.ValidateAnthropicRelayBlockMetadata(block, "response.content_block."); err != nil {
					return err
				}
				typeName, _ := block["type"].(string)
				entry := &crossStreamBlock{kind: typeName}
				blocks[index] = entry
				switch typeName {
				case "thinking", "redacted_thinking":
					entry.thinking = gatewayinfra.RelayBlock{Type: typeName, ReasoningField: "reasoning_content"}
					entry.textOffset, entry.toolOffset = renderer.textBytes, renderer.nextTool
					text, _ := block["thinking"].(string)
					signature, _ := block["signature"].(string)
					entry.thinking.Data, _ = block["data"].(string)
					thinkingBytes += len(text) + len(signature) + len(entry.thinking.Data)
					if thinkingBytes > 8<<20 {
						return responseFieldError("thinking", "thinking history exceeds conversion limit")
					}
					entry.thinkingText.WriteString(text)
					entry.thinkingSignature.WriteString(signature)
					if text != "" {
						return renderer.chat(map[string]any{"reasoning_content": text}, nil)
					}
					return nil
				case "text":
					text, ok := block["text"].(string)
					if !ok {
						return responseFieldError("content_block.text", "expected text")
					}
					return renderer.text(text)
				case "tool_use":
					name, _ := block["name"].(string)
					callID, _ := block["id"].(string)
					input, ok := block["input"].(map[string]any)
					if name == "" || callID == "" || !ok || seenCalls[callID] {
						return responseFieldError("content_block", "invalid invocation")
					}
					seenCalls[callID] = true
					entry.tool, err = renderer.toolStart(gatewayinfra.RelayBlock{ID: callID, Name: name})
					if err != nil {
						return err
					}
					if len(input) > 0 {
						args, _ := json.Marshal(input)
						entry.hasArguments = true
						entry.initialArguments = true
						entry.arguments = args
						argumentBytes += len(args)
						if argumentBytes > 8<<20 {
							return responseFieldError("tool_use.input", "tool arguments exceed conversion limit")
						}
						return renderer.toolArguments(entry.tool, string(args))
					}
					return nil
				default:
					return responseFieldError("content_block.type", "media and server tools cannot be preserved")
				}
			}
			entry := blocks[index]
			if entry == nil {
				return responseFieldError("index", "delta/stop without a start")
			}
			if kind == "content_block_stop" {
				delete(blocks, index)
				closed[index] = true
				if entry.kind == "thinking" || entry.kind == "redacted_thinking" {
					entry.thinking.Text = entry.thinkingText.String()
					entry.thinking.Signature = entry.thinkingSignature.String()
					if _, err := gatewayinfra.ParseAnthropicThinking(entry.thinking.AnthropicThinking(), "response.content_block."); err != nil {
						return err
					}
					return renderer.chat(map[string]any{"anthropic_thinking": []any{entry.thinking.ChatThinking(entry.textOffset, entry.toolOffset)}}, nil)
				}
				if entry.kind == "tool_use" {
					if entry.hasArguments {
						if _, err := gatewayinfra.DecodeProtocolJSON(entry.arguments); err != nil {
							return responseFieldError("tool_use.input", "incomplete or invalid JSON tool arguments")
						}
					}
					if !entry.hasArguments {
						if err := renderer.toolArguments(entry.tool, "{}"); err != nil {
							return err
						}
					}
					return renderer.toolStop(entry.tool)
				}
				return nil
			}
			delta, ok := root["delta"].(map[string]any)
			if !ok {
				return responseFieldError("delta", "missing delta")
			}
			if err := gatewayinfra.ValidateAnthropicRelayDeltaFields(delta, "response.delta."); err != nil {
				return err
			}
			if entry.kind == "thinking" {
				field, _ := delta["type"].(string)
				switch field {
				case "thinking_delta":
					text, ok := delta["thinking"].(string)
					if !ok || entry.thinkingSignature.Len() > 0 {
						return responseFieldError("delta.thinking", "expected thinking text before its signature")
					}
					thinkingBytes += len(text)
					if thinkingBytes > 8<<20 {
						return responseFieldError("thinking", "thinking history exceeds conversion limit")
					}
					entry.thinkingText.WriteString(text)
					return renderer.chat(map[string]any{"reasoning_content": text}, nil)
				case "signature_delta":
					signature, ok := delta["signature"].(string)
					if !ok {
						return responseFieldError("delta.signature", "expected a signature string")
					}
					thinkingBytes += len(signature)
					if thinkingBytes > 8<<20 {
						return responseFieldError("thinking", "thinking history exceeds conversion limit")
					}
					entry.thinkingSignature.WriteString(signature)
					return nil
				}
			}
			if entry.kind == "text" && delta["type"] == "text_delta" {
				text, ok := delta["text"].(string)
				if !ok {
					return responseFieldError("delta.text", "expected text")
				}
				return renderer.text(text)
			}
			if entry.kind == "tool_use" && delta["type"] == "input_json_delta" {
				if entry.initialArguments {
					return responseFieldError("delta.partial_json", "argument deltas cannot follow complete initial input")
				}
				args, ok := delta["partial_json"].(string)
				if !ok {
					return responseFieldError("delta.partial_json", "expected text")
				}
				entry.hasArguments = true
				argumentBytes += len(args)
				if argumentBytes > 8<<20 {
					return responseFieldError("tool_use.input", "tool arguments exceed conversion limit")
				}
				entry.arguments = append(entry.arguments, args...)
				return renderer.toolArguments(entry.tool, args)
			}
			return responseFieldError("delta.type", "unsupported content delta")
		default:
			return responseFieldError("type", "unsupported upstream event")
		}
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	data := []string{}
	event := ""
	size := 0
	dispatch := func() error {
		if len(data) == 0 {
			size = 0
			event = ""
			return nil
		}
		frameIndex++
		setGatewayStreamDiagnostic(r.Context(), frameIndex, event, nil)
		payload := []byte(strings.Join(data, "\n"))
		data = nil
		size = 0
		err := handle(payload, event)
		event = ""
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				failure(err)
				return
			}
			if terminal {
				writer.finish(io.EOF)
				return
			}
			continue
		}
		size += len(line)
		if size > 1<<20 {
			setGatewayStreamDiagnostic(r.Context(), frameIndex+1, event, nil)
			failure(responseFieldError("stream", "SSE frame exceeds limit"))
			return
		}
		if strings.HasPrefix(line, ":") {
			if !writeToolChunks(w, [][]byte{[]byte(line + "\n\n")}) {
				recordGatewayFailure(r, "response_write", errorcode.DeliveryInterrupted, "response.body", "downstream response write failed")
				return
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		setGatewayStreamDiagnostic(r.Context(), frameIndex+1, "read", nil)
		if errors.Is(err, bufio.ErrTooLong) {
			failure(responseFieldError("stream", "SSE frame exceeds limit"))
			return
		}
		code := recordResponseReadFailure(r, "response.stream", err)
		emitFailure(code, responseFieldError("stream", "upstream stream interrupted"))
		return
	}
	if err := dispatch(); err != nil {
		failure(err)
		return
	}
	if target == "gemini" {
		if renderer.reply.Stop == "" {
			failure(responseFieldError("stream", "missing finish reason"))
			return
		}
		if err := renderer.finish(); err != nil {
			failure(err)
			return
		}
	} else if !terminal {
		failure(responseFieldError("stream", "missing terminal event"))
		return
	}
	writer.finish(io.EOF)
}
func anthropicStop(reason string) (string, error) {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop", nil
	case "max_tokens":
		return "length", nil
	case "tool_use":
		return "tool_calls", nil
	case "refusal":
		return "content_filter", nil
	}
	return "", responseFieldError("stop_reason", "unsupported finish reason")
}
func geminiStop(reason string, tools bool) (string, error) {
	switch reason {
	case "STOP":
		if tools {
			return "tool_calls", nil
		}
		return "stop", nil
	case "MAX_TOKENS":
		return "length", nil
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return "content_filter", nil
	}
	return "", responseFieldError("finishReason", "unsupported finish reason")
}
