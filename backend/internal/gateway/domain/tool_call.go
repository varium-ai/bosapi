package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"unicode"
)

type ToolProtocol string

const (
	ToolProtocolOpenAI    ToolProtocol = "openai"
	ToolProtocolAnthropic ToolProtocol = "anthropic"
	ToolProtocolMCP       ToolProtocol = "mcp"
	ToolProtocolGemini    ToolProtocol = "gemini"
)

type ToolDirection string

const (
	ToolDirectionRequest  ToolDirection = "request"
	ToolDirectionResponse ToolDirection = "response"
)

type ToolCallKind string

const (
	ToolCallInvocation ToolCallKind = "call"
	ToolCallResult     ToolCallKind = "result"
)

// ToolCallContext is supplied by the authenticated gateway, never by payload fields.
type ToolCallContext struct {
	OrganizationID uint64
	APIKeyID       uint64
	Direction      ToolDirection
}

// ToolCall is an ephemeral internal value. Never enqueue or persist this value,
// or expose it as a customer API. Arguments and results remain in memory only.
type ToolCall struct {
	ToolCallContext
	ID         string
	IDIsNumber bool
	Name       string
	Protocol   ToolProtocol
	Kind       ToolCallKind
	Arguments  json.RawMessage
	Result     json.RawMessage
	IsError    bool
}

// ToolCallMetadata retains only the bounded tool name and safe correlation metadata.
type ToolCallMetadata struct {
	Protocol   ToolProtocol
	Direction  ToolDirection
	Kind       ToolCallKind
	CallIDHash string
	Name       string
}

func (call ToolCall) Metadata() ToolCallMetadata {
	metadata := ToolCallMetadata{Protocol: call.Protocol, Direction: call.Direction, Kind: call.Kind}
	for _, character := range call.Name {
		if len(metadata.Name) >= 124 {
			break
		}
		if !unicode.IsControl(character) {
			metadata.Name += string(character)
		}
	}
	if call.ID != "" {
		identity := string(call.Protocol) + ":string:" + call.ID
		if call.IDIsNumber {
			identity = string(call.Protocol) + ":number:" + call.ID
		}
		hash := sha256.Sum256([]byte(identity))
		metadata.CallIDHash = hex.EncodeToString(hash[:])
	}
	return metadata
}

// ToolCallInspector lets policy engines consume the same normalized value for all protocols.
type ToolCallInspector interface {
	InspectToolCall(context.Context, ToolCall) (SecurityInspectionResult, error)
}

// ToolCallParseError contains only safe classification, never provider content.
type ToolCallParseError struct {
	Protocol  ToolProtocol
	Direction ToolDirection
}

func (*ToolCallParseError) Error() string { return "invalid tool call" }
