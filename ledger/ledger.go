// Package ledger records every LLM call's token usage and cost, and rolls
// it up per month.
package ledger

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/rizgust/aiassist/llm"
	"github.com/rizgust/aiassist/registry"
)

// Status of one call.
const (
	StatusOK    = "ok"
	StatusError = "error"
)

// Record is one LLM call. A tool-use loop writes one Record per iteration,
// all sharing RequestID; a fallback writes an error Record followed by the
// Record of the model that succeeded.
type Record struct {
	At            time.Time
	Project       string
	Channel       string
	CallerID      string
	Task          string
	ModelID       string // registry id
	Provider      string
	ProviderModel string // provider's model id
	RequestID     string
	Usage         llm.Usage
	CostUSD       float64
	LatencyMS     int64
	Status        string
	Error         string
}

// MonthlyRow is one rollup line.
type MonthlyRow struct {
	Month            time.Time // first day of month, UTC
	Project          string
	Task             string
	ModelID          string
	Calls            int64
	Errors           int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
}

// Recorder is what the engine writes to.
type Recorder interface {
	Record(ctx context.Context, r Record) error
}

// Store is a full ledger backend: write, spend lookup for budgets, and
// monthly rollup for reports.
type Store interface {
	Recorder
	registry.SpendReader
	Monthly(ctx context.Context, month time.Time, project string) ([]MonthlyRow, error)
}

// Cost prices u at p.
func Cost(p registry.Pricing, u llm.Usage) float64 {
	const m = 1_000_000.0
	return float64(u.InputTokens)*p.InPerMTok/m +
		float64(u.OutputTokens)*p.OutPerMTok/m +
		float64(u.CacheReadTokens)*p.CacheReadPerMTok/m +
		float64(u.CacheWriteTokens)*p.CacheWritePerMTok/m
}

// MonthStart returns the first instant of t's month in UTC.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Memory is an in-process Store for tests and ephemeral deployments.
type Memory struct {
	mu      sync.Mutex
	records []Record
}

func (m *Memory) Record(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, r)
	return nil
}

// Records returns a copy of everything recorded.
func (m *Memory) Records() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.records...)
}

func (m *Memory) MonthSpendUSD(_ context.Context, month time.Time, project, task string) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start := MonthStart(month)
	var total float64
	for _, r := range m.records {
		if MonthStart(r.At).Equal(start) && r.Project == project && (task == "" || r.Task == task) {
			total += r.CostUSD
		}
	}
	return total, nil
}

func (m *Memory) Monthly(_ context.Context, month time.Time, project string) ([]MonthlyRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start := MonthStart(month)
	rows := map[[2]string]*MonthlyRow{}
	for _, r := range m.records {
		if !MonthStart(r.At).Equal(start) || r.Project != project {
			continue
		}
		key := [2]string{r.Task, r.ModelID}
		row, ok := rows[key]
		if !ok {
			row = &MonthlyRow{Month: start, Project: project, Task: r.Task, ModelID: r.ModelID}
			rows[key] = row
		}
		row.Calls++
		if r.Status == StatusError {
			row.Errors++
		}
		row.InputTokens += r.Usage.InputTokens
		row.OutputTokens += r.Usage.OutputTokens
		row.CacheReadTokens += r.Usage.CacheReadTokens
		row.CacheWriteTokens += r.Usage.CacheWriteTokens
		row.CostUSD += r.CostUSD
	}
	out := make([]MonthlyRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		return out[i].Task+out[i].ModelID < out[j].Task+out[j].ModelID
	})
	return out, nil
}

var _ Store = (*Memory)(nil)
