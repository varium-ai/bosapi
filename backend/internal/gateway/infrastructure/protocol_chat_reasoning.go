package infrastructure

import "strings"

const chatThinkingPrefix = "bos-chat-reasoning-v1:"

// ChatRelayReasoning preserves explicit Chat reasoning without mixing it into
// user-facing answer text or executable tool arguments.
func ChatRelayReasoning(message map[string]any) ([]RelayBlock, error) {
	blocks := []RelayBlock{}
	for _, field := range []string{"reasoning", "reasoning_content"} {
		value := message[field]
		if value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, conversionError("response.message."+field, "expected reasoning text")
		}
		if text == "" {
			continue
		}
		if len(blocks) > 0 {
			return nil, conversionError("response.message.reasoning", "multiple reasoning fields cannot be preserved unambiguously")
		}
		blocks = append(blocks, RelayBlock{Type: "thinking", Text: text, ReasoningField: field})
	}
	return blocks, nil
}

// ThinkingSignature identifies the Chat wire field for a later tool-history turn.
// This marker carries no authentication claim and is not an Anthropic signature.
func (block RelayBlock) ThinkingSignature() string {
	return chatThinkingPrefix + block.ReasoningField
}

func parseChatThinking(value map[string]any, path string) (RelayBlock, error) {
	block := RelayBlock{Type: "thinking"}
	if err := fieldsOnly(value, path, "type", "thinking", "signature"); err != nil {
		return block, err
	}
	text, ok := value["thinking"].(string)
	if !ok {
		return block, conversionError(path+"thinking", "expected reasoning text")
	}
	signature, _ := value["signature"].(string)
	field := strings.TrimPrefix(signature, chatThinkingPrefix)
	if !strings.HasPrefix(signature, chatThinkingPrefix) || field != "reasoning" && field != "reasoning_content" {
		return block, conversionError(path+"signature", "only bos chat reasoning history can be replayed to chat")
	}
	block.Text, block.ReasoningField = text, field
	return block, nil
}
