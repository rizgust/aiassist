// Package ladder runs the diagnosis ladder: an ordered set of checks that
// turns "why did revenue move?" into an evidence pack. Levels go from cheap
// and certain (data integrity) to expensive and uncertain (news). Checks
// return findings, not conclusions; the runner validates them and the LLM
// explains only from what survives.
package ladder

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Levels, in run order.
const (
	LevelIntegrity  = 0
	LevelCalendar   = 1
	LevelEvents     = 2
	LevelEnviron    = 3
	LevelDisruption = 4
	LevelPattern    = 5
	LevelNews       = 6
)

// Direction of the move being explained.
type Direction string

const (
	Up   Direction = "up"
	Down Direction = "down"
)

// Scope of a finding's cause.
const (
	ScopeVenue    = "venue"
	ScopeArea     = "area" // Nuanu / Tabanan
	ScopeIsland   = "island"
	ScopeNational = "national"
)

// DateMatch describes how a finding's date relates to the subject's.
const (
	MatchSameDay  = "same_day"
	MatchAdjacent = "adjacent"
	MatchPeriod   = "period"
)

// Subject is the move to explain.
type Subject struct {
	Venue     string    `json:"venue"`      // department id
	VenueName string    `json:"venue_name"` // for prompts
	VenueKind string    `json:"venue_kind"` // e.g. "beach_club", "restaurant", "attraction"
	Date      time.Time `json:"date"`       // the day that moved
	Direction Direction `json:"direction"`
	Value     float64   `json:"value"`      // revenue that day
	Baseline  float64   `json:"baseline"`   // average daily revenue it's compared to
	ChangePct float64   `json:"change_pct"` // (value - baseline) / baseline
}

// Finding is one piece of evidence from a check.
type Finding struct {
	Level        int             `json:"level"`
	Check        string          `json:"check"`
	Code         string          `json:"code"`
	DirectionFit bool            `json:"direction_fit"`
	Scope        string          `json:"scope"`
	DateMatch    string          `json:"date_match"`
	Strength     float64         `json:"strength"` // 0..1
	Summary      string          `json:"summary"`
	Evidence     json.RawMessage `json:"evidence,omitempty"`
	SourceURL    string          `json:"source_url,omitempty"`
	// StopsLadder marks a level-0 finding that proves the move is a data
	// artifact: business explanations are then skipped.
	StopsLadder bool `json:"stops_ladder,omitempty"`
}

// Check is one ladder step.
type Check interface {
	Level() int
	Name() string
	Run(ctx context.Context, s Subject) ([]Finding, error)
}

// Rejected is a finding that failed validation, kept with the reason so
// the model doesn't re-propose it.
type Rejected struct {
	Finding Finding `json:"finding"`
	Reason  string  `json:"reason"`
}

// CheckError records a check that failed to run; the ladder continues.
type CheckError struct {
	Check string `json:"check"`
	Level int    `json:"level"`
	Error string `json:"error"`
}

// Pack is the ladder's output: everything the explaining model may use.
type Pack struct {
	Subject  Subject      `json:"subject"`
	Stopped  bool         `json:"stopped"` // a level-0 artifact ended the ladder
	Accepted []Finding    `json:"accepted"`
	Rejected []Rejected   `json:"rejected,omitempty"`
	Errors   []CheckError `json:"errors,omitempty"`
	Ran      []string     `json:"ran"`
	Skipped  []string     `json:"skipped,omitempty"`
	Version  string       `json:"version"`
}

// Explained reports whether any accepted finding is a business or data
// explanation strong enough to stand on its own.
func (p Pack) Explained(minStrength float64) bool {
	for _, f := range p.Accepted {
		if f.Strength >= minStrength {
			return true
		}
	}
	return false
}

// Runner runs checks in level order.
type Runner struct {
	Checks []Check
	// Validate decides whether a finding counts. Defaults to DefaultValidator.
	Validate func(s Subject, f Finding) (ok bool, reason string)
	// Version tags packs so stored explanations record which ladder made them.
	Version string
}

// Run executes every check in level order. Level 0 runs first; a finding
// with StopsLadder ends the ladder after level 0. Every other level always
// runs in full, because causes stack. A failing check is recorded and
// skipped, never fatal.
func (r *Runner) Run(ctx context.Context, s Subject) Pack {
	validate := r.Validate
	if validate == nil {
		validate = DefaultValidator
	}
	checks := append([]Check(nil), r.Checks...)
	sort.SliceStable(checks, func(i, j int) bool { return checks[i].Level() < checks[j].Level() })

	p := Pack{Subject: s, Version: r.Version}
	for _, c := range checks {
		if p.Stopped && c.Level() > LevelIntegrity {
			p.Skipped = append(p.Skipped, c.Name())
			continue
		}
		if err := ctx.Err(); err != nil {
			p.Errors = append(p.Errors, CheckError{Check: c.Name(), Level: c.Level(), Error: err.Error()})
			continue
		}
		p.Ran = append(p.Ran, c.Name())
		findings, err := c.Run(ctx, s)
		if err != nil {
			p.Errors = append(p.Errors, CheckError{Check: c.Name(), Level: c.Level(), Error: err.Error()})
			continue
		}
		for _, f := range findings {
			f.Level, f.Check = c.Level(), c.Name()
			if ok, reason := validate(s, f); !ok {
				p.Rejected = append(p.Rejected, Rejected{Finding: f, Reason: reason})
				continue
			}
			p.Accepted = append(p.Accepted, f)
			if f.StopsLadder && c.Level() == LevelIntegrity {
				p.Stopped = true
			}
		}
	}
	sort.SliceStable(p.Accepted, func(i, j int) bool {
		if p.Accepted[i].Level != p.Accepted[j].Level {
			return p.Accepted[i].Level < p.Accepted[j].Level
		}
		return p.Accepted[i].Strength > p.Accepted[j].Strength
	})
	return p
}

// DefaultValidator applies the rules every finding must pass:
//   - direction fit (the check says the code can cause this direction),
//   - evidence (a summary plus structured evidence or a source URL),
//   - timing (a recognised date match),
//   - strength in (0, 1].
func DefaultValidator(s Subject, f Finding) (bool, string) {
	switch {
	case !f.DirectionFit:
		return false, fmt.Sprintf("%s cannot explain a move %s here", f.Code, s.Direction)
	case f.Summary == "" || (len(f.Evidence) == 0 && f.SourceURL == ""):
		return false, "no evidence"
	case f.DateMatch != MatchSameDay && f.DateMatch != MatchAdjacent && f.DateMatch != MatchPeriod:
		return false, "date does not match the move"
	case f.Strength <= 0 || f.Strength > 1:
		return false, "strength out of range"
	}
	return true, ""
}

// MustJSON marshals v for Finding.Evidence; panics only on unmarshalable
// values, which is a programming error.
func MustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
