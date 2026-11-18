package interfaces

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

type responsesChatOutput struct {
	namespaces map[string]gatewayinfra.ResponsesToolName
	root       map[string]any
	items      []map[string]any
	sequence   int
	openKind   string
	value      strings.Builder
}

func newResponsesChatOutput(model string) *responsesChatOutput {
	return &responsesChatOutput{
		root: map[string]any{"id": chatResponsePrefix + random(16), "object": "response", "created_at": time.Now().Unix(), "model": model, "status": "in_progress", "error": nil, "incomplete_details": nil, "output": []any{}, "usage": nil},
	}
}

func (output *responsesChatOutput) addItem(kind string, fields map[string]any) (int, map[string]any) {
	index := len(output.items)
	fields["type"] = kind
	switch kind {
	case "message":
		fields["role"], fields["status"] = "assistant", "in_progress"
		fields["id"] = fmt.Sprintf("msg_%s_%d", output.root["id"], index)
	case "reasoning":
		fields["id"] = fmt.Sprintf("rs_%s_%d", output.root["id"], index)
	case "function_call":
		if name, ok := fields["name"].(string); ok {
			if original, exists := output.namespaces[name]; exists {
				fields["name"], fields["namespace"] = original.Name, original.Namespace
			}
		}
		fields["status"] = "in_progress"
		fields["id"] = fmt.Sprintf("fc_%s_%d", output.root["id"], index)
	}
	output.items = append(output.items, fields)
	return index, fields
}

func (output *responsesChatOutput) completed(reply gatewayinfra.RelayReply) string {
	event := "response.completed"
	output.root["status"] = "completed"
	switch reply.Stop {
	case "length", "content_filter":
		reason := "max_output_tokens"
		if reply.Stop == "content_filter" {
			reason = "content_filter"
		}
		output.root["status"] = "incomplete"
		output.root["incomplete_details"] = map[string]any{"reason": reason}
		event = "response.incomplete"
	}
	if output.items != nil {
		output.root["output"] = output.items
	}
	if reply.HasUsage {
		input := reply.Input + reply.CacheRead + reply.CacheWrite
		output.root["usage"] = map[string]any{
			"input_tokens": input, "output_tokens": reply.Output, "total_tokens": input + reply.Output,
			"input_tokens_details":  map[string]any{"cached_tokens": reply.CacheRead},
			"output_tokens_details": map[string]any{"reasoning_tokens": reply.Thoughts},
		}
	}
	return event
}

func responsesTextPart(text string) map[string]any {
	return map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}
}

func responsesSummaryPart(text string) map[string]any {
	return map[string]any{"type": "summary_text", "text": text}
}

func (a *app) writeResponsesChatJSON(w http.ResponseWriter, r *http.Request, model string, reply gatewayinfra.RelayReply, namespaces map[string]gatewayinfra.ResponsesToolName, save func([]byte) error) {
	output := newResponsesChatOutput(model)
	output.namespaces = namespaces
	for _, block := range reply.Blocks {
		switch block.Type {
		case "text":
			output.addItem("message", map[string]any{"content": []any{responsesTextPart(block.Text)}})
		case "thinking":
			output.addItem("reasoning", map[string]any{"summary": []any{responsesSummaryPart(block.Text)}})
		case "tool_use":
			arguments, _ := json.Marshal(block.Input)
			output.addItem("function_call", map[string]any{"call_id": block.ID, "name": block.Name, "arguments": string(arguments)})
		}
	}
	for _, item := range output.items {
		if item["status"] != nil {
			item["status"] = "completed"
			if item["type"] == "message" && (reply.Stop == "length" || reply.Stop == "content_filter") {
				item["status"] = "incomplete"
			}
		}
	}
	output.completed(reply)
	raw, _ := json.Marshal(output.root)
	a.writeToolJSONWithSave(w, r, http.StatusOK, raw, save)
}

func (stream *crossStreamRenderer) responsesEvent(event string, fields map[string]any) error {
	fields["type"] = event
	fields["sequence_number"] = stream.responses.sequence
	stream.responses.sequence++
	return stream.emit(event, fields)
}

func (stream *crossStreamRenderer) responsesPart(kind, text string) error {
	output := stream.responses
	if output.openKind != kind {
		if err := stream.closeResponsesPart("completed"); err != nil {
			return err
		}
		fields := map[string]any{"content": []any{}}
		if kind == "reasoning" {
			fields = map[string]any{"summary": []any{}}
		}
		index, item := output.addItem(kind, fields)
		if err := stream.responsesEvent("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
		output.openKind = kind
		event, indexField, part := "response.content_part.added", "content_index", responsesTextPart("")
		if kind == "reasoning" {
			event, indexField, part = "response.reasoning_summary_part.added", "summary_index", responsesSummaryPart("")
		}
		if err := stream.responsesEvent(event, map[string]any{"output_index": index, "item_id": item["id"], indexField: 0, "part": part}); err != nil {
			return err
		}
	}
	stream.textBytes += len(text)
	if stream.textBytes > 2<<20 {
		return responseFieldError("stream", "response text exceeds conversion limit")
	}
	output.value.WriteString(text)
	index := len(output.items) - 1
	event, indexField := "response.output_text.delta", "content_index"
	if kind == "reasoning" {
		event, indexField = "response.reasoning_summary_text.delta", "summary_index"
	}
	fields := map[string]any{"output_index": index, "item_id": output.items[index]["id"], indexField: 0, "delta": text}
	if kind == "message" {
		fields["logprobs"] = []any{}
	}
	return stream.responsesEvent(event, fields)
}

func (stream *crossStreamRenderer) closeResponsesPart(status string) error {
	output := stream.responses
	kind := output.openKind
	if kind == "" {
		return nil
	}
	index := len(output.items) - 1
	item := output.items[index]
	text := output.value.String()
	output.value.Reset()
	output.openKind = ""
	textEvent, partEvent, indexField := "response.output_text.done", "response.content_part.done", "content_index"
	part := responsesTextPart(text)
	if kind == "reasoning" {
		textEvent, partEvent, indexField = "response.reasoning_summary_text.done", "response.reasoning_summary_part.done", "summary_index"
		part = responsesSummaryPart(text)
		item["summary"] = []any{part}
	} else {
		item["content"], item["status"] = []any{part}, status
	}
	fields := map[string]any{"output_index": index, "item_id": item["id"], indexField: 0, "text": text}
	if kind == "message" {
		fields["logprobs"] = []any{}
	}
	if err := stream.responsesEvent(textEvent, fields); err != nil {
		return err
	}
	if err := stream.responsesEvent(partEvent, map[string]any{"output_index": index, "item_id": item["id"], indexField: 0, "part": part}); err != nil {
		return err
	}
	return stream.responsesEvent("response.output_item.done", map[string]any{"output_index": index, "item": item})
}
