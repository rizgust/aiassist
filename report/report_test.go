package report

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rizgust/aiassist/core"
	"github.com/rizgust/aiassist/ledger"
)

type fakeModule struct{ builds int }

func (f *fakeModule) ID() string              { return "satudata.spike" }
func (f *fakeModule) Title() string           { return "Spike Report" }
func (f *fakeModule) Description() string     { return "Spikes." }
func (f *fakeModule) Params() json.RawMessage { return core.Schema(map[string]any{}) }
func (f *fakeModule) Formats() []Format       { return []Format{FormatPDF, FormatJSON} }
func (f *fakeModule) Build(context.Context, json.RawMessage) (any, error) {
	f.builds++
	return map[string]int{"spikes": 3}, nil
}
func (f *fakeModule) Render(_ context.Context, _ any, fm Format) (Rendered, error) {
	return Rendered{Format: fm, MIME: "application/pdf", FileName: "spike.pdf", Caption: "Spikes", Data: []byte("%PDF")}, nil
}

func TestRegistryTools_NamingGatingAndDedup(t *testing.T) {
	mod := &fakeModule{}
	reg, err := NewRegistry(mod)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(mod, mod); err == nil {
		t.Fatal("duplicate module should error")
	}

	tools := reg.Tools(ToolOptions{})
	if len(tools) != 2 || tools[0].Name() != "get_satudata_spike" || tools[1].Name() != "send_satudata_spike" {
		t.Fatalf("tools = %v, %v", tools[0].Name(), tools[1].Name())
	}
	gated, ok := tools[1].(core.Gated)
	if !ok || gated.Offered("why the spike?", core.Caller{}) || !gated.Offered("send the spike report", core.Caller{}) {
		t.Fatal("send tool should be keyword-gated")
	}
	if got := reg.Tools(ToolOptions{NoSend: true}); len(got) != 1 {
		t.Fatalf("NoSend tools = %d", len(got))
	}

	out, err := tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || out != `{"spikes":3}` {
		t.Fatalf("get = %s %v", out, err)
	}

	// send without a deliverer: refuses
	if _, err := tools[1].Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("send without deliverer should error")
	}

	var delivered []core.Output
	ctx := core.WithDeliverer(core.WithCaller(context.Background(), core.Caller{ID: "tg:1"}),
		core.DelivererFunc(func(_ context.Context, to core.Caller, o core.Output) error {
			if to.ID != "tg:1" {
				t.Errorf("delivered to %+v", to)
			}
			delivered = append(delivered, o)
			return nil
		}))
	// Within one dedup scope (one Ask), the same params — in any key order —
	// send once.
	scope := core.ContextWithDeliveryState(ctx)
	for _, in := range []string{`{"month":"2026-09","venue":"a"}`, `{"venue":"a", "month":"2026-09"}`} {
		if _, err := tools[1].Execute(scope, json.RawMessage(in)); err != nil {
			t.Fatal(err)
		}
	}
	if len(delivered) != 1 || delivered[0].File == nil || delivered[0].File.Name != "spike.pdf" {
		t.Fatalf("delivered = %+v", delivered)
	}
}

func TestUsageModule(t *testing.T) {
	led := &ledger.Memory{}
	oct := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	_ = led.Record(context.Background(), ledger.Record{At: oct, Project: "sd", Task: "chat", ModelID: "sonnet", CostUSD: 1.25, Status: "ok"})
	_ = led.Record(context.Background(), ledger.Record{At: oct.AddDate(0, -1, 0), Project: "sd", Task: "chat", ModelID: "sonnet", CostUSD: 3, Status: "ok"})

	m := &UsageModule{Ledger: led, Project: "sd", Now: func() time.Time { return oct }}
	data, err := m.Build(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := data.(UsageReport)
	if rep.Month != "2026-10" || rep.TotalCostUSD != 1.25 || rep.PrevMonthCostUSD != 3 || rep.TotalCalls != 1 {
		t.Fatalf("report = %+v", rep)
	}
	text, err := m.Render(context.Background(), rep, FormatText)
	if err != nil || !strings.Contains(string(text.Data), "chat / sonnet: $1.25") {
		t.Fatalf("text = %s %v", text.Data, err)
	}
	if _, err := m.Build(context.Background(), json.RawMessage(`{"month":"Sept"}`)); err == nil {
		t.Fatal("bad month should error")
	}
}
