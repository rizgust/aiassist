package registry

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSpend map[string]float64 // task ("" = project) → spend

func (f fakeSpend) MonthSpendUSD(_ context.Context, _ time.Time, _ string, task string) (float64, error) {
	return f[task], nil
}

func testStore() *Static {
	return &Static{
		ModelList: []Model{
			{ID: "opus", Provider: "anthropic", ModelID: "claude-opus-5-5", Enabled: true},
			{ID: "sonnet", Provider: "anthropic", ModelID: "claude-sonnet-5-5", Enabled: true},
			{ID: "haiku", Provider: "anthropic", ModelID: "claude-haiku-5-5", Enabled: true},
			{ID: "off", Provider: "anthropic", ModelID: "x", Enabled: false},
		},
		RouteList: []Route{
			{Task: "chat", ModelID: "sonnet", Weight: 80, Priority: 0, Enabled: true},
			{Task: "chat", ModelID: "opus", Weight: 20, Priority: 0, Enabled: true},
			{Task: "chat", ModelID: "haiku", Weight: 1, Priority: 1, Enabled: true},
			{Task: "chat", ModelID: "off", Weight: 1, Priority: 0, Enabled: true},   // disabled model
			{Task: "chat", ModelID: "opus", Weight: 1, Priority: 2, Enabled: false}, // disabled route
		},
	}
}

func ids(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Model.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCandidates_WeightedWithinTierThenFallbackTier(t *testing.T) {
	// Rand returning 0 always picks the first remaining candidate.
	r := &Router{Store: testStore(), Rand: func(int) int { return 0 }}
	got, err := r.Candidates(context.Background(), "chat")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"sonnet", "opus", "haiku"}; !equal(ids(got), want) {
		t.Fatalf("got %v want %v", ids(got), want)
	}

	// A draw of 85 (of total 100) lands in opus's 20-weight slice.
	r.Rand = func(n int) int {
		if n == 100 {
			return 85
		}
		return 0
	}
	got, _ = r.Candidates(context.Background(), "chat")
	if want := []string{"opus", "sonnet", "haiku"}; !equal(ids(got), want) {
		t.Fatalf("got %v want %v", ids(got), want)
	}
}

func TestCandidates_WeightDistribution(t *testing.T) {
	r := &Router{Store: testStore()}
	first := map[string]int{}
	for i := 0; i < 5000; i++ {
		got, err := r.Candidates(context.Background(), "chat")
		if err != nil {
			t.Fatal(err)
		}
		first[got[0].Model.ID]++
	}
	share := float64(first["sonnet"]) / 5000
	if share < 0.75 || share > 0.85 {
		t.Fatalf("sonnet first-pick share %.3f, want ≈0.80", share)
	}
}

func TestCandidates_NoRoute(t *testing.T) {
	r := &Router{Store: testStore()}
	if _, err := r.Candidates(context.Background(), "unknown"); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("want ErrNoRoute, got %v", err)
	}
}

func TestCandidates_Budgets(t *testing.T) {
	store := testStore()
	store.BudgetList = []Budget{{Scope: ScopeTask, ScopeKey: "chat", LimitUSD: 10, Action: BudgetDowngrade, Enabled: true}}

	var warned []Budget
	r := &Router{Store: store, Project: "satudata", Spend: fakeSpend{"chat": 12}, Rand: func(int) int { return 0 },
		OnBudget: func(b Budget, _ float64) { warned = append(warned, b) }}

	got, err := r.Candidates(context.Background(), "chat")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"haiku"}; !equal(ids(got), want) {
		t.Fatalf("downgrade: got %v want %v", ids(got), want)
	}
	if len(warned) != 1 {
		t.Fatalf("OnBudget calls = %d, want 1", len(warned))
	}

	store.BudgetList = []Budget{{Scope: ScopeProject, ScopeKey: "satudata", LimitUSD: 50, Action: BudgetBlock, Enabled: true}}
	r.Spend = fakeSpend{"": 50}
	if _, err := r.Candidates(context.Background(), "chat"); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("block: want ErrBudgetExceeded, got %v", err)
	}

	// Under the limit, or a budget for another project: untouched.
	r.Spend = fakeSpend{"": 10}
	if got, err := r.Candidates(context.Background(), "chat"); err != nil || len(got) != 3 {
		t.Fatalf("under limit: got %v, %v", ids(got), err)
	}
	store.BudgetList[0].ScopeKey = "other"
	r.Spend = fakeSpend{"": 999}
	if got, err := r.Candidates(context.Background(), "chat"); err != nil || len(got) != 3 {
		t.Fatalf("other project: got %v, %v", ids(got), err)
	}
}

func TestDefaults_RoutesPointAtRegisteredModels(t *testing.T) {
	models := map[string]bool{}
	for _, m := range ClaudeModels() {
		models[m.ID] = true
	}
	for _, r := range DefaultRoutes() {
		if !models[r.ModelID] {
			t.Errorf("route %s → unknown model %s", r.Task, r.ModelID)
		}
	}
}
