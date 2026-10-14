package ladder

import (
	"context"
	"fmt"
	"strings"

	"github.com/rizgust/aiassist/calendar"
)

// Calendar reason codes.
const (
	CodePublicHoliday = "PUBLIC_HOLIDAY"
	CodeBaliHolyDay   = "BALI_HOLY_DAY"
	CodeLongWeekend   = "LONG_WEEKEND"
	CodeWeekend       = "WEEKEND"
)

// CalendarCheck is level 1: national holidays and collective leave, Bali
// holy days, the long-weekend rule and weekends, for the subject's date.
type CalendarCheck struct{}

func (CalendarCheck) Level() int   { return LevelCalendar }
func (CalendarCheck) Name() string { return "calendar" }

func (CalendarCheck) Run(_ context.Context, s Subject) ([]Finding, error) {
	info := calendar.Describe(s.Date)
	if !info.Coverage {
		return nil, fmt.Errorf("no verified holiday table for %d - holidays unknown, not absent", s.Date.Year())
	}
	var out []Finding

	for _, h := range info.Holidays {
		fit, strength := holidayFit(h, s.Direction)
		kind := "national holiday"
		if h.Kind == calendar.KindCollectiveLeave {
			kind = "collective leave (cuti bersama)"
		}
		out = append(out, Finding{
			Code: CodePublicHoliday, DirectionFit: fit, Scope: ScopeNational, DateMatch: MatchSameDay,
			Strength: strength, Summary: fmt.Sprintf("%s: %s (%s)", info.Date.Format("Mon 2 Jan 2006"), h.Name, kind),
			Evidence: MustJSON(h),
		})
	}
	for _, h := range info.BaliDays {
		out = append(out, Finding{
			Code: CodeBaliHolyDay, DirectionFit: true, Scope: ScopeIsland, DateMatch: MatchSameDay,
			Strength: baliStrength(h), Summary: fmt.Sprintf("%s: %s (Bali Hindu holy day)", info.Date.Format("Mon 2 Jan 2006"), h.Name),
			Evidence: MustJSON(h),
		})
	}
	if info.Role != calendar.RoleNone {
		fit, strength := longWeekendFit(info, s.Direction)
		out = append(out, Finding{
			Code: CodeLongWeekend, DirectionFit: fit, Scope: ScopeNational, DateMatch: MatchSameDay,
			Strength: strength, Summary: longWeekendSummary(info), Evidence: MustJSON(info),
		})
	}
	if info.Weekend {
		// Weekends lift leisure venues; they don't explain a drop.
		out = append(out, Finding{
			Code: CodeWeekend, DirectionFit: s.Direction == Up, Scope: ScopeNational, DateMatch: MatchSameDay,
			Strength: 0.4, Summary: info.Date.Format("Monday") + " (weekend)", Evidence: MustJSON(map[string]string{"weekday": info.Weekday}),
		})
	}
	return out, nil
}

// holidayFit: Nyepi shuts Bali down (airport included), so it only explains
// a drop - strongly. Other holidays can go either way: leisure venues
// usually rise, venues serving local workers can fall.
func holidayFit(h calendar.Holiday, dir Direction) (bool, float64) {
	if h.Tag == "nyepi" {
		return dir == Down, 0.95
	}
	if h.Kind == calendar.KindCollectiveLeave {
		return true, 0.6
	}
	return true, 0.7
}

func baliStrength(h calendar.Holiday) float64 {
	switch h.Tag {
	case "galungan", "kuningan":
		return 0.6 // many Balinese staff off, local businesses close
	default:
		return 0.35
	}
}

func longWeekendFit(info calendar.DayInfo, dir Direction) (bool, float64) {
	switch info.Role {
	case calendar.RoleArrival:
		return dir == Up, 0.5
	case calendar.RoleBridge:
		return true, 0.45 // many take the day off; leisure up, office-led venues down
	case calendar.RoleInRun:
		// A long run of days off lifts leisure venues; a day inside the run
		// that is itself Nyepi is handled by the holiday finding.
		return dir == Up || hasTag(info, "nyepi"), 0.55
	}
	return false, 0
}

func longWeekendSummary(info calendar.DayInfo) string {
	day := info.Date.Format("Mon 2 Jan 2006")
	switch info.Role {
	case calendar.RoleArrival:
		return fmt.Sprintf("%s: arrival day - last working day before %d consecutive days off", day, info.RunLength)
	case calendar.RoleBridge:
		return fmt.Sprintf("%s: bridge day - a working day between a weekend and a holiday", day)
	default:
		return fmt.Sprintf("%s: inside a run of %d consecutive non-working days", day, info.RunLength)
	}
}

func hasTag(info calendar.DayInfo, tag string) bool {
	for _, h := range info.Holidays {
		if strings.EqualFold(h.Tag, tag) {
			return true
		}
	}
	return false
}
