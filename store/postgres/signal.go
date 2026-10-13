package postgres

import (
	"context"
	"time"

	"github.com/Nuanu-com/aiassist/signal"
)

// Signals implements signal.Store.
type Signals struct{ DB DB }

func (s *Signals) Upsert(ctx context.Context, signals []signal.Signal) error {
	if len(signals) == 0 {
		return nil
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, sg := range signals {
		var payload any
		if len(sg.Payload) > 0 {
			payload = []byte(sg.Payload)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ai_signal (date, scope, kind, code, severity, title, payload, source)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (kind, source, scope, date, code, title)
			DO UPDATE SET severity = EXCLUDED.severity, payload = EXCLUDED.payload, fetched_at = NOW()`,
			sg.Date.Format("2006-01-02"), sg.Scope, sg.Kind, sg.Code, sg.Severity, sg.Title, payload, sg.Source); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Signals) Range(ctx context.Context, from, to time.Time, kinds ...string) ([]signal.Signal, error) {
	rows, err := s.DB.Query(ctx, `SELECT date, scope, kind, code, severity::float8, title, COALESCE(payload, 'null'::jsonb), source
		FROM ai_signal WHERE date BETWEEN $1 AND $2 AND (cardinality($3::text[]) = 0 OR kind = ANY($3))
		ORDER BY date, kind, code`, from.Format("2006-01-02"), to.Format("2006-01-02"), kinds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []signal.Signal
	for rows.Next() {
		var sg signal.Signal
		var payload []byte
		if err := rows.Scan(&sg.Date, &sg.Scope, &sg.Kind, &sg.Code, &sg.Severity, &sg.Title, &payload, &sg.Source); err != nil {
			return nil, err
		}
		if string(payload) != "null" {
			sg.Payload = payload
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

var _ signal.Store = (*Signals)(nil)
