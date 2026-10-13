package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// NewSaveGlobalMemoryTool lets the model record a durable, cross-caller
// fact. The model calls it proactively (the description says so), which is
// what makes saving automatic rather than command-gated.
func NewSaveGlobalMemoryTool(store GlobalMemoryStore) Tool {
	return FuncTool{
		ToolName: "save_global_memory",
		ToolDescription: "Save a durable, cross-user fact or decision to shared long-term memory, visible to every " +
			"future conversation with any stakeholder - for example \"month X was high-potential income\" or " +
			"\"the Q3 revenue target was revised to Y\". Call this proactively whenever a stakeholder states " +
			"this kind of durable fact or decision mid-conversation, without waiting to be explicitly asked to " +
			"remember it. Do NOT use this for routine chat, transient questions, or anything not meant to be " +
			"remembered indefinitely.",
		ToolSchema: Schema(map[string]any{
			"content": map[string]any{
				"type": "string",
				"description": "The durable fact or decision to remember, written as a standalone statement " +
					"that will make sense read out of context later.",
			},
		}, "content"),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("invalid save_global_memory input: %w", err)
			}
			if in.Content == "" {
				return "", errors.New("save_global_memory: content is required")
			}
			caller, _ := CallerFrom(ctx)
			entry, err := store.Save(ctx, in.Content, caller.ID)
			if err != nil {
				return "", fmt.Errorf("save global memory: %w", err)
			}
			out, err := json.Marshal(entry)
			return string(out), err
		},
	}
}

// Escalator forwards a question to a human operator.
type Escalator interface {
	Escalate(ctx context.Context, from Caller, question, reason string) error
}

// NewEscalateTool lets the model hand a question to a human as a last
// resort. name and description are project-specific (the SatuData bot uses
// "ask_the_matrix"); identity comes from context, never from tool input.
func NewEscalateTool(name, description string, esc Escalator) Tool {
	return FuncTool{
		ToolName:        name,
		ToolDescription: description,
		ToolSchema: Schema(map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "The user's original question or request, verbatim or lightly cleaned up.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Why you can't answer this yourself.",
			},
		}, "question", "reason"),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Question string `json:"question"`
				Reason   string `json:"reason"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("invalid %s input: %w", name, err)
			}
			if in.Question == "" {
				return "", fmt.Errorf("%s: question is required", name)
			}
			caller, _ := CallerFrom(ctx)
			if err := esc.Escalate(ctx, caller, in.Question, in.Reason); err != nil {
				return "", fmt.Errorf("escalate: %w", err)
			}
			return `{"forwarded":true}`, nil
		},
	}
}
