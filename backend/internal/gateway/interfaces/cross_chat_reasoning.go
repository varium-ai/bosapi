package interfaces

import gatewayinfra "bosapi/backend/internal/gateway/infrastructure"

func (stream *crossStreamRenderer) thinking(block gatewayinfra.RelayBlock) error {
	if err := stream.stopText(); err != nil {
		return err
	}
	if stream.source != "messages" {
		return responseFieldError("reasoning", "thinking rendering requires a messages client")
	}
	if stream.thinkingOpen && stream.thinkingField != block.ReasoningField {
		return responseFieldError("reasoning", "reasoning format changed during the stream")
	}
	if !stream.thinkingOpen {
		stream.thinkingOpen = true
		stream.thinkingIndex = stream.nextBlock
		stream.nextBlock++
		stream.thinkingField = block.ReasoningField
		if err := stream.emit("content_block_start", map[string]any{"type": "content_block_start", "index": stream.thinkingIndex, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}}); err != nil {
			return err
		}
	}
	return stream.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": stream.thinkingIndex, "delta": map[string]any{"type": "thinking_delta", "thinking": block.Text}})
}

func (stream *crossStreamRenderer) stopThinking() error {
	if !stream.thinkingOpen {
		return nil
	}
	block := gatewayinfra.RelayBlock{ReasoningField: stream.thinkingField}
	if err := stream.emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": stream.thinkingIndex, "delta": map[string]any{"type": "signature_delta", "signature": block.ThinkingSignature()}}); err != nil {
		return err
	}
	stream.thinkingOpen = false
	return stream.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": stream.thinkingIndex})
}
