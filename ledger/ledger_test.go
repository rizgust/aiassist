package ledger

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Nuanu-com/aiassist/llm"
	"github.com/Nuanu-com/aiassist/registry"
)

func TestCost(t *testing.T) {
	p := registry.Pricing{InPerMTok: 2, OutPerMTok: 10, CacheReadPerMTok: 0.2, CacheWritePerMTok: 2.5}
	u := llm.Usage{InputTokens: 1_000_000, OutputTokens: 500_000, CacheReadTokens: 2_000_000, CacheWriteTokens: 400_000}
	// 2 + 5 + 0.4 + 1.0
	if got := Cost(p, u); math.Abs(got-8.4) > 1e-9 {
		t.Fatalf("Cost = %v, want 8.4", got)
	}
}

func TestMemory_MonthlyAndSpend(t *testing.T) {
	ctx := context.Background()
	m := &Memory{}
	oct := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
	sep := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	recs := []Record{
		{At: oct, Project: "sd", Task: "chat", ModelID: "sonnet", Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}, CostUSD: 1.5, Status: StatusOK},
		{At: oct, Project: "sd", Task: "chat", ModelID: "sonnet", CostUSD: 0.5, Status: StatusError},
		{At: oct, Project: "sd", Task: "memory_merge", ModelID: "haiku", CostUSD: 0.01, Status: StatusOK},
		{At: sep, Project: "sd", Task: "chat", ModelID: "sonnet", CostUSD: 7, Status: StatusOK},
		{At: oct, Project: "other", Task: "chat", ModelID: "sonnet", CostUSD: 100, Status: StatusOK},
	}
	for _, r := range recs {
		_ = m.Record(ctx, r)
	}

	if got, _ := m.MonthSpendUSD(ctx, oct, "sd", ""); math.Abs(got-2.01) > 1e-9 {
		t.Fatalf("project spend = %v, want 2.01", got)
	}
	if got, _ := m.MonthSpendUSD(ctx, oct, "sd", "chat"); got != 2 {
		t.Fatalf("task spend = %v, want 2", got)
	}

	rows, _ := m.Monthly(ctx, oct, "sd")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if r := rows[0]; r.Task != "chat" || r.Calls != 2 || r.Errors != 1 || r.InputTokens != 10 || r.CostUSD != 2 {
		t.Fatalf("chat row = %+v", r)
	}
}
