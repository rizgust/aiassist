// Package signal is the context-signal framework: external facts (weather,
// events, holidays, calendar rules) that help explain primary data. Signals
// are cached so explanations are reproducible and providers are called once
// per day, not once per question.
package signal

import (
	"context"
	"encoding/json"
	"time"
)

// ScopeArea marks a signal that applies to the whole area (every venue).
const ScopeArea = "area"

// Signal is one dated fact.
type Signal struct {
	Date     time.Time       `json:"date"`  // calendar day, UTC midnight
	Scope    string          `json:"scope"` // ScopeArea or a project venue/department id
	Kind     string          `json:"kind"`  // provider kind: "calendar", "holiday", "weather", "event", ...
	Code     string          `json:"code"`  // taxonomy code, e.g. "PUBLIC_HOLIDAY", "EVENT_PUBLIC"
	Severity float64         `json:"severity"`
	Title    string          `json:"title"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Source   string          `json:"source"`
}

// Provider fetches signals for [from, to] inclusive.
type Provider interface {
	Kind() string
	Fetch(ctx context.Context, from, to time.Time) ([]Signal, error)
}

// Store caches signals.
type Store interface {
	Upsert(ctx context.Context, signals []Signal) error
	// Range returns cached signals in [from, to]; kinds empty means all.
	Range(ctx context.Context, from, to time.Time, kinds ...string) ([]Signal, error)
}
