// Package llm defines the provider-neutral request/response shapes the
// engine speaks, plus the Provider interface each vendor adapter implements.
// Nothing outside llm/<vendor> packages imports a vendor SDK.
package llm

import (
	"context"
	"encoding/json"
	"strings"
)

// Role is who authored a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType discriminates Block.
type BlockType string

const (
	BlockText             BlockType = "text"
	BlockToolUse          BlockType = "tool_use"
	BlockToolResult       BlockType = "tool_result"
	BlockThinking         BlockType = "thinking"
	BlockRedactedThinking BlockType = "redacted_thinking"
)

// Block is one content block. Only the fields relevant to Type are set.
//
// Thinking blocks are carried so they can be passed back to the provider
// unchanged within a tool-use loop (current Claude models require this);
// callers should never inspect or edit them.
type Block struct {
	Type BlockType `json:"type"`

	// text / thinking
	Text string `json:"text,omitempty"`

	// tool_use / tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	// thinking (Signature) / redacted_thinking (Data)
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

// Text builds a text block.
func Text(s string) Block { return Block{Type: BlockText, Text: s} }

// ToolResult builds a tool_result block answering toolUseID.
func ToolResult(toolUseID, content string, isError bool) Block {
	return Block{Type: BlockToolResult, ToolUseID: toolUseID, Text: content, IsError: isError}
}

// Message is one conversation turn.
type Message struct {
	Role    Role    `json:"role"`
	Content []Block `json:"content"`
}

// UserText is shorthand for a single-text-block user message.
func UserText(s string) Message { return Message{Role: RoleUser, Content: []Block{Text(s)}} }

// AssistantText is shorthand for a single-text-block assistant message.
func AssistantText(s string) Message {
	return Message{Role: RoleAssistant, Content: []Block{Text(s)}}
}

// SystemBlock is one block of the system prompt. Cache marks a prompt-cache
// breakpoint after this block: put stable content first and volatile
// content (dates, memory) after the last cached block.
type SystemBlock struct {
	Text  string
	Cache bool
}

// ToolDef describes a tool offered to the model. Schema is a JSON Schema
// object ({"type":"object","properties":{...},"required":[...]}).
type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Request is a provider-neutral completion request. Model is the
// provider's own model id (e.g. "claude-sonnet-5-5"); the engine fills it
// from the routed registry entry.
type Request struct {
	Model     string
	System    []SystemBlock
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int64
	// Effort is a provider hint for reasoning depth ("low".."max"); "" uses
	// the model default.
	Effort string
}

// StopReason is why generation stopped.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	// StopRefusal means the provider's safety layer declined the request;
	// the engine treats it like an error and falls back to the next route.
	StopRefusal StopReason = "refusal"
	StopOther   StopReason = "other"
)

// Usage is token accounting for one call.
type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

// Add returns u + o.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + o.InputTokens,
		OutputTokens:     u.OutputTokens + o.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
	}
}

// Response is a provider-neutral completion result.
type Response struct {
	Content    []Block
	StopReason StopReason
	Usage      Usage
}

// Text concatenates every text block, in order.
func (r Response) Text() string {
	var sb strings.Builder
	for _, b := range r.Content {
		if b.Type == BlockText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// ToolUses returns the tool_use blocks, in order.
func (r Response) ToolUses() []Block {
	var out []Block
	for _, b := range r.Content {
		if b.Type == BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

// AsMessage returns the response as an assistant turn, thinking blocks
// included, for appending to history.
func (r Response) AsMessage() Message {
	return Message{Role: RoleAssistant, Content: r.Content}
}

// Provider is one vendor adapter. Name must match registry.Model.Provider.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}
