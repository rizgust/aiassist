// Package registry holds the model catalogue and per-task routes, and picks
// which registered model serves a task: weighted within a priority tier,
// falling back tier by tier, subject to monthly budgets.
package registry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"time"
)

// Well-known task names. Projects may add their own.
const (
	TaskChat            = "chat"
	TaskMemoryMerge     = "memory_merge"
	TaskSummarizeReport = "summarize_report"
	TaskIntentRoute     = "intent_route"
	TaskExplainSpike    = "explain_spike"
	TaskLabelSuggest    = "label_suggest"
	TaskEvalJudge       = "eval_judge"
)

// Pricing is USD per million tokens.
type Pricing struct {
	InPerMTok         float64
	OutPerMTok        float64
	CacheReadPerMTok  float64
	CacheWritePerMTok float64
}

// Model is one registered model. ID is the registry's own key (e.g.
// "sonnet-5-5"); ModelID is what the provider API expects.
type Model struct {
	ID          string
	Provider    string
	ModelID     string
	DisplayName string
	Price       Pricing
	Enabled     bool
}

// Route maps a task to a model. Lower Priority is tried first; within one
// priority tier a model is picked at random proportional to Weight.
type Route struct {
	ID        int64
	Task      string
	ModelID   string // → Model.ID
	Weight    int
	Priority  int
	MaxTokens int64
	Effort    string // "" = model default
	Enabled   bool
}

// BudgetAction is what happens when a monthly budget is exceeded.
type BudgetAction string

const (
	// BudgetWarn only invokes the router's OnBudget callback.
	BudgetWarn BudgetAction = "warn"
	// BudgetDowngrade keeps only the last (cheapest, by convention)
	// priority tier.
	BudgetDowngrade BudgetAction = "downgrade"
	// BudgetBlock refuses the call with ErrBudgetExceeded.
	BudgetBlock BudgetAction = "block"
)

// Budget scopes.
const (
	ScopeProject = "project"
	ScopeTask    = "task"
)

// Budget is a monthly USD limit for a whole project (Scope=project,
// ScopeKey=project name) or one task (Scope=task, ScopeKey=task name).
type Budget struct {
	Scope    string
	ScopeKey string
	LimitUSD float64
	Action   BudgetAction
	Enabled  bool
}

// ErrBudgetExceeded is returned when a BudgetBlock budget is exhausted.
var ErrBudgetExceeded = errors.New("ai budget exceeded")

// ErrNoRoute is returned when a task has no enabled route to an enabled model.
var ErrNoRoute = errors.New("no enabled route for task")

// Store reads the registry. store/postgres implements it; Static is an
// in-memory implementation for config-driven projects and tests.
type Store interface {
	Routes(ctx context.Context, task string) ([]Route, error)
	Models(ctx context.Context) ([]Model, error)
	Budgets(ctx context.Context) ([]Budget, error)
}

// SpendReader reports month-to-date spend. ledger stores implement it.
// task == "" means the whole project.
type SpendReader interface {
	MonthSpendUSD(ctx context.Context, month time.Time, project, task string) (float64, error)
}

// Candidate is one routed model, in the order it should be tried.
type Candidate struct {
	Route Route
	Model Model
}

// Router picks candidates for a task.
type Router struct {
	Store   Store
	Spend   SpendReader // optional; nil disables budgets
	Project string

	// OnBudget is called for every exceeded budget (any action). Optional.
	OnBudget func(b Budget, spentUSD float64)

	// Rand returns a value in [0, n); defaults to math/rand/v2.IntN.
	Rand func(n int) int
	// Now defaults to time.Now.
	Now func() time.Time
}

// Candidates returns the models to try for task, best first: tiers by
// ascending priority, each tier ordered by weighted random draw without
// replacement. Budgets are applied before returning.
func (r *Router) Candidates(ctx context.Context, task string) ([]Candidate, error) {
	routes, err := r.Store.Routes(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("load routes for %s: %w", task, err)
	}
	models, err := r.Store.Models(ctx)
	if err != nil {
		return nil, fmt.Errorf("load models: %w", err)
	}
	byID := make(map[string]Model, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}

	tiers := map[int][]Candidate{}
	for _, rt := range routes {
		m, ok := byID[rt.ModelID]
		if !rt.Enabled || rt.Task != task || rt.Weight <= 0 || !ok || !m.Enabled {
			continue
		}
		tiers[rt.Priority] = append(tiers[rt.Priority], Candidate{Route: rt, Model: m})
	}
	if len(tiers) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoRoute, task)
	}
	priorities := make([]int, 0, len(tiers))
	for p := range tiers {
		priorities = append(priorities, p)
	}
	sort.Ints(priorities)

	downgrade, err := r.applyBudgets(ctx, task)
	if err != nil {
		return nil, err
	}
	if downgrade {
		priorities = priorities[len(priorities)-1:]
	}

	var out []Candidate
	for _, p := range priorities {
		out = append(out, r.weightedOrder(tiers[p])...)
	}
	return out, nil
}

func (r *Router) applyBudgets(ctx context.Context, task string) (downgrade bool, err error) {
	if r.Spend == nil {
		return false, nil
	}
	budgets, err := r.Store.Budgets(ctx)
	if err != nil {
		return false, fmt.Errorf("load budgets: %w", err)
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	month := now()
	for _, b := range budgets {
		if !b.Enabled {
			continue
		}
		var spendTask string
		switch {
		case b.Scope == ScopeProject && b.ScopeKey == r.Project:
			spendTask = ""
		case b.Scope == ScopeTask && b.ScopeKey == task:
			spendTask = task
		default:
			continue
		}
		spent, err := r.Spend.MonthSpendUSD(ctx, month, r.Project, spendTask)
		if err != nil {
			return false, fmt.Errorf("read spend: %w", err)
		}
		if spent < b.LimitUSD {
			continue
		}
		if r.OnBudget != nil {
			r.OnBudget(b, spent)
		}
		switch b.Action {
		case BudgetBlock:
			return false, fmt.Errorf("%w: %s %s spent $%.2f of $%.2f", ErrBudgetExceeded, b.Scope, b.ScopeKey, spent, b.LimitUSD)
		case BudgetDowngrade:
			downgrade = true
		}
	}
	return downgrade, nil
}

func (r *Router) weightedOrder(in []Candidate) []Candidate {
	pick := rand.IntN
	if r.Rand != nil {
		pick = r.Rand
	}
	pool := append([]Candidate(nil), in...)
	out := make([]Candidate, 0, len(pool))
	for len(pool) > 0 {
		total := 0
		for _, c := range pool {
			total += c.Route.Weight
		}
		n := pick(total)
		i := 0
		for ; i < len(pool)-1; i++ {
			n -= pool[i].Route.Weight
			if n < 0 {
				break
			}
		}
		out = append(out, pool[i])
		pool = append(pool[:i], pool[i+1:]...)
	}
	return out
}

// Static is an in-memory Store.
type Static struct {
	ModelList  []Model
	RouteList  []Route
	BudgetList []Budget
}

func (s *Static) Routes(_ context.Context, task string) ([]Route, error) {
	var out []Route
	for _, r := range s.RouteList {
		if r.Task == task {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Static) Models(context.Context) ([]Model, error)   { return s.ModelList, nil }
func (s *Static) Budgets(context.Context) ([]Budget, error) { return s.BudgetList, nil }

var _ Store = (*Static)(nil)
