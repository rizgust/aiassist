// Package report is the attachable report framework. A Module builds data
// once and renders it in several formats; Registry.Tools exposes every
// registered module to the model as a read tool and, where a channel can
// push files, a gated send tool.
package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rizgust/aiassist/core"
)

// Format is a render target.
type Format string

const (
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
	FormatHTML     Format = "html"
	FormatPDF      Format = "pdf"
	FormatJSON     Format = "json"
)

// Rendered is one rendered report.
type Rendered struct {
	Format   Format
	MIME     string
	FileName string
	Caption  string
	Data     []byte
}

// Module is one report type. ID is dotted and namespaced
// ("satudata.spike", "ai.usage").
type Module interface {
	ID() string
	Title() string
	// Description tells the model what the report contains and when to use it.
	Description() string
	// Params is the JSON Schema for Build's params (also the tools' input).
	Params() json.RawMessage
	Build(ctx context.Context, params json.RawMessage) (any, error)
	Formats() []Format
	Render(ctx context.Context, data any, f Format) (Rendered, error)
}

// ErrUnsupportedFormat is returned by Render for a format the module lacks.
var ErrUnsupportedFormat = errors.New("unsupported report format")

// Registry holds the enabled modules, in registration order.
type Registry struct {
	modules []Module
	byID    map[string]Module
}

// NewRegistry registers modules; duplicate IDs are an error.
func NewRegistry(modules ...Module) (*Registry, error) {
	r := &Registry{byID: map[string]Module{}}
	for _, m := range modules {
		if err := r.Register(m); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register adds m.
func (r *Registry) Register(m Module) error {
	if _, dup := r.byID[m.ID()]; dup {
		return fmt.Errorf("report: duplicate module %q", m.ID())
	}
	r.modules = append(r.modules, m)
	r.byID[m.ID()] = m
	return nil
}

// Get returns a module by ID.
func (r *Registry) Get(id string) (Module, bool) {
	m, ok := r.byID[id]
	return m, ok
}

// List returns modules in registration order.
func (r *Registry) List() []Module { return append([]Module(nil), r.modules...) }

// ToolOptions controls Registry.Tools.
type ToolOptions struct {
	// DeliveryFormat is what send_* tools render; default FormatPDF, falling
	// back to the module's first format if it lacks PDF.
	DeliveryFormat Format
	// SendGate decides when send_* tools are offered; default
	// core.KeywordGate(core.DeliveryKeywords...).
	SendGate core.GateFunc
	// NoSend omits send_* tools entirely (e.g. MCP, read-only API).
	NoSend bool
}

// Tools returns get_<id> (and send_<id>) tools for every module. Dots in
// IDs become underscores: "satudata.spike" → "get_satudata_spike".
func (r *Registry) Tools(opts ToolOptions) []core.Tool {
	if opts.DeliveryFormat == "" {
		opts.DeliveryFormat = FormatPDF
	}
	if opts.SendGate == nil {
		opts.SendGate = core.KeywordGate(core.DeliveryKeywords...)
	}
	var out []core.Tool
	for _, m := range r.modules {
		out = append(out, getTool(m))
		if !opts.NoSend {
			out = append(out, core.WithGate(sendTool(m, opts.DeliveryFormat), opts.SendGate))
		}
	}
	return out
}

func toolSuffix(id string) string { return strings.ReplaceAll(id, ".", "_") }

func getTool(m Module) core.Tool {
	return core.FuncTool{
		ToolName: "get_" + toolSuffix(m.ID()),
		ToolDescription: m.Description() + " Returns the report as data; it does not send anything to the user. " +
			"Use it to answer questions and compute from the numbers.",
		ToolSchema: m.Params(),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			data, err := m.Build(ctx, input)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(data)
			if err != nil {
				return "", fmt.Errorf("marshal %s: %w", m.ID(), err)
			}
			return string(b), nil
		},
	}
}

func sendTool(m Module, preferred Format) core.Tool {
	return core.FuncTool{
		ToolName: "send_" + toolSuffix(m.ID()),
		ToolDescription: "Build the " + m.Title() + " and send it to the user as a file. Use only when the user " +
			"asked for the report/file itself. After it is sent, do not restate its contents; just acknowledge " +
			"or answer the specific question.",
		ToolSchema: m.Params(),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			d, ok := core.DelivererFrom(ctx)
			if !ok {
				return "", errors.New("this channel cannot send files; answer in text instead")
			}
			key := m.ID() + "|" + canonical(input)
			if !core.ClaimDelivery(ctx, key) {
				return `{"sent":true,"duplicate":true}`, nil
			}
			data, err := m.Build(ctx, input)
			if err != nil {
				core.ReleaseDelivery(ctx, key)
				return "", err
			}
			rendered, err := m.Render(ctx, data, pickFormat(m, preferred))
			if err != nil {
				core.ReleaseDelivery(ctx, key)
				return "", fmt.Errorf("render %s: %w", m.ID(), err)
			}
			caller, _ := core.CallerFrom(ctx)
			out := core.Output{Caption: rendered.Caption}
			if rendered.Format == FormatText || rendered.Format == FormatMarkdown {
				out.Text = string(rendered.Data)
			} else {
				out.File = &core.File{Name: rendered.FileName, MIME: rendered.MIME, Data: rendered.Data}
			}
			if err := d.Deliver(ctx, caller, out); err != nil {
				core.ReleaseDelivery(ctx, key)
				return "", fmt.Errorf("deliver %s: %w", m.ID(), err)
			}
			return `{"sent":true}`, nil
		},
	}
}

func pickFormat(m Module, preferred Format) Format {
	formats := m.Formats()
	for _, f := range formats {
		if f == preferred {
			return f
		}
	}
	if len(formats) > 0 {
		return formats[0]
	}
	return preferred
}

// canonical re-marshals JSON so key order/whitespace don't defeat dedup.
func canonical(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}
