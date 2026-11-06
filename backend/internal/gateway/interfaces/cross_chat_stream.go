package interfaces

import (
	"encoding/json"
	"sort"
	"strings"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

type chatStreamCall struct {
	id, name, arguments strings.Builder
}

type chatStreamRelay struct {
	renderer *crossStreamRenderer
	calls    map[int]*chatStreamCall
	bytes    int
	finished bool
}

func (stream *chatStreamRelay) handle(root map[string]any) error {
	if root["error"] != nil {
		return responseFieldError("error", "upstream stream failed")
	}
	if !stream.renderer.started {
		if id, ok := root["id"].(string); ok && id != "" {
			stream.renderer.reply.ID = id
		}
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) > 1 {
		return responseFieldError("choices", "expected at most one Chat choice")
	}
	if len(choices) == 0 {
		if root["usage"] == nil {
			return responseFieldError("choices", "empty choice requires usage")
		}
		return nil
	}
	if stream.finished {
		return responseFieldError("stream", "content after finish reason")
	}
	choice, ok := choices[0].(map[string]any)
	if !ok || choice["index"] != json.Number("0") {
		return responseFieldError("choices.index", "expected choice zero")
	}
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return responseFieldError("delta", "expected a Chat delta")
	}
	if err := stream.delta(delta); err != nil {
		return err
	}
	if reason := choice["finish_reason"]; reason != nil {
		text, ok := reason.(string)
		if !ok || text == "" {
			return responseFieldError("finish_reason", "expected a finish reason")
		}
		return stream.finish(text)
	}
	return nil
}

func (stream *chatStreamRelay) delta(delta map[string]any) error {
	if err := gatewayinfra.ChatRelayMessage(delta); err != nil {
		return err
	}
	if err := stream.renderer.start(); err != nil {
		return err
	}
	thoughts, err := gatewayinfra.ChatRelayReasoning(delta)
	if err != nil {
		return err
	}
	for _, block := range thoughts {
		if err := stream.renderer.thinking(block); err != nil {
			return err
		}
	}
	if content := delta["content"]; content != nil {
		text, ok := content.(string)
		if !ok {
			return responseFieldError("delta.content", "expected text")
		}
		if err := stream.renderer.text(text); err != nil {
			return err
		}
	}
	if value := delta["tool_calls"]; value != nil {
		calls, ok := value.([]any)
		if !ok || len(calls) > 128 {
			return responseFieldError("delta.tool_calls", "expected at most 128 function deltas")
		}
		for _, value := range calls {
			if err := stream.callDelta(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (stream *chatStreamRelay) callDelta(value any) error {
	call, ok := value.(map[string]any)
	if !ok {
		return responseFieldError("delta.tool_calls", "expected a function delta")
	}
	for key := range call {
		if key != "index" && key != "id" && key != "type" && key != "function" {
			return responseFieldError("delta.tool_calls", "unsupported function delta field")
		}
	}
	number, ok := call["index"].(json.Number)
	if !ok {
		return responseFieldError("delta.tool_calls.index", "expected a tool index")
	}
	index, err := number.Int64()
	if err != nil || index < 0 || index >= 128 {
		return responseFieldError("delta.tool_calls.index", "invalid tool index")
	}
	if kind := call["type"]; kind != nil && kind != "function" {
		return responseFieldError("delta.tool_calls.type", "only function tools are supported")
	}
	entry := stream.calls[int(index)]
	if entry == nil {
		entry = &chatStreamCall{}
		stream.calls[int(index)] = entry
	}
	if err := stream.appendField(&entry.id, call, "id"); err != nil {
		return err
	}
	return stream.functionDelta(entry, call)
}

func (stream *chatStreamRelay) functionDelta(entry *chatStreamCall, call map[string]any) error {
	if call["function"] == nil {
		return nil
	}
	function, ok := call["function"].(map[string]any)
	if !ok {
		return responseFieldError("delta.tool_calls.function", "expected a function object")
	}
	for key := range function {
		if key != "name" && key != "arguments" {
			return responseFieldError("delta.tool_calls.function", "unsupported function field")
		}
	}
	if err := stream.appendField(&entry.name, function, "name"); err != nil {
		return err
	}
	return stream.appendField(&entry.arguments, function, "arguments")
}

func (stream *chatStreamRelay) appendField(dst *strings.Builder, value map[string]any, key string) error {
	part, exists := value[key]
	if !exists || part == nil {
		return nil
	}
	text, ok := part.(string)
	if !ok {
		return responseFieldError("delta.tool_calls."+key, "expected a string fragment")
	}
	stream.bytes += len(text)
	if stream.bytes > 8<<20 {
		return responseFieldError("delta.tool_calls", "tool deltas exceed conversion limit")
	}
	dst.WriteString(text)
	return nil
}

func (stream *chatStreamRelay) finish(reason string) error {
	stop, err := gatewayinfra.ChatRelayStop(reason, len(stream.calls) > 0)
	if err != nil {
		return err
	}
	// Validate all parallel calls before releasing any executable content.
	blocks, err := stream.toolBlocks()
	if err != nil {
		return err
	}
	for _, block := range blocks {
		index, err := stream.renderer.toolStart(block)
		if err != nil {
			return err
		}
		args, err := json.Marshal(block.Input)
		if err != nil {
			return err
		}
		if err := stream.renderer.toolArguments(index, string(args)); err != nil {
			return err
		}
		if err := stream.renderer.toolStop(index); err != nil {
			return err
		}
	}
	stream.renderer.reply.Stop = stop
	stream.finished = true
	return nil
}

func (stream *chatStreamRelay) toolBlocks() ([]gatewayinfra.RelayBlock, error) {
	indexes := make([]int, 0, len(stream.calls))
	for index := range stream.calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	calls := []any{}
	for _, index := range indexes {
		entry := stream.calls[index]
		calls = append(calls, map[string]any{"id": entry.id.String(), "type": "function", "function": map[string]any{"name": entry.name.String(), "arguments": entry.arguments.String()}})
	}
	return gatewayinfra.ChatRelayToolCalls(calls)
}
