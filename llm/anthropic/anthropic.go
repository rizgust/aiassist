// Package anthropic adapts the Anthropic Messages API to llm.Provider.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/Nuanu-com/aiassist/llm"
)

// ProviderName is the registry.Model.Provider value this adapter serves.
const ProviderName = "anthropic"

// Provider implements llm.Provider over a shared Anthropic client.
type Provider struct {
	client *sdk.Client
}

// New wraps an existing client (construct once, share it).
func New(client *sdk.Client) *Provider { return &Provider{client: client} }

// NewWithAPIKey builds a client authenticated with apiKey. Extra options
// (base URL, timeouts) are passed through.
func NewWithAPIKey(apiKey string, opts ...option.RequestOption) *Provider {
	client := sdk.NewClient(append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)...)
	return &Provider{client: &client}
}

func (p *Provider) Name() string { return ProviderName }

// Complete sends one non-streaming Messages request.
//
// Thinking is left unset on purpose: current Claude models run adaptive
// thinking by default and reject explicit "disabled"/budget configs, so
// depth is steered with req.Effort only. Temperature is never sent for the
// same reason (non-default sampling params are rejected on current models).
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: req.MaxTokens,
	}
	for _, s := range req.System {
		block := sdk.TextBlockParam{Text: s.Text}
		if s.Cache {
			block.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		params.System = append(params.System, block)
	}
	for _, t := range req.Tools {
		schema, err := toInputSchema(t.Schema)
		if err != nil {
			return llm.Response{}, fmt.Errorf("tool %s: %w", t.Name, err)
		}
		params.Tools = append(params.Tools, sdk.ToolUnionParam{OfTool: &sdk.ToolParam{
			Name:        t.Name,
			Description: sdk.String(t.Description),
			InputSchema: schema,
		}})
	}
	for _, m := range req.Messages {
		msg, err := toMessageParam(m)
		if err != nil {
			return llm.Response{}, err
		}
		params.Messages = append(params.Messages, msg)
	}
	if req.Effort != "" {
		params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffort(req.Effort)}
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return llm.Response{}, fmt.Errorf("anthropic messages.new: %w", err)
	}
	return fromMessage(resp), nil
}

func toInputSchema(raw json.RawMessage) (sdk.ToolInputSchemaParam, error) {
	if len(raw) == 0 {
		return sdk.ToolInputSchemaParam{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return sdk.ToolInputSchemaParam{}, fmt.Errorf("invalid input schema: %w", err)
	}
	schema := sdk.ToolInputSchemaParam{Properties: m["properties"]}
	if req, ok := m["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				schema.Required = append(schema.Required, s)
			}
		}
	}
	extra := map[string]any{}
	for k, v := range m {
		switch k {
		case "type", "properties", "required":
		default:
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		schema.ExtraFields = extra
	}
	return schema, nil
}

func toMessageParam(m llm.Message) (sdk.MessageParam, error) {
	blocks := make([]sdk.ContentBlockParamUnion, 0, len(m.Content))
	for _, b := range m.Content {
		switch b.Type {
		case llm.BlockText:
			blocks = append(blocks, sdk.NewTextBlock(b.Text))
		case llm.BlockToolUse:
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			blocks = append(blocks, sdk.NewToolUseBlock(b.ToolUseID, input, b.ToolName))
		case llm.BlockToolResult:
			blocks = append(blocks, sdk.NewToolResultBlock(b.ToolUseID, b.Text, b.IsError))
		case llm.BlockThinking:
			blocks = append(blocks, sdk.NewThinkingBlock(b.Signature, b.Text))
		case llm.BlockRedactedThinking:
			blocks = append(blocks, sdk.NewRedactedThinkingBlock(b.Data))
		default:
			return sdk.MessageParam{}, fmt.Errorf("unsupported block type %q", b.Type)
		}
	}
	switch m.Role {
	case llm.RoleUser:
		return sdk.NewUserMessage(blocks...), nil
	case llm.RoleAssistant:
		return sdk.NewAssistantMessage(blocks...), nil
	default:
		return sdk.MessageParam{}, fmt.Errorf("unsupported role %q", m.Role)
	}
}

func fromMessage(resp *sdk.Message) llm.Response {
	out := llm.Response{
		StopReason: fromStopReason(resp.StopReason),
		Usage: llm.Usage{
			InputTokens:      resp.Usage.InputTokens,
			OutputTokens:     resp.Usage.OutputTokens,
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
		},
	}
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case sdk.TextBlock:
			out.Content = append(out.Content, llm.Text(v.Text))
		case sdk.ToolUseBlock:
			out.Content = append(out.Content, llm.Block{
				Type: llm.BlockToolUse, ToolUseID: v.ID, ToolName: v.Name, Input: v.Input,
			})
		case sdk.ThinkingBlock:
			out.Content = append(out.Content, llm.Block{
				Type: llm.BlockThinking, Text: v.Thinking, Signature: v.Signature,
			})
		case sdk.RedactedThinkingBlock:
			out.Content = append(out.Content, llm.Block{Type: llm.BlockRedactedThinking, Data: v.Data})
		}
	}
	return out
}

func fromStopReason(r sdk.StopReason) llm.StopReason {
	switch r {
	case sdk.StopReasonEndTurn:
		return llm.StopEndTurn
	case sdk.StopReasonToolUse:
		return llm.StopToolUse
	case sdk.StopReasonMaxTokens:
		return llm.StopMaxTokens
	case sdk.StopReasonRefusal:
		return llm.StopRefusal
	default:
		return llm.StopOther
	}
}

var _ llm.Provider = (*Provider)(nil)
