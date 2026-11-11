package interfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
	"bosapi/backend/internal/platform/errorcode"
)

func (a *app) codexChat(w http.ResponseWriter, r *http.Request, p provider) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestBodyLimit(r)))
	if err != nil {
		crossFailure(w, r, "chat", 400, errorcode.InvalidRequest, &gatewayinfra.ConversionError{Field: "body", Reason: "request body could not be read within the request limit"})
		return
	}
	q, err := gatewayinfra.ParseRelayRequest(raw, "chat", "openai_responses")
	if err != nil {
		crossFailure(w, r, "chat", 400, errorcode.UnsupportedConversion, err)
		return
	}
	public := q.Model
	if model := requestModel(r); model != "" && model != public {
		crossFailure(w, r, "chat", 400, errorcode.UnsupportedConversion, &gatewayinfra.ConversionError{Field: "model", Reason: "body model must match the authorized request model"})
		return
	}
	if mapped := p.ModelMapping[q.Model]; mapped != "" {
		q.Model = mapped
	}
	payload, err := q.CodexUpstream()
	if err != nil {
		crossFailure(w, r, "chat", 400, errorcode.UnsupportedConversion, err)
		return
	}
	// The OAuth backend always streams, even for a non-streaming caller. Bound
	// response headers rather than cancelling a healthy long-running SSE turn.
	client := *a.client
	timeout := client.Timeout
	if timeout > 0 {
		client.Timeout = 0
	}
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(context.Canceled)
	var timer *time.Timer
	var expired chan struct{}
	if timeout > 0 {
		expired = make(chan struct{})
		timer = time.AfterFunc(timeout, func() { cancel(context.DeadlineExceeded); close(expired) })
	}
	res, err := a.upstreamRequestWithClient(ctx, p, "/responses", payload, &client)
	if timer != nil && !timer.Stop() {
		<-expired
		if res != nil {
			res.Body.Close()
		}
		err = context.DeadlineExceeded
	}
	if err != nil {
		fail(w, upstreamErrorStatus(r.Context(), err), upstreamErrorCode(r.Context(), err))
		return
	}
	defer res.Body.Close()
	if res.StatusCode == 429 {
		writeUpstreamLimitError(w, res, time.Now())
		return
	}
	if res.StatusCode != 200 {
		fail(w, upstreamStatus(res.StatusCode), upstreamCode(res.StatusCode))
		return
	}
	if !strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		crossFailure(w, r, "chat", 502, errorcode.UnsupportedUpstreamFeature, responseFieldError("content-type", "expected an SSE stream"))
		return
	}
	if state := usageState(r); state != nil {
		state.streaming = q.Stream
	}
	a.convertCodexChat(w, r, res.Body, q, public)
}

func codexReply(root map[string]any, fallback string) (gatewayinfra.RelayReply, error) {
	reply := gatewayinfra.RelayReply{ID: fallback, Stop: "stop"}
	if id, ok := root["id"].(string); ok && id != "" {
		reply.ID = id
	}
	if status, ok := root["status"].(string); ok && status != "completed" {
		details, _ := root["incomplete_details"].(map[string]any)
		if status != "incomplete" || details["reason"] != "max_output_tokens" {
			return reply, responseFieldError("status", "response did not complete")
		}
		reply.Stop = "length"
	}
	output, ok := root["output"].([]any)
	if !ok {
		return reply, responseFieldError("output", "expected output items")
	}
	calls := []any{}
	for _, entry := range output {
		item, ok := entry.(map[string]any)
		if !ok {
			return reply, responseFieldError("output", "invalid output item")
		}
		switch item["type"] {
		case "message":
			content, ok := item["content"].([]any)
			if !ok {
				return reply, responseFieldError("output.content", "expected content array")
			}
			for _, c := range content {
				part, ok := c.(map[string]any)
				if !ok {
					return reply, responseFieldError("output.content", "invalid content")
				}
				if part["type"] != "output_text" {
					return reply, responseFieldError("output.content.type", "unsupported response content")
				}
				text, ok := part["text"].(string)
				if !ok {
					return reply, responseFieldError("output.content.text", "expected text")
				}
				if annotations, exists := part["annotations"]; exists && annotations != nil {
					if values, ok := annotations.([]any); !ok || len(values) > 0 {
						return reply, responseFieldError("output.content.annotations", "annotations cannot be represented")
					}
				}
				reply.Blocks = append(reply.Blocks, gatewayinfra.RelayBlock{Type: "text", Text: text})
			}
		case "function_call":
			id, _ := item["call_id"].(string)
			name, _ := item["name"].(string)
			args, _ := item["arguments"].(string)
			calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}})
		default:
			return reply, responseFieldError("output.type", "output item cannot be represented in Chat")
		}
	}
	blocks, err := gatewayinfra.ChatRelayToolCalls(calls)
	if err != nil {
		return reply, err
	}
	reply.Blocks = append(reply.Blocks, blocks...)
	if len(blocks) > 0 && reply.Stop != "length" {
		reply.Stop = "tool_calls"
	}
	if usage, ok := root["usage"].(map[string]any); ok {
		number := func(name string) (int64, error) {
			n, ok := usage[name].(json.Number)
			if !ok {
				return 0, errors.New("invalid usage")
			}
			v, e := n.Int64()
			if e != nil || v < 0 {
				return 0, errors.New("invalid usage")
			}
			return v, nil
		}
		in, e1 := number("input_tokens")
		out, e2 := number("output_tokens")
		if e1 != nil || e2 != nil || in > int64(^uint64(0)>>1)-out {
			return reply, responseFieldError("usage", "invalid token counts")
		}
		reply.Input = in
		reply.Output = out
		reply.HasUsage = true
		if details, ok := usage["input_tokens_details"].(map[string]any); ok && details["cached_tokens"] != nil {
			n, ok := details["cached_tokens"].(json.Number)
			if !ok {
				return reply, responseFieldError("usage.cached_tokens", "invalid cached token count")
			}
			cached, e := n.Int64()
			if e != nil || cached < 0 || cached > in {
				return reply, responseFieldError("usage.cached_tokens", "invalid cached token count")
			}
			reply.CacheRead = cached
			reply.Input -= cached
		}
	}
	return reply, nil
}

func (a *app) convertCodexChat(w http.ResponseWriter, r *http.Request, body io.Reader, q gatewayinfra.RelayRequest, model string) {
	id := "relay_" + random(16)
	var renderer *crossStreamRenderer
	var writer *toolBodyWriter
	if q.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(200)
		writer = &toolBodyWriter{app: a, request: r, writer: w, filter: a.toolDeliveryFilter(r), scanner: securityAuditScanner{app: a, request: r}, isSSE: true, inspectTools: true}
		renderer = &crossStreamRenderer{source: "chat", model: model, q: q, reply: gatewayinfra.RelayReply{ID: id}, writer: writer}
	}
	var text strings.Builder
	var reply gatewayinfra.RelayReply
	completedItems := map[int64]any{}
	terminal := false
	failConversion := func(err error) {
		recordConversionFailure(r, errorcode.UnsupportedUpstreamFeature, err)
		if state := usageState(r); state != nil {
			state.streamFailure = errorcode.UnsupportedUpstreamFeature
		}
		if q.Stream {
			writeToolChunks(w, [][]byte{sseJSON("", conversionEnvelope("chat", errorcode.UnsupportedUpstreamFeature, err))})
		} else {
			respond(w, 502, conversionEnvelope("chat", errorcode.UnsupportedUpstreamFeature, err))
		}
	}
	handle := func(raw string) error {
		if raw == "[DONE]" {
			return nil
		}
		event, err := gatewayinfra.DecodeProtocolJSON([]byte(raw))
		if err != nil {
			return responseFieldError("stream", "invalid SSE JSON")
		}
		kind, _ := event["type"].(string)
		switch kind {
		case "response.metadata":
			// Codex may send auxiliary moderation metadata between text events.
			// It carries no Chat content or completion/usage semantics. Keep
			// requiring response.completed and rejecting unknown event types.
			return nil
		case "response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.content_part.done", "response.output_item.done", "response.output_text.done", "response.function_call_arguments.delta", "response.function_call_arguments.done":
			if kind == "response.output_item.done" {
				index, ok := event["output_index"].(json.Number)
				if !ok {
					return responseFieldError("output_index", "missing output index")
				}
				n, err := index.Int64()
				if err != nil || n < 0 || n >= 1024 || completedItems[n] != nil || event["item"] == nil {
					return responseFieldError("output_index", "invalid or duplicate output item")
				}
				completedItems[n] = event["item"]
			}
			if kind == "response.created" && renderer != nil && !renderer.started {
				if response, ok := event["response"].(map[string]any); ok {
					if value, ok := response["id"].(string); ok && value != "" {
						renderer.reply.ID = value
					}
				}
			}
			return nil
		case "response.output_text.delta":
			if renderer != nil && !renderer.started {
				if responseID, ok := event["response_id"].(string); ok && responseID != "" {
					renderer.reply.ID = responseID
				}
			}
			delta, ok := event["delta"].(string)
			if !ok || text.Len()+len(delta) > 8<<20 {
				return responseFieldError("output_text.delta", "invalid or excessive text")
			}
			text.WriteString(delta)
			if renderer != nil {
				if err := renderer.start(); err != nil {
					return err
				}
				return renderer.text(delta)
			}
			return nil
		case "response.completed", "response.incomplete":
			response, ok := event["response"].(map[string]any)
			if !ok {
				return responseFieldError("response", "missing completed response")
			}
			if kind == "response.incomplete" {
				details, _ := response["incomplete_details"].(map[string]any)
				if response["status"] != "incomplete" || details["reason"] != "max_output_tokens" {
					return responseFieldError("stream", "upstream response failed or was incomplete")
				}
			}
			if output, ok := response["output"].([]any); ok && len(output) == 0 && len(completedItems) > 0 {
				indexes := make([]int, 0, len(completedItems))
				for index := range completedItems {
					indexes = append(indexes, int(index))
				}
				sort.Ints(indexes)
				for _, index := range indexes {
					output = append(output, completedItems[int64(index)])
				}
				response["output"] = output
			}
			reply, err = codexReply(response, id)
			if err != nil {
				return err
			}
			var complete strings.Builder
			for _, block := range reply.Blocks {
				if block.Type == "text" {
					complete.WriteString(block.Text)
				}
			}
			if text.Len() > 0 && text.String() != complete.String() {
				return responseFieldError("output_text", "stream and completed response differ")
			}
			observeUsageValue(usageState(r), response, kind)
			terminal = true
			return nil
		case "response.failed", "error":
			return responseFieldError("stream", "upstream response failed or was incomplete")
		default:
			return responseFieldError("stream.type", "unsupported Responses event")
		}
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	data := []string{}
	size := 0
	dispatch := func() error {
		if len(data) == 0 {
			return nil
		}
		raw := strings.Join(data, "\n")
		data = nil
		size = 0
		return handle(raw)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				failConversion(err)
				return
			}
			if terminal {
				break
			}
			continue
		}
		size += len(line)
		if size > 1<<20 {
			failConversion(responseFieldError("stream", "SSE frame exceeds limit"))
			return
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		failConversion(responseFieldError("stream", "upstream stream interrupted"))
		return
	}
	if !terminal {
		if err := dispatch(); err != nil {
			failConversion(err)
			return
		}
	}
	if !terminal {
		failConversion(responseFieldError("stream", "missing completed event"))
		return
	}
	if !q.Stream {
		converted, err := reply.JSON("chat", model)
		if err != nil {
			failConversion(err)
			return
		}
		a.writeToolJSON(w, r, 200, converted)
		return
	}
	if q.IncludeUsage && !reply.HasUsage {
		failConversion(responseFieldError("usage", "upstream did not provide required usage metrics"))
		return
	}
	// The first emitted chunk fixes the stream ID for all later chunks.
	if renderer.started {
		reply.ID = renderer.reply.ID
	}
	renderer.reply = reply
	if text.Len() == 0 {
		for _, block := range reply.Blocks {
			if block.Type == "text" {
				if err := renderer.start(); err != nil {
					return
				}
				if err := renderer.text(block.Text); err != nil {
					return
				}
			}
		}
	}
	for _, block := range reply.Blocks {
		if block.Type == "tool_use" {
			index, err := renderer.toolStart(block)
			if err != nil {
				failConversion(err)
				return
			}
			args, _ := json.Marshal(block.Input)
			if err = renderer.toolArguments(index, string(args)); err != nil {
				failConversion(err)
				return
			}
			if err = renderer.toolStop(index); err != nil {
				failConversion(err)
				return
			}
		}
	}
	if err := renderer.finish(); err != nil {
		failConversion(err)
		return
	}
	writer.finish(io.EOF)
}
