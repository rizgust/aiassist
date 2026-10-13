package core

import (
	"context"
	"encoding/json"
	"strings"
)

// Tool is anything the model can call. Execute reads the Caller (and any
// Deliverer) from ctx.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// Gated is optionally implemented by a Tool that should only be offered to
// the model for some questions or callers. An unoffered tool cannot be
// invoked at all, so a gate is a hard guarantee rather than a prompt
// instruction.
type Gated interface {
	Offered(question string, caller Caller) bool
}

// GateFunc decides whether a tool is offered.
type GateFunc func(question string, caller Caller) bool

type gatedTool struct {
	Tool
	gate GateFunc
}

func (g gatedTool) Offered(question string, caller Caller) bool { return g.gate(question, caller) }

// WithGate wraps t so it is only offered when gate returns true.
func WithGate(t Tool, gate GateFunc) Tool { return gatedTool{Tool: t, gate: gate} }

// DeliveryKeywords are the case-insensitive substrings that make a question
// eligible for file-delivering tools: a data question only triggers a file
// push when the asker's own words call for one.
var DeliveryKeywords = []string{"report", "file", "pdf", "doc"}

// KeywordGate offers a tool when question contains any keyword
// (case-insensitive substring; "doc" also matches "document").
func KeywordGate(keywords ...string) GateFunc {
	return func(question string, _ Caller) bool {
		lower := strings.ToLower(question)
		for _, k := range keywords {
			if strings.Contains(lower, strings.ToLower(k)) {
				return true
			}
		}
		return false
	}
}

// PrivilegedOnly offers a tool only to privileged callers.
func PrivilegedOnly(_ string, c Caller) bool { return c.Privileged }

// Schema builds a JSON Schema object from properties and required names.
// Panics only on unmarshalable input, which is a programming error.
func Schema(properties map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		s["required"] = required
	}
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}

// FuncTool adapts plain functions to Tool.
type FuncTool struct {
	ToolName        string
	ToolDescription string
	ToolSchema      json.RawMessage
	Fn              func(ctx context.Context, input json.RawMessage) (string, error)
}

func (f FuncTool) Name() string            { return f.ToolName }
func (f FuncTool) Description() string     { return f.ToolDescription }
func (f FuncTool) Schema() json.RawMessage { return f.ToolSchema }
func (f FuncTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	return f.Fn(ctx, input)
}
