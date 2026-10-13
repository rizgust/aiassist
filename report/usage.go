package report

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Nuanu-com/aiassist/core"
	"github.com/Nuanu-com/aiassist/ledger"
)

// UsageModule is the built-in "ai.usage" report: a month's AI token usage
// and cost by task and model, compared with the previous month.
type UsageModule struct {
	Ledger  ledger.Store
	Project string
	Now     func() time.Time // default time.Now
}

// UsageReport is UsageModule's data.
type UsageReport struct {
	Month             string              `json:"month"`
	Project           string              `json:"project"`
	TotalCostUSD      float64             `json:"total_cost_usd"`
	PrevMonthCostUSD  float64             `json:"prev_month_cost_usd"`
	TotalCalls        int64               `json:"total_calls"`
	TotalErrors       int64               `json:"total_errors"`
	TotalInputTokens  int64               `json:"total_input_tokens"`
	TotalOutputTokens int64               `json:"total_output_tokens"`
	Rows              []ledger.MonthlyRow `json:"rows"`
}

func (u *UsageModule) ID() string    { return "ai.usage" }
func (u *UsageModule) Title() string { return "AI Usage Report" }
func (u *UsageModule) Description() string {
	return "Monthly AI token usage and cost (USD) for this assistant, broken down by task and model, with the " +
		"previous month's total for comparison. Defaults to the current month."
}

func (u *UsageModule) Params() json.RawMessage {
	return core.Schema(map[string]any{
		"month": map[string]any{"type": "string", "description": "Month as YYYY-MM. Omit for the current month."},
	})
}

func (u *UsageModule) Formats() []Format { return []Format{FormatText, FormatJSON} }

func (u *UsageModule) Build(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Month string `json:"month"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, fmt.Errorf("invalid ai.usage params: %w", err)
		}
	}
	now := time.Now
	if u.Now != nil {
		now = u.Now
	}
	month := ledger.MonthStart(now())
	if in.Month != "" {
		t, err := time.Parse("2006-01", in.Month)
		if err != nil {
			return nil, fmt.Errorf("month must be YYYY-MM: %w", err)
		}
		month = t
	}

	rows, err := u.Ledger.Monthly(ctx, month, u.Project)
	if err != nil {
		return nil, err
	}
	prev, err := u.Ledger.MonthSpendUSD(ctx, month.AddDate(0, -1, 0), u.Project, "")
	if err != nil {
		return nil, err
	}
	rep := UsageReport{Month: month.Format("2006-01"), Project: u.Project, PrevMonthCostUSD: prev, Rows: rows}
	for _, r := range rows {
		rep.TotalCostUSD += r.CostUSD
		rep.TotalCalls += r.Calls
		rep.TotalErrors += r.Errors
		rep.TotalInputTokens += r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens
		rep.TotalOutputTokens += r.OutputTokens
	}
	return rep, nil
}

func (u *UsageModule) Render(_ context.Context, data any, f Format) (Rendered, error) {
	rep, ok := data.(UsageReport)
	if !ok {
		return Rendered{}, fmt.Errorf("ai.usage: unexpected data %T", data)
	}
	switch f {
	case FormatJSON:
		b, err := json.MarshalIndent(rep, "", "  ")
		return Rendered{Format: f, MIME: "application/json", FileName: "ai-usage-" + rep.Month + ".json", Data: b}, err
	case FormatText:
		var sb strings.Builder
		fmt.Fprintf(&sb, "AI usage %s (%s)\n", rep.Month, rep.Project)
		fmt.Fprintf(&sb, "Total: $%.2f, %d calls, %d errors (previous month $%.2f)\n",
			rep.TotalCostUSD, rep.TotalCalls, rep.TotalErrors, rep.PrevMonthCostUSD)
		fmt.Fprintf(&sb, "Tokens: %d in, %d out\n", rep.TotalInputTokens, rep.TotalOutputTokens)
		for _, r := range rep.Rows {
			fmt.Fprintf(&sb, "- %s / %s: $%.2f, %d calls\n", r.Task, r.ModelID, r.CostUSD, r.Calls)
		}
		return Rendered{Format: f, MIME: "text/plain; charset=utf-8", Data: []byte(sb.String())}, nil
	default:
		return Rendered{}, fmt.Errorf("%w: %s", ErrUnsupportedFormat, f)
	}
}

var _ Module = (*UsageModule)(nil)
