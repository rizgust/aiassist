package postgres

import (
	"context"
	"fmt"

	"github.com/Nuanu-com/aiassist/registry"
)

// Registry implements registry.Store.
type Registry struct{ DB DB }

func (s *Registry) Models(ctx context.Context) ([]registry.Model, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, provider, model_id, display_name,
		price_in_per_mtok::float8, price_out_per_mtok::float8,
		price_cache_read_per_mtok::float8, price_cache_write_per_mtok::float8, enabled
		FROM ai_model ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []registry.Model
	for rows.Next() {
		var m registry.Model
		if err := rows.Scan(&m.ID, &m.Provider, &m.ModelID, &m.DisplayName,
			&m.Price.InPerMTok, &m.Price.OutPerMTok, &m.Price.CacheReadPerMTok, &m.Price.CacheWritePerMTok,
			&m.Enabled); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Registry) Routes(ctx context.Context, task string) ([]registry.Route, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, task, model_id, weight, priority, max_tokens, effort, enabled
		FROM ai_task_route WHERE task = $1 ORDER BY priority, id`, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []registry.Route
	for rows.Next() {
		var r registry.Route
		if err := rows.Scan(&r.ID, &r.Task, &r.ModelID, &r.Weight, &r.Priority, &r.MaxTokens, &r.Effort, &r.Enabled); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Registry) Budgets(ctx context.Context) ([]registry.Budget, error) {
	rows, err := s.DB.Query(ctx, `SELECT scope, scope_key, limit_usd::float8, action, enabled FROM ai_budget`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []registry.Budget
	for rows.Next() {
		var b registry.Budget
		var action string
		if err := rows.Scan(&b.Scope, &b.ScopeKey, &b.LimitUSD, &action, &b.Enabled); err != nil {
			return nil, err
		}
		b.Action = registry.BudgetAction(action)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Seed inserts models that don't exist yet (existing rows, including
// edited prices, are left alone) and routes for any task that has none, so
// it is safe to call on every startup without clobbering tuning done in
// the database.
func (s *Registry) Seed(ctx context.Context, models []registry.Model, routes []registry.Route) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, m := range models {
		if _, err := tx.Exec(ctx, `INSERT INTO ai_model (id, provider, model_id, display_name,
			price_in_per_mtok, price_out_per_mtok, price_cache_read_per_mtok, price_cache_write_per_mtok, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING`,
			m.ID, m.Provider, m.ModelID, m.DisplayName,
			m.Price.InPerMTok, m.Price.OutPerMTok, m.Price.CacheReadPerMTok, m.Price.CacheWritePerMTok, m.Enabled); err != nil {
			return fmt.Errorf("seed model %s: %w", m.ID, err)
		}
	}

	seeded := map[string]bool{}
	for _, r := range routes {
		if _, done := seeded[r.Task]; !done {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ai_task_route WHERE task = $1)`, r.Task).Scan(&exists); err != nil {
				return err
			}
			seeded[r.Task] = !exists
		}
		if !seeded[r.Task] {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ai_task_route (task, model_id, weight, priority, max_tokens, effort, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			r.Task, r.ModelID, r.Weight, r.Priority, r.MaxTokens, r.Effort, r.Enabled); err != nil {
			return fmt.Errorf("seed route %s→%s: %w", r.Task, r.ModelID, err)
		}
	}
	return tx.Commit(ctx)
}

var _ registry.Store = (*Registry)(nil)
