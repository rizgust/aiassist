package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nuanu-com/aiassist/ledger"
	"github.com/Nuanu-com/aiassist/llm"
	"github.com/Nuanu-com/aiassist/registry"
)

// scriptedProvider returns queued responses per model id and records requests.
type scriptedProvider struct {
	mu       sync.Mutex
	script   map[string][]func(llm.Request) (llm.Response, error)
	requests []llm.Request
}

func (p *scriptedProvider) Name() string { return "anthropic" }

func (p *scriptedProvider) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	q := p.script[req.Model]
	if len(q) == 0 {
		return llm.Response{}, errors.New("no scripted response for " + req.Model)
	}
	p.script[req.Model] = q[1:]
	return q[0](req)
}

func reply(text string) func(llm.Request) (llm.Response, error) {
	return func(llm.Request) (llm.Response, error) {
		return llm.Response{Content: []llm.Block{llm.Text(text)}, StopReason: llm.StopEndTurn,
			Usage: llm.Usage{InputTokens: 100, OutputTokens: 10}}, nil
	}
}

func callTool(name, input string) func(llm.Request) (llm.Response, error) {
	return func(llm.Request) (llm.Response, error) {
		return llm.Response{Content: []llm.Block{
			{Type: llm.BlockThinking, Text: "", Signature: "sig"},
			{Type: llm.BlockToolUse, ToolUseID: "tu_" + name, ToolName: name, Input: json.RawMessage(input)},
		}, StopReason: llm.StopToolUse, Usage: llm.Usage{InputTokens: 50, OutputTokens: 5}}, nil
	}
}

func fail(err error) func(llm.Request) (llm.Response, error) {
	return func(llm.Request) (llm.Response, error) { return llm.Response{}, err }
}

func testRouter() *registry.Router {
	return &registry.Router{
		Store: &registry.Static{
			ModelList: []registry.Model{
				{ID: "sonnet", Provider: "anthropic", ModelID: "claude-sonnet-5-5", Enabled: true,
					Price: registry.Pricing{InPerMTok: 2, OutPerMTok: 10}},
				{ID: "opus", Provider: "anthropic", ModelID: "claude-opus-5-5", Enabled: true,
					Price: registry.Pricing{InPerMTok: 4, OutPerMTok: 20}},
				{ID: "haiku", Provider: "anthropic", ModelID: "claude-haiku-5-5", Enabled: true},
			},
			RouteList: []registry.Route{
				{Task: "chat", ModelID: "sonnet", Weight: 1, Priority: 0, MaxTokens: 16000, Enabled: true},
				{Task: "chat", ModelID: "opus", Weight: 1, Priority: 1, MaxTokens: 8000, Effort: "high", Enabled: true},
				{Task: "memory_merge", ModelID: "haiku", Weight: 1, Priority: 0, MaxTokens: 512, Enabled: true},
			},
		},
		Project: "test",
	}
}

func newEngine(t *testing.T, p *scriptedProvider, mutate func(*Config)) (*Engine, *ledger.Memory) {
	t.Helper()
	led := &ledger.Memory{}
	cfg := Config{Project: "test", SystemPrompt: "You are a test assistant.", Router: testRouter(),
		Providers: []llm.Provider{p}, Ledger: led}
	if mutate != nil {
		mutate(&cfg)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e, led
}

var caller = Caller{ID: "tg:1", Username: "rizgust", Channel: "telegram"}

func TestAsk_ToolLoopPassesThinkingBackAndAnswers(t *testing.T) {
	var gotCaller Caller
	tool := FuncTool{ToolName: "lookup", ToolDescription: "d", ToolSchema: Schema(map[string]any{}),
		Fn: func(ctx context.Context, in json.RawMessage) (string, error) {
			gotCaller, _ = CallerFrom(ctx)
			return `{"revenue":42}`, nil
		}}
	p := &scriptedProvider{script: map[string][]func(llm.Request) (llm.Response, error){
		"claude-sonnet-5-5": {callTool("lookup", `{}`), reply("Revenue was 42.")},
	}}
	e, led := newEngine(t, p, func(c *Config) { c.Tools = []Tool{tool} })

	answer, err := e.Ask(context.Background(), caller, "how much?")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Revenue was 42." {
		t.Fatalf("answer = %q", answer)
	}
	if gotCaller.ID != "tg:1" {
		t.Fatalf("tool saw caller %+v", gotCaller)
	}
	second := p.requests[1]
	// user question, assistant(thinking+tool_use), user(tool_result)
	if len(second.Messages) != 3 {
		t.Fatalf("second request has %d messages", len(second.Messages))
	}
	if b := second.Messages[1].Content[0]; b.Type != llm.BlockThinking || b.Signature != "sig" {
		t.Fatalf("thinking block not passed back: %+v", b)
	}
	if r := second.Messages[2].Content[0]; r.Type != llm.BlockToolResult || r.Text != `{"revenue":42}` || r.IsError {
		t.Fatalf("tool result = %+v", r)
	}
	if second.MaxTokens != 16000 || second.Model != "claude-sonnet-5-5" {
		t.Fatalf("route params not applied: %+v", second)
	}
	if !second.System[0].Cache {
		t.Fatal("stable system prompt should be cached")
	}

	recs := led.Records()
	if len(recs) != 2 || recs[0].RequestID != recs[1].RequestID || recs[0].Channel != "telegram" {
		t.Fatalf("ledger records = %+v", recs)
	}
	// 150 in @ $2 + 15 out @ $10 per MTok
	if got := recs[0].CostUSD + recs[1].CostUSD; got < 0.000449 || got > 0.000451 {
		t.Fatalf("cost = %v", got)
	}
}

func TestAsk_FallsBackOnErrorAndRefusal(t *testing.T) {
	refusal := func(llm.Request) (llm.Response, error) {
		return llm.Response{StopReason: llm.StopRefusal}, nil
	}
	for name, first := range map[string]func(llm.Request) (llm.Response, error){
		"error":   fail(errors.New("529 overloaded")),
		"refusal": refusal,
	} {
		t.Run(name, func(t *testing.T) {
			p := &scriptedProvider{script: map[string][]func(llm.Request) (llm.Response, error){
				"claude-sonnet-5-5": {first},
				"claude-opus-5-5":   {reply("from opus")},
			}}
			e, led := newEngine(t, p, nil)
			answer, err := e.Ask(context.Background(), caller, "hi")
			if err != nil || answer != "from opus" {
				t.Fatalf("answer %q err %v", answer, err)
			}
			if p.requests[1].Effort != "high" || p.requests[1].MaxTokens != 8000 {
				t.Fatalf("fallback route params not applied: %+v", p.requests[1])
			}
			recs := led.Records()
			if len(recs) != 2 || recs[0].Status != ledger.StatusError || recs[1].Status != ledger.StatusOK {
				t.Fatalf("ledger = %+v", recs)
			}
		})
	}
}

func TestAsk_StaysOnModelThatSucceeded(t *testing.T) {
	tool := FuncTool{ToolName: "t", ToolSchema: Schema(map[string]any{}),
		Fn: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}
	p := &scriptedProvider{script: map[string][]func(llm.Request) (llm.Response, error){
		"claude-sonnet-5-5": {fail(errors.New("down"))},
		"claude-opus-5-5":   {callTool("t", `{}`), reply("done")},
	}}
	e, _ := newEngine(t, p, func(c *Config) { c.Tools = []Tool{tool} })
	if _, err := e.Ask(context.Background(), caller, "x"); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 3 || p.requests[2].Model != "claude-opus-5-5" {
		t.Fatalf("expected iteration 2 to stay on opus, got %d requests", len(p.requests))
	}
}

func TestAsk_AllCandidatesFail(t *testing.T) {
	p := &scriptedProvider{script: map[string][]func(llm.Request) (llm.Response, error){
		"claude-sonnet-5-5": {fail(errors.New("a"))},
		"claude-opus-5-5":   {fail(errors.New("b"))},
	}}
	e, _ := newEngine(t, p, nil)
	_, err := e.Ask(context.Background(), caller, "x")
	if err == nil || !strings.Contains(err.Error(), "all 2 candidates failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestAsk_GatedToolsOnlyOfferedWhenGatePasses(t *testing.T) {
	plain := FuncTool{ToolName: "get_data", ToolSchema: Schema(map[string]any{})}
	send := WithGate(FuncTool{ToolName: "send_pdf", ToolSchema: Schema(map[string]any{})}, KeywordGate(DeliveryKeywords...))
	admin := WithGate(FuncTool{ToolName: "purge", ToolSchema: Schema(map[string]any{})}, PrivilegedOnly)

	p := &scriptedProvider{script: map[string][]func(llm.Request) (llm.Response, error){
		"claude-sonnet-5-5": {reply("a"), reply("b"), reply("c")},
	}}
	e, _ := newEngine(t, p, func(c *Config) { c.Tools = []Tool{plain, send, admin} })

	names := func(r llm.Request) string {
		var n []string
		for _, d := range r.Tools {
			n = append(n, d.Name)
		}
		return strings.Join(n, ",")
	}
	_, _ = e.Ask(context.Background(), caller, "what was revenue?")
	_, _ = e.Ask(context.Background(), caller, "send me the PDF Report")
	_, _ = e.Ask(context.Background(), Caller{ID: "tg:0", Privileged: true}, "hello")
	if got := names(p.requests[0]); got != "get_data" {
		t.Errorf("plain question tools = %s", got)
	}
	if got := names(p.requests[1]); got != "get_data,send_pdf" {
		t.Errorf("delivery question tools = %s", got)
	}
	if got := names(p.requests[2]); got != "get_data,purge" {
		t.Errorf("privileged tools = %s", got)
	}
}

func TestAsk_UnknownToolAndToolErrorBecomeErrorResults(t *testing.T) {
	bad := FuncTool{ToolName: "bad", ToolSchema: Schema(map[string]any{}),
		Fn: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("boom") }}
	p := &scriptedProvider{script: map[string][]func(llm.Request) (llm.Response, error){
		"claude-sonnet-5-5": {
			func(llm.Request) (llm.Response, error) {
				return llm.Response{StopReason: llm.StopToolUse, Content: []llm.Block{
					{Type: llm.BlockToolUse, ToolUseID: "1", ToolName: "bad", Input: json.RawMessage(`{}`)},
					{Type: llm.BlockToolUse, ToolUseID: "2", ToolName: "ghost", Input: json.RawMessage(`{}`)},
				}}, nil
			},
			reply("sorry"),
		},
	}}
	e, _ := newEngine(t, p, func(c *Config) { c.Tools = []Tool{bad} })
	if _, err := e.Ask(context.Background(), caller, "x"); err != nil {
		t.Fatal(err)
	}
	results := p.requests[1].Messages[2].Content
	if len(results) != 2 || !results[0].IsError || results[0].Text != "boom" ||
		!results[1].IsError || results[1].Text != "unknown tool: ghost" {
		t.Fatalf("results = %+v", results)
	}
}

func TestAsk_MaxIterations(t *testing.T) {
	loop := FuncTool{ToolName: "t", ToolSchema: Schema(map[string]any{}),
		Fn: func(context.Context, json.RawMessage) (string, error) { return "again", nil }}
	var script []func(llm.Request) (llm.Response, error)
	for i := 0; i < 3; i++ {
		script = append(script, callTool("t", `{}`))
	}
	p := &scriptedProvider{script: map[string][]func(llm.Request) (llm.Response, error){"claude-sonnet-5-5": script}}
	e, _ := newEngine(t, p, func(c *Config) { c.Tools = []Tool{loop}; c.MaxIterations = 3 })
	if _, err := e.Ask(context.Background(), caller, "x"); !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("err = %v", err)
	}
}

// --- memory -----------------------------------------------------------------

type memConv struct {
	mu    sync.Mutex
	turns map[string][]Turn
}

func (m *memConv) Append(_ context.Context, id string, role TurnRole, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns[id] = append(m.turns[id], Turn{Role: role, Content: content})
	return nil
}

func (m *memConv) Recent(_ context.Context, id string, limit int) ([]Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.turns[id]
	if len(t) > limit {
		t = t[len(t)-limit:]
	}
	return append([]Turn(nil), t...), nil
}

type memGlobal struct{ entries []MemoryEntry }

func (m *memGlobal) List(context.Context) ([]MemoryEntry, error) { return m.entries, nil }
func (m *memGlobal) Save(_ context.Context, c, by string) (MemoryEntry, error) {
	e := MemoryEntry{ID: int64(len(m.entries) + 1), Content: c, SavedBy: by}
	m.entries = append(m.entries, e)
	return e, nil
}

type memUser struct {
	mu      sync.Mutex
	content map[string]string
	saved   chan struct{}
}

func (m *memUser) Get(_ context.Context, id string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.content[id]
	return c, ok, nil
}

func (m *memUser) Upsert(_ context.Context, id, c string) error {
	m.mu.Lock()
	m.content[id] = c
	m.mu.Unlock()
	m.saved <- struct{}{}
	return nil
}

func TestAsk_HistoryMemoryAndContextBlocks(t *testing.T) {
	conv := &memConv{turns: map[string][]Turn{"tg:1": {
		{Role: TurnUser, Content: "earlier question"},
		{Role: TurnAssistant, Content: "  "}, // legacy empty row: skipped
		{Role: TurnAssistant, Content: "earlier answer"},
	}}}
	global := &memGlobal{entries: []MemoryEntry{{Content: "Q3 target is 5B"}}}
	user := &memUser{content: map[string]string{"tg:1": "Prefers monthly views."}, saved: make(chan struct{}, 1)}

	p := &scriptedProvider{script: map[string][]func(llm.Request) (llm.Response, error){
		"claude-sonnet-5-5": {reply("It is 5B.")},
		"claude-haiku-5-5":  {reply("Prefers monthly views. Asked about Q3 target.")},
	}}
	e, led := newEngine(t, p, func(c *Config) {
		c.Conversation, c.GlobalMemory, c.UserMemory, c.HistoryLimit = conv, global, user, 10
		c.ContextBlocks = func(context.Context, Caller) []string { return []string{"Today is 2026-10-12."} }
	})

	if _, err := e.Ask(context.Background(), caller, "what is the target?"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-user.saved:
	case <-time.After(2 * time.Second):
		t.Fatal("user memory merge never saved")
	}

	req := p.requests[0]
	if len(req.Messages) != 3 || req.Messages[1].Content[0].Text != "earlier answer" {
		t.Fatalf("history not seeded correctly: %+v", req.Messages)
	}
	sys := req.System
	if len(sys) != 4 || sys[1].Text != "Today is 2026-10-12." || !strings.Contains(sys[2].Text, "Q3 target is 5B") ||
		!strings.Contains(sys[3].Text, "Prefers monthly views.") || sys[1].Cache || sys[2].Cache || sys[3].Cache {
		t.Fatalf("system blocks = %+v", sys)
	}
	if got := conv.turns["tg:1"]; len(got) != 5 || got[4].Content != "It is 5B." {
		t.Fatalf("exchange not recorded: %+v", got)
	}
	if got, _, _ := user.Get(context.Background(), "tg:1"); got != "Prefers monthly views. Asked about Q3 target." {
		t.Fatalf("user memory = %q", got)
	}
	var mergeRec *ledger.Record
	for _, r := range led.Records() {
		if r.Task == registry.TaskMemoryMerge {
			mergeRec = &r
		}
	}
	if mergeRec == nil || mergeRec.ModelID != "haiku" || mergeRec.CallerID != "tg:1" {
		t.Fatalf("memory merge not ledgered on haiku: %+v", mergeRec)
	}
}

func TestBuiltinTools(t *testing.T) {
	global := &memGlobal{}
	ctx := WithCaller(context.Background(), caller)
	out, err := NewSaveGlobalMemoryTool(global).Execute(ctx, json.RawMessage(`{"content":"remember this"}`))
	if err != nil || !strings.Contains(out, "remember this") || global.entries[0].SavedBy != "tg:1" {
		t.Fatalf("save memory: %s %v %+v", out, err, global.entries)
	}

	var got []string
	esc := escalatorFunc(func(_ context.Context, from Caller, q, reason string) error {
		got = []string{from.ID, q, reason}
		return nil
	})
	tool := NewEscalateTool("ask_the_matrix", "desc", esc)
	if _, err := tool.Execute(ctx, json.RawMessage(`{"question":"why?","reason":"no data"}`)); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "tg:1|why?|no data" {
		t.Fatalf("escalated %v", got)
	}
}

type escalatorFunc func(ctx context.Context, from Caller, question, reason string) error

func (f escalatorFunc) Escalate(ctx context.Context, from Caller, q, r string) error {
	return f(ctx, from, q, r)
}

func TestClaimDelivery(t *testing.T) {
	ctx := ContextWithDeliveryState(context.Background())
	if !ClaimDelivery(ctx, "k") || ClaimDelivery(ctx, "k") {
		t.Fatal("second claim of the same key should fail")
	}
	ReleaseDelivery(ctx, "k")
	if !ClaimDelivery(ctx, "k") {
		t.Fatal("claim after release should succeed")
	}
	if !ClaimDelivery(context.Background(), "k") {
		t.Fatal("outside an Ask every claim succeeds")
	}
}

func TestNew_Validation(t *testing.T) {
	p := &scriptedProvider{}
	dup := FuncTool{ToolName: "x"}
	cases := map[string]Config{
		"no prompt":    {Router: testRouter(), Providers: []llm.Provider{p}},
		"no router":    {SystemPrompt: "s", Providers: []llm.Provider{p}},
		"no providers": {SystemPrompt: "s", Router: testRouter()},
		"dup tools":    {SystemPrompt: "s", Router: testRouter(), Providers: []llm.Provider{p}, Tools: []Tool{dup, dup}},
	}
	for name, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
