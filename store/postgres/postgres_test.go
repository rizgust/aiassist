package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nuanu-com/aiassist/core"
	"github.com/Nuanu-com/aiassist/ledger"
	"github.com/Nuanu-com/aiassist/llm"
	"github.com/Nuanu-com/aiassist/registry"
	"github.com/Nuanu-com/aiassist/signal"
)

// Integration test against a throwaway database:
//
//	AIASSIST_TEST_DSN=postgres://postgres:postgres@localhost:5432/aiassist_test?sslmode=disable go test ./store/postgres/
//
// It drops and recreates every ai_* table, so never point it at a real DB.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AIASSIST_TEST_DSN")
	if dsn == "" {
		t.Skip("AIASSIST_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, tbl := range []string{"ai_signal", "ai_user_memory", "ai_global_memory", "ai_conversation", "ai_usage",
		"ai_budget", "ai_task_route", "ai_model", "ai_schema_migrations"} {
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+tbl+" CASCADE"); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil { // idempotent
		t.Fatal(err)
	}
	return pool
}

func TestPostgresStores(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()

	reg := &Registry{DB: pool}
	if err := reg.Seed(ctx, registry.ClaudeModels(), registry.DefaultRoutes()); err != nil {
		t.Fatal(err)
	}
	// Re-seeding must not duplicate routes or overwrite edited prices.
	if _, err := pool.Exec(ctx, `UPDATE ai_model SET price_in_per_mtok = 9 WHERE id = 'sonnet-5-5'`); err != nil {
		t.Fatal(err)
	}
	if err := reg.Seed(ctx, registry.ClaudeModels(), registry.DefaultRoutes()); err != nil {
		t.Fatal(err)
	}
	routes, err := reg.Routes(ctx, registry.TaskChat)
	if err != nil || len(routes) != 2 {
		t.Fatalf("chat routes = %+v, %v", routes, err)
	}
	models, _ := reg.Models(ctx)
	for _, m := range models {
		if m.ID == "sonnet-5-5" && m.Price.InPerMTok != 9 {
			t.Fatalf("seed overwrote edited price: %+v", m)
		}
	}
	router := &registry.Router{Store: reg, Project: "sd"}
	if c, err := router.Candidates(ctx, registry.TaskChat); err != nil || c[0].Model.ID != "sonnet-5-5" {
		t.Fatalf("candidates = %+v, %v", c, err)
	}

	led := &Ledger{DB: pool}
	now := time.Now()
	for _, r := range []ledger.Record{
		{At: now, Project: "sd", Task: "chat", ModelID: "sonnet-5-5", Provider: "anthropic", ProviderModel: "claude-sonnet-5-5",
			RequestID: "r1", Usage: llm.Usage{InputTokens: 100, OutputTokens: 10}, CostUSD: 0.5, Status: ledger.StatusOK},
		{At: now, Project: "sd", Task: "chat", ModelID: "sonnet-5-5", Provider: "anthropic", ProviderModel: "claude-sonnet-5-5",
			RequestID: "r1", CostUSD: 0.25, Status: ledger.StatusError, Error: "529"},
	} {
		if err := led.Record(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if spend, _ := led.MonthSpendUSD(ctx, now, "sd", ""); spend != 0.75 {
		t.Fatalf("spend = %v", spend)
	}
	rows, err := led.Monthly(ctx, now, "sd")
	if err != nil || len(rows) != 1 || rows[0].Calls != 2 || rows[0].Errors != 1 || rows[0].InputTokens != 100 {
		t.Fatalf("monthly = %+v, %v", rows, err)
	}

	conv := &Conversation{DB: pool}
	for i, c := range []string{"q1", "a1", "q2", "a2"} {
		role := core.TurnUser
		if i%2 == 1 {
			role = core.TurnAssistant
		}
		_ = conv.Append(ctx, "tg:1", role, c)
	}
	turns, _ := conv.Recent(ctx, "tg:1", 3)
	if len(turns) != 3 || turns[0].Content != "a1" || turns[2].Content != "a2" {
		t.Fatalf("recent = %+v", turns)
	}

	um := &UserMemory{DB: pool}
	if _, found, _ := um.Get(ctx, "tg:1"); found {
		t.Fatal("expected no user memory yet")
	}
	_ = um.Upsert(ctx, "tg:1", "v1")
	_ = um.Upsert(ctx, "tg:1", "v2")
	if c, found, _ := um.Get(ctx, "tg:1"); !found || c != "v2" {
		t.Fatalf("user memory = %q %v", c, found)
	}

	gm := &GlobalMemory{DB: pool}
	if e, err := gm.Save(ctx, "fact", "tg:1"); err != nil || e.ID == 0 {
		t.Fatalf("save = %+v %v", e, err)
	}

	sig := &Signals{DB: pool}
	d := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	s := signal.Signal{Date: d, Scope: signal.ScopeArea, Kind: "event", Code: "EVENT_PUBLIC", Title: "Art & Bali 2026",
		Severity: 1, Source: "cockpit", Payload: json.RawMessage(`{"is_important":true}`)}
	if err := sig.Upsert(ctx, []signal.Signal{s, s}); err != nil {
		t.Fatal(err)
	}
	got, err := sig.Range(ctx, d, d, "event")
	if err != nil || len(got) != 1 || got[0].Title != "Art & Bali 2026" || string(got[0].Payload) != `{"is_important": true}` {
		t.Fatalf("signals = %+v %v", got, err)
	}
	if all, _ := sig.Range(ctx, d, d); len(all) != 1 {
		t.Fatalf("range without kinds = %d", len(all))
	}
}
