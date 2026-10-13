# aiassist

A reusable AI assistant core for Nuanu projects. Import it, run its migrations, register your project's **pack** (tools, reports, prompt), attach a channel, and you have a tool-calling assistant with:

- **Model registry and task routing.** Models are registered as data. Each task (`chat`, `explain_spike`, `memory_merge`, …) routes to models by **weight** (A/B testing) and **priority** (fallback order).
- **Fallback.** A provider error or a safety refusal moves to the next routed model automatically.
- **Token and cost ledger.** One row per LLM call, priced at call time, with a monthly rollup and month-to-date budgets (`warn` / `downgrade` / `block`).
- **Attachable reports.** One module becomes a `get_<id>` data tool and a keyword-gated `send_<id>` file tool on every channel.
- **Memory.** Per-caller conversation history, cross-caller global facts, and an evolving per-caller profile merged in the background on a cheap model.
- **Context signals.** A framework and cache for weather, events, holidays and other facts that help explain your data.

Design rationale: `nuanu-satudata/_TASKS/AI_ASSISTANT_TEMPLATE.md`.

## Packages

| Package | What it is |
|---|---|
| `llm` | Provider-neutral request/response types and the `Provider` interface |
| `llm/anthropic` | Anthropic Messages API adapter (v1 ships Anthropic only) |
| `registry` | Models, routes, budgets, the `Router`, and default Claude catalogue and routes |
| `ledger` | Usage `Record`, `Cost`, monthly rollup, in-memory store |
| `core` | `Engine` (tool loop, routing, ledgering, memory), `Tool`, gates, `Caller`, `Deliverer`, built-in tools |
| `report` | `Module` framework, `Registry.Tools`, built-in `ai.usage` report |
| `signal` | Context-signal types and the `Provider` / `Store` interfaces |
| `channel` | `Channel` interface (Telegram, MCP and HTTP adapters live in consumers for now) |
| `store/postgres` | pgx implementations of every store, with embedded migrations and a runner |

## Install in a project

```go
import (
    "github.com/Nuanu-com/aiassist/core"
    "github.com/Nuanu-com/aiassist/llm"
    "github.com/Nuanu-com/aiassist/llm/anthropic"
    "github.com/Nuanu-com/aiassist/registry"
    "github.com/Nuanu-com/aiassist/report"
    pg "github.com/Nuanu-com/aiassist/store/postgres"
)

pool, _ := pgxpool.New(ctx, os.Getenv("ASSISTANT_DB_DSN"))
if err := pg.Migrate(ctx, pool); err != nil { ... }            // creates ai_* tables, idempotent

reg := &pg.Registry{DB: pool}
_ = reg.Seed(ctx, registry.ClaudeModels(), registry.DefaultRoutes()) // inserts only what's missing
led := &pg.Ledger{DB: pool}

reports, _ := report.NewRegistry(
    &report.UsageModule{Ledger: led, Project: "satudata"},
    // your pack's modules, e.g. &spike.Module{...}
)

engine, err := core.New(core.Config{
    Project:      "satudata",
    SystemPrompt: satudataPrompt,                               // stable text only; it is prompt-cached
    Tools: append(reports.Tools(report.ToolOptions{}),
        core.NewSaveGlobalMemoryTool(&pg.GlobalMemory{DB: pool}),
        core.NewEscalateTool("ask_the_matrix", matrixDescription, escalator),
    ),
    Router:       &registry.Router{Store: reg, Spend: led, Project: "satudata", OnBudget: alertMatrix},
    Providers:    []llm.Provider{anthropic.NewWithAPIKey(os.Getenv("ANTHROPIC_API_KEY"))},
    Ledger:       led,
    Conversation: &pg.Conversation{DB: pool},
    GlobalMemory: &pg.GlobalMemory{DB: pool},
    UserMemory:   &pg.UserMemory{DB: pool},
    HistoryLimit: 20,
    ContextBlocks: func(ctx context.Context, c core.Caller) []string {
        return []string{"Today's business date (Bali time) is " + today() + "."}
    },
})
```

### Serving a request from a channel

```go
caller := core.Caller{ID: fmt.Sprintf("tg:%d", msg.From.Id), Username: msg.From.Username,
    Channel: "telegram", Privileged: isMaster(msg.From.Id)}
ctx = core.WithDeliverer(ctx, telegramDeliverer) // lets send_* tools push files to this chat
answer, err := engine.Ask(ctx, caller, msg.Text)
```

Tools read the asker with `core.CallerFrom(ctx)`. Identity never travels through tool input, so the model can't spoof it.

### One-shot tasks

Non-conversational work (spike explanations, summaries, eval judging) goes through the same routing, fallback and ledger:

```go
resp, err := engine.Complete(ctx, registry.TaskExplainSpike, llm.Request{
    System:   []llm.SystemBlock{{Text: spikePrompt, Cache: true}},
    Messages: []llm.Message{llm.UserText(evidenceJSON)},
})
```

## Tuning models without a deploy

Routing is data. Example: run an 80/20 A/B test on `chat`, then cap spend.

```sql
-- add Opus as a 20% arm in chat's primary tier
INSERT INTO ai_task_route (task, model_id, weight, priority, max_tokens) VALUES ('chat', 'opus-5-5', 1, 0, 16000);
UPDATE ai_task_route SET weight = 4 WHERE task = 'chat' AND model_id = 'sonnet-5-5' AND priority = 0;

-- monthly budgets
INSERT INTO ai_budget (scope, scope_key, limit_usd, action) VALUES ('project', 'satudata', 200, 'warn');
INSERT INTO ai_budget (scope, scope_key, limit_usd, action) VALUES ('task', 'chat', 150, 'downgrade');
```

- **Downgrade** keeps only the task's last priority tier, so put the cheapest model last.
- **Route fields:** `effort` is `''` / `low` / `medium` / `high` / `xhigh` / `max`. There is no temperature, because current Claude models reject non-default sampling parameters.

## Usage and cost

```sql
SELECT task, model_id, COUNT(*) calls, SUM(cost_usd) usd
FROM ai_usage WHERE project = 'satudata' AND at >= date_trunc('month', now())
GROUP BY 1, 2 ORDER BY usd DESC;
```

The same numbers are available to users through the built-in `ai.usage` report: `get_ai_usage` / `send_ai_usage`.

**Pricing:** `registry.ClaudeModels()` carries first-party prices as of 2026-10. Confirm them against the Anthropic pricing page. Edit `ai_model` rows to change prices; `Seed` never overwrites existing rows.

## Writing a pack

- **Tool:** implement `core.Tool`, or use `core.FuncTool`. Wrap it with `core.WithGate` to offer it only for some questions or callers (`core.KeywordGate`, `core.PrivilegedOnly`).
- **Report:** implement `report.Module` (`Build` once, `Render` per format) and register it. `get_*` and `send_*` tools are generated for it. `send_*` dedups identical params within one question.
- **Signal provider:** implement `signal.Provider`, and cache results with `pg.Signals`.

## Development

```sh
go test ./...
AIASSIST_TEST_DSN=postgres://postgres:postgres@localhost:5432/aiassist_test?sslmode=disable go test ./store/postgres/
```

**Warning:** the Postgres test drops every `ai_*` table. Only point it at a throwaway database.
