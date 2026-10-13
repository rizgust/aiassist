package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Nuanu-com/aiassist/ledger"
	"github.com/Nuanu-com/aiassist/llm"
	"github.com/Nuanu-com/aiassist/registry"
)

const (
	defaultMaxIterations    = 5
	defaultMaxTokens        = 4096
	defaultUserMemoryMaxLen = 1500
	userMemoryMergeTimeout  = 30 * time.Second
)

// ErrRefused is recorded when a provider's safety layer declines a request.
var ErrRefused = errors.New("model refused the request")

// ErrMaxIterations is returned when the tool loop never reaches an answer.
var ErrMaxIterations = errors.New("exceeded max tool-use iterations without a final answer")

// CandidateSource yields routed models for a task; *registry.Router
// implements it.
type CandidateSource interface {
	Candidates(ctx context.Context, task string) ([]registry.Candidate, error)
}

// Config wires an Engine. Only SystemPrompt, Router and Providers are
// required; every store is optional and its feature is skipped when nil.
type Config struct {
	Project      string
	SystemPrompt string // stable; cached
	Tools        []Tool

	Router    CandidateSource
	Providers []llm.Provider
	Ledger    ledger.Recorder

	Conversation ConversationStore
	GlobalMemory GlobalMemoryStore
	UserMemory   UserMemoryStore
	HistoryLimit int

	// ContextBlocks returns volatile per-call system text (today's date,
	// data availability window, ...). It is placed after the cached prompt.
	ContextBlocks func(ctx context.Context, c Caller) []string

	MaxIterations    int    // default 5
	ChatTask         string // default registry.TaskChat
	MemoryTask       string // default registry.TaskMemoryMerge
	UserMemoryMaxLen int    // runes; default 1500

	Logger *slog.Logger
	Now    func() time.Time
}

// Engine answers questions with tool calling over routed models.
type Engine struct {
	cfg       Config
	providers map[string]llm.Provider
}

// New validates cfg and applies defaults.
func New(cfg Config) (*Engine, error) {
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		return nil, errors.New("core: SystemPrompt is required")
	}
	if cfg.Router == nil {
		return nil, errors.New("core: Router is required")
	}
	if len(cfg.Providers) == 0 {
		return nil, errors.New("core: at least one Provider is required")
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = defaultMaxIterations
	}
	if cfg.ChatTask == "" {
		cfg.ChatTask = registry.TaskChat
	}
	if cfg.MemoryTask == "" {
		cfg.MemoryTask = registry.TaskMemoryMerge
	}
	if cfg.UserMemoryMaxLen <= 0 {
		cfg.UserMemoryMaxLen = defaultUserMemoryMaxLen
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	seen := map[string]bool{}
	for _, t := range cfg.Tools {
		if seen[t.Name()] {
			return nil, fmt.Errorf("core: duplicate tool name %q", t.Name())
		}
		seen[t.Name()] = true
	}
	providers := make(map[string]llm.Provider, len(cfg.Providers))
	for _, p := range cfg.Providers {
		providers[p.Name()] = p
	}
	return &Engine{cfg: cfg, providers: providers}, nil
}

// Ask answers question for caller, running the tool-use loop until the
// model produces a final answer. Gated tools are offered only when their
// gate passes for this question and caller. The routed model that first
// succeeds is kept for the rest of the loop; later candidates remain as
// fallbacks if it starts failing.
func (e *Engine) Ask(ctx context.Context, caller Caller, question string) (string, error) {
	ctx = WithCaller(ctx, caller)
	ctx = ContextWithDeliveryState(ctx)

	offered := e.offeredTools(question, caller)
	defs := make([]llm.ToolDef, 0, len(offered))
	byName := make(map[string]Tool, len(offered))
	for _, t := range offered {
		defs = append(defs, llm.ToolDef{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
		byName[t.Name()] = t
	}

	candidates, err := e.cfg.Router.Candidates(ctx, e.cfg.ChatTask)
	if err != nil {
		return "", err
	}

	messages := e.seedHistory(ctx, caller.ID)
	messages = append(messages, llm.UserText(question))
	req := llm.Request{System: e.buildSystem(ctx, caller), Tools: defs}
	requestID := newRequestID()

	for iteration := 0; iteration < e.cfg.MaxIterations; iteration++ {
		req.Messages = messages
		resp, used, err := e.completeWith(ctx, e.cfg.ChatTask, candidates, req, requestID)
		if err != nil {
			return "", err
		}
		candidates = candidates[used:]

		// Append the assistant turn (thinking blocks included) before
		// acting on it, so tool_use blocks stay attached to their turn.
		messages = append(messages, resp.AsMessage())

		uses := resp.ToolUses()
		if resp.StopReason != llm.StopToolUse || len(uses) == 0 {
			answer := resp.Text()
			e.finish(ctx, caller, question, answer)
			return answer, nil
		}
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: e.runTools(ctx, uses, byName)})
	}
	return "", fmt.Errorf("%w (%d)", ErrMaxIterations, e.cfg.MaxIterations)
}

// Complete runs one routed, ledgered, non-tool call for task (summaries,
// classification, memory merge, spike explanation...). req.Model is filled
// from the route; req.MaxTokens/Effort fall back to the route's values.
func (e *Engine) Complete(ctx context.Context, task string, req llm.Request) (llm.Response, error) {
	candidates, err := e.cfg.Router.Candidates(ctx, task)
	if err != nil {
		return llm.Response{}, err
	}
	resp, _, err := e.completeWith(ctx, task, candidates, req, newRequestID())
	return resp, err
}

// completeWith tries candidates in order and returns the response and the
// index of the candidate that produced it.
func (e *Engine) completeWith(ctx context.Context, task string, candidates []registry.Candidate, req llm.Request, requestID string) (llm.Response, int, error) {
	var errs []error
	for i, c := range candidates {
		p, ok := e.providers[c.Model.Provider]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: no provider %q configured", c.Model.ID, c.Model.Provider))
			continue
		}
		r := req
		r.Model = c.Model.ModelID
		if r.MaxTokens <= 0 {
			r.MaxTokens = c.Route.MaxTokens
		}
		if r.MaxTokens <= 0 {
			r.MaxTokens = defaultMaxTokens
		}
		if r.Effort == "" {
			r.Effort = c.Route.Effort
		}

		start := e.cfg.Now()
		resp, err := p.Complete(ctx, r)
		if err == nil && resp.StopReason == llm.StopRefusal {
			err = ErrRefused
		}
		e.record(ctx, task, c, requestID, resp.Usage, e.cfg.Now().Sub(start), err)
		if err == nil {
			return resp, i, nil
		}
		if ctx.Err() != nil {
			return llm.Response{}, -1, ctx.Err()
		}
		e.cfg.Logger.Warn("ai: candidate failed, falling back",
			slog.String("task", task), slog.String("model", c.Model.ID), slog.Any("error", err))
		errs = append(errs, fmt.Errorf("%s: %w", c.Model.ID, err))
	}
	return llm.Response{}, -1, fmt.Errorf("all %d candidates failed for task %s: %w", len(candidates), task, errors.Join(errs...))
}

func (e *Engine) record(ctx context.Context, task string, c registry.Candidate, requestID string, usage llm.Usage, latency time.Duration, callErr error) {
	if e.cfg.Ledger == nil {
		return
	}
	caller, _ := CallerFrom(ctx)
	rec := ledger.Record{
		At:            e.cfg.Now(),
		Project:       e.cfg.Project,
		Channel:       caller.Channel,
		CallerID:      caller.ID,
		Task:          task,
		ModelID:       c.Model.ID,
		Provider:      c.Model.Provider,
		ProviderModel: c.Model.ModelID,
		RequestID:     requestID,
		Usage:         usage,
		CostUSD:       ledger.Cost(c.Model.Price, usage),
		LatencyMS:     latency.Milliseconds(),
		Status:        ledger.StatusOK,
	}
	if callErr != nil {
		rec.Status = ledger.StatusError
		rec.Error = callErr.Error()
	}
	// Ledger writes must not fail the answer; use a detached context so a
	// cancelled request still gets its cost logged.
	if err := e.cfg.Ledger.Record(context.WithoutCancel(ctx), rec); err != nil {
		e.cfg.Logger.Error("ai: ledger record failed", slog.Any("error", err))
	}
}

func (e *Engine) offeredTools(question string, caller Caller) []Tool {
	out := make([]Tool, 0, len(e.cfg.Tools))
	for _, t := range e.cfg.Tools {
		if g, ok := t.(Gated); ok && !g.Offered(question, caller) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// runTools executes every tool_use concurrently and returns all results,
// in the original order, for a single user message.
func (e *Engine) runTools(ctx context.Context, uses []llm.Block, byName map[string]Tool) []llm.Block {
	results := make([]llm.Block, len(uses))
	var wg sync.WaitGroup
	for i, use := range uses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tool, ok := byName[use.ToolName]
			if !ok {
				results[i] = llm.ToolResult(use.ToolUseID, "unknown tool: "+use.ToolName, true)
				return
			}
			out, err := tool.Execute(ctx, use.Input)
			if err != nil {
				results[i] = llm.ToolResult(use.ToolUseID, err.Error(), true)
				return
			}
			results[i] = llm.ToolResult(use.ToolUseID, out, false)
		}()
	}
	wg.Wait()
	return results
}

// buildSystem returns the cached stable prompt followed by uncached
// volatile blocks: context blocks, global memory, the caller's memory.
func (e *Engine) buildSystem(ctx context.Context, caller Caller) []llm.SystemBlock {
	system := []llm.SystemBlock{{Text: e.cfg.SystemPrompt, Cache: true}}
	if e.cfg.ContextBlocks != nil {
		for _, b := range e.cfg.ContextBlocks(ctx, caller) {
			if strings.TrimSpace(b) != "" {
				system = append(system, llm.SystemBlock{Text: b})
			}
		}
	}
	if e.cfg.GlobalMemory != nil {
		if entries, err := e.cfg.GlobalMemory.List(ctx); err != nil {
			e.cfg.Logger.Error("ai: load global memory failed", slog.Any("error", err))
		} else if len(entries) > 0 {
			var sb strings.Builder
			sb.WriteString("Durable facts and decisions previously recorded by stakeholders, applicable across all users:\n")
			for _, m := range entries {
				sb.WriteString("- ")
				sb.WriteString(m.Content)
				sb.WriteString("\n")
			}
			system = append(system, llm.SystemBlock{Text: sb.String()})
		}
	}
	if e.cfg.UserMemory != nil && caller.ID != "" {
		if content, found, err := e.cfg.UserMemory.Get(ctx, caller.ID); err != nil {
			e.cfg.Logger.Error("ai: load user memory failed", slog.Any("error", err), slog.String("caller", caller.ID))
		} else if found && strings.TrimSpace(content) != "" {
			system = append(system, llm.SystemBlock{
				Text: "What you know about this specific stakeholder from past conversations:\n" + content,
			})
		}
	}
	return system
}

func (e *Engine) seedHistory(ctx context.Context, callerID string) []llm.Message {
	if e.cfg.Conversation == nil || e.cfg.HistoryLimit <= 0 || callerID == "" {
		return nil
	}
	turns, err := e.cfg.Conversation.Recent(ctx, callerID, e.cfg.HistoryLimit)
	if err != nil {
		e.cfg.Logger.Error("ai: load conversation history failed", slog.Any("error", err), slog.String("caller", callerID))
		return nil
	}
	out := make([]llm.Message, 0, len(turns))
	for _, t := range turns {
		// The API rejects empty text blocks; skip any legacy empty rows.
		if strings.TrimSpace(t.Content) == "" {
			continue
		}
		switch t.Role {
		case TurnUser:
			out = append(out, llm.UserText(t.Content))
		case TurnAssistant:
			out = append(out, llm.AssistantText(t.Content))
		}
	}
	return out
}

// finish records the exchange and merges it into the caller's memory in
// the background. Failures are logged, never returned.
func (e *Engine) finish(ctx context.Context, caller Caller, question, answer string) {
	if e.cfg.Conversation != nil && caller.ID != "" && strings.TrimSpace(answer) != "" {
		if err := e.cfg.Conversation.Append(ctx, caller.ID, TurnUser, question); err != nil {
			e.cfg.Logger.Error("ai: append user turn failed", slog.Any("error", err))
		}
		if err := e.cfg.Conversation.Append(ctx, caller.ID, TurnAssistant, answer); err != nil {
			e.cfg.Logger.Error("ai: append assistant turn failed", slog.Any("error", err))
		}
	}
	if e.cfg.UserMemory != nil && caller.ID != "" && strings.TrimSpace(answer) != "" {
		go e.mergeUserMemory(caller, question, answer)
	}
}

// mergeUserMemory runs detached from the request (which is usually
// cancelled right after Ask returns) with its own timeout.
func (e *Engine) mergeUserMemory(caller Caller, question, answer string) {
	ctx, cancel := context.WithTimeout(WithCaller(context.Background(), caller), userMemoryMergeTimeout)
	defer cancel()

	existing, _, err := e.cfg.UserMemory.Get(ctx, caller.ID)
	if err != nil {
		e.cfg.Logger.Error("ai: load user memory failed", slog.Any("error", err), slog.String("caller", caller.ID))
		return
	}
	prompt := fmt.Sprintf(
		"You maintain a short, evolving profile of one specific stakeholder, built from their ongoing "+
			"conversation history with an internal data assistant. Update the profile below to fold in the "+
			"latest exchange: keep it concise (a few sentences), write it as standalone notes that will make "+
			"sense read out of context later, and drop anything that's no longer relevant rather than letting "+
			"it grow forever. Reply with ONLY the updated profile text and nothing else - no preamble, no "+
			"markdown headers.\n\n"+
			"Existing profile (empty if this is their first exchange):\n%s\n\n"+
			"Latest exchange:\nQ: %s\nA: %s",
		existing, question, answer,
	)
	resp, err := e.Complete(ctx, e.cfg.MemoryTask, llm.Request{Messages: []llm.Message{llm.UserText(prompt)}})
	if err != nil {
		e.cfg.Logger.Error("ai: merge user memory failed", slog.Any("error", err), slog.String("caller", caller.ID))
		return
	}
	merged := strings.TrimSpace(resp.Text())
	if merged == "" {
		return
	}
	if r := []rune(merged); len(r) > e.cfg.UserMemoryMaxLen {
		merged = string(r[:e.cfg.UserMemoryMaxLen])
	}
	if err := e.cfg.UserMemory.Upsert(ctx, caller.ID, merged); err != nil {
		e.cfg.Logger.Error("ai: save user memory failed", slog.Any("error", err), slog.String("caller", caller.ID))
	}
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
