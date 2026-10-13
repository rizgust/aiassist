package postgres

import (
	"context"
	"time"

	"github.com/Nuanu-com/aiassist/ledger"
)

// Ledger implements ledger.Store.
type Ledger struct{ DB DB }

func (s *Ledger) Record(ctx context.Context, r ledger.Record) error {
	at := r.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO ai_usage (at, project, channel, caller_id, task, model_id, provider,
		provider_model, request_id, tokens_in, tokens_out, tokens_cache_read, tokens_cache_write, cost_usd,
		latency_ms, status, error) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		at, r.Project, r.Channel, r.CallerID, r.Task, r.ModelID, r.Provider, r.ProviderModel, r.RequestID,
		r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.CacheReadTokens, r.Usage.CacheWriteTokens,
		r.CostUSD, r.LatencyMS, r.Status, r.Error)
	return err
}

func (s *Ledger) MonthSpendUSD(ctx context.Context, month time.Time, project, task string) (float64, error) {
	start := ledger.MonthStart(month)
	var total float64
	err := s.DB.QueryRow(ctx, `SELECT COALESCE(SUM(cost_usd), 0)::float8 FROM ai_usage
		WHERE project = $1 AND at >= $2 AND at < $3 AND ($4 = '' OR task = $4)`,
		project, start, start.AddDate(0, 1, 0), task).Scan(&total)
	return total, err
}

func (s *Ledger) Monthly(ctx context.Context, month time.Time, project string) ([]ledger.MonthlyRow, error) {
	start := ledger.MonthStart(month)
	rows, err := s.DB.Query(ctx, `SELECT task, model_id, COUNT(*),
		COUNT(*) FILTER (WHERE status = 'error'),
		COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0),
		COALESCE(SUM(tokens_cache_read),0), COALESCE(SUM(tokens_cache_write),0),
		COALESCE(SUM(cost_usd),0)::float8
		FROM ai_usage WHERE project = $1 AND at >= $2 AND at < $3
		GROUP BY task, model_id ORDER BY 9 DESC, task, model_id`,
		project, start, start.AddDate(0, 1, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.MonthlyRow
	for rows.Next() {
		r := ledger.MonthlyRow{Month: start, Project: project}
		if err := rows.Scan(&r.Task, &r.ModelID, &r.Calls, &r.Errors, &r.InputTokens, &r.OutputTokens,
			&r.CacheReadTokens, &r.CacheWriteTokens, &r.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var _ ledger.Store = (*Ledger)(nil)
