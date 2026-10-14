package ladder

import (
	"context"
	"errors"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

type fakeCheck struct {
	level    int
	name     string
	findings []Finding
	err      error
	ran      *bool
}

func (f fakeCheck) Level() int   { return f.level }
func (f fakeCheck) Name() string { return f.name }
func (f fakeCheck) Run(context.Context, Subject) ([]Finding, error) {
	if f.ran != nil {
		*f.ran = true
	}
	return f.findings, f.err
}

func ev() []byte { return MustJSON(map[string]string{"k": "v"}) }

func TestRunner_LevelZeroStopSkipsBusinessLevels(t *testing.T) {
	var calendarRan bool
	r := &Runner{Checks: []Check{
		fakeCheck{level: LevelCalendar, name: "calendar", ran: &calendarRan,
			findings: []Finding{{Code: "PUBLIC_HOLIDAY", DirectionFit: true, DateMatch: MatchSameDay, Strength: 0.9, Summary: "x", Evidence: ev()}}},
		fakeCheck{level: LevelIntegrity, name: "coverage",
			findings: []Finding{{Code: "DATA_MISSING", DirectionFit: true, DateMatch: MatchSameDay, Strength: 1, Summary: "no document", Evidence: ev(), StopsLadder: true}}},
	}}
	p := r.Run(context.Background(), Subject{Date: day("2026-03-18"), Direction: Down})
	if !p.Stopped || calendarRan || len(p.Skipped) != 1 || p.Accepted[0].Code != "DATA_MISSING" {
		t.Fatalf("pack = %+v, calendarRan=%v", p, calendarRan)
	}
}

func TestRunner_ValidationAndErrors(t *testing.T) {
	r := &Runner{Checks: []Check{
		fakeCheck{level: LevelEnviron, name: "weather", findings: []Finding{
			{Code: "WEATHER_ADVERSE", DirectionFit: false, DateMatch: MatchSameDay, Strength: 0.7, Summary: "rain", Evidence: ev()},
			{Code: "WEATHER_ADVERSE", DirectionFit: true, DateMatch: MatchSameDay, Strength: 0.7, Summary: "no evidence"},
		}},
		fakeCheck{level: LevelDisruption, name: "quakes", err: errors.New("usgs timeout")},
		fakeCheck{level: LevelEvents, name: "events", findings: []Finding{
			{Code: "EVENT_PUBLIC", DirectionFit: true, DateMatch: MatchSameDay, Strength: 0.8, Summary: "festival", SourceURL: "https://x"},
		}},
	}}
	p := r.Run(context.Background(), Subject{Date: day("2026-09-12"), Direction: Up})
	if len(p.Accepted) != 1 || p.Accepted[0].Code != "EVENT_PUBLIC" || p.Accepted[0].Check != "events" {
		t.Fatalf("accepted = %+v", p.Accepted)
	}
	if len(p.Rejected) != 2 || len(p.Errors) != 1 || p.Errors[0].Check != "quakes" {
		t.Fatalf("rejected=%+v errors=%+v", p.Rejected, p.Errors)
	}
	if !p.Explained(0.5) || p.Explained(0.9) {
		t.Fatal("Explained threshold wrong")
	}
}

// Luna Beach Club, 18 Mar 2026: zero revenue. The calendar level must
// produce Nyepi collective leave as an accepted, strong finding.
func TestCalendarCheck_LunaNyepi(t *testing.T) {
	r := &Runner{Checks: []Check{CalendarCheck{}}}
	p := r.Run(context.Background(), Subject{Venue: "luna", Date: day("2026-03-18"), Direction: Down, Value: 0, Baseline: 250e6, ChangePct: -1})
	var holiday, longWeekend bool
	for _, f := range p.Accepted {
		if f.Code == CodePublicHoliday && f.Strength >= 0.9 {
			holiday = true
		}
		if f.Code == CodeLongWeekend {
			longWeekend = true
		}
	}
	if !holiday || !longWeekend {
		t.Fatalf("accepted = %+v", p.Accepted)
	}
	for _, rj := range p.Accepted {
		if rj.Code == CodeWeekend {
			t.Fatal("18 Mar 2026 is a Wednesday")
		}
	}
}

// Same calendar, other direction: the 21 Mar 2026 surge (Saturday, Idul
// Fitri) is explained by the holiday, the long run and the weekend, while a
// Nyepi-style closure reading would be rejected.
func TestCalendarCheck_IdulFitriSurge(t *testing.T) {
	r := &Runner{Checks: []Check{CalendarCheck{}}}
	p := r.Run(context.Background(), Subject{Date: day("2026-03-21"), Direction: Up, Value: 485e6, Baseline: 250e6})
	codes := map[string]bool{}
	for _, f := range p.Accepted {
		codes[f.Code] = true
	}
	if !codes[CodePublicHoliday] || !codes[CodeLongWeekend] || !codes[CodeWeekend] {
		t.Fatalf("accepted = %+v", p.Accepted)
	}

	// A drop on a plain weekend: WEEKEND is rejected (direction).
	p = r.Run(context.Background(), Subject{Date: day("2026-03-08"), Direction: Down})
	if len(p.Accepted) != 0 || len(p.Rejected) != 1 || p.Rejected[0].Finding.Code != CodeWeekend {
		t.Fatalf("plain weekend drop: %+v", p)
	}
}

func TestCalendarCheck_UntabledYearIsAnError(t *testing.T) {
	p := (&Runner{Checks: []Check{CalendarCheck{}}}).Run(context.Background(), Subject{Date: day("2031-01-05"), Direction: Down})
	if len(p.Errors) != 1 {
		t.Fatalf("want an error for an untabled year, got %+v", p)
	}
}

func TestDateShiftCheck(t *testing.T) {
	series := func(m map[string]float64) DailySeries {
		return func(context.Context, string, time.Time, time.Time) (map[string]float64, error) { return m, nil }
	}
	// Drop to 50 on the 10th, surplus 290 on the 11th against a baseline of 200.
	c := DateShiftCheck{Series: series(map[string]float64{"2026-05-09": 210, "2026-05-11": 290})}
	fs, err := c.Run(context.Background(), Subject{Venue: "v", Date: day("2026-05-10"), Direction: Down, Value: 50, Baseline: 200})
	if err != nil || len(fs) != 1 || fs[0].Code != CodeDataDateShift || fs[0].DateMatch != MatchAdjacent {
		t.Fatalf("findings = %+v, %v", fs, err)
	}
	// Luna-like: both neighbours normal - no shift.
	c = DateShiftCheck{Series: series(map[string]float64{"2026-03-17": 245e6, "2026-03-19": 0})}
	fs, _ = c.Run(context.Background(), Subject{Date: day("2026-03-18"), Direction: Down, Value: 0, Baseline: 250e6})
	if len(fs) != 0 {
		t.Fatalf("unexpected shift finding: %+v", fs)
	}
}
