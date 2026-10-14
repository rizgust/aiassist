package ladder

import (
	"context"
	"fmt"
	"time"
)

// CodeDataDateShift: the move is likely revenue landing on the wrong day
// (offline POS synced later, DD/MM vs MM/DD, business vs calendar date).
const CodeDataDateShift = "DATA_DATE_SHIFT"

// DailySeries returns a venue's daily revenue for [from, to] keyed by
// "2006-01-02". Days with no revenue may be absent.
type DailySeries func(ctx context.Context, venue string, from, to time.Time) (map[string]float64, error)

// DateShiftCheck is a level-0 check: a drop next to a matching surplus (or
// a spike next to a matching deficit) on an adjacent day suggests revenue
// was recorded on the wrong day. It doesn't stop the ladder - real causes
// (e.g. a holiday) can produce the same shape - but it is reported first.
type DateShiftCheck struct {
	Series DailySeries
	// Match is how much of the move a neighbour must offset to count
	// (default 0.6).
	Match float64
}

func (DateShiftCheck) Level() int   { return LevelIntegrity }
func (DateShiftCheck) Name() string { return "date_shift" }

func (c DateShiftCheck) Run(ctx context.Context, s Subject) ([]Finding, error) {
	if s.Baseline <= 0 {
		return nil, nil
	}
	match := c.Match
	if match <= 0 {
		match = 0.6
	}
	day := s.Date
	series, err := c.Series(ctx, s.Venue, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	move := s.Value - s.Baseline // negative for a drop
	var out []Finding
	for _, n := range []time.Time{day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)} {
		nv := series[n.Format("2006-01-02")]
		offset := nv - s.Baseline // must have the opposite sign
		if move == 0 || offset*move >= 0 {
			continue
		}
		share := -offset / move
		if share < match {
			continue
		}
		strength := 0.4
		if share >= 0.9 {
			strength = 0.55
		}
		out = append(out, Finding{
			Code: CodeDataDateShift, DirectionFit: true, Scope: ScopeVenue, DateMatch: MatchAdjacent,
			Strength: strength,
			Summary: fmt.Sprintf("%s offsets %.0f%% of the move on %s - possible date shift (check POS sync and date parsing)",
				n.Format("Mon 2 Jan"), share*100, day.Format("Mon 2 Jan")),
			Evidence: MustJSON(map[string]any{"day": day.Format("2006-01-02"), "value": s.Value, "neighbour": n.Format("2006-01-02"), "neighbour_value": nv, "baseline": s.Baseline, "offset_share": share}),
		})
	}
	return out, nil
}
