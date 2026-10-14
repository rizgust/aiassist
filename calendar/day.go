package calendar

import "time"

// LongWeekendRole is a day's place relative to a long weekend.
type LongWeekendRole string

const (
	// RoleNone: not part of or next to a long weekend.
	RoleNone LongWeekendRole = ""
	// RoleArrival: the last working day before a run of 3+ non-working days.
	RoleArrival LongWeekendRole = "arrival"
	// RoleInRun: a non-working day inside a run of 3+ non-working days.
	RoleInRun LongWeekendRole = "in_run"
	// RoleBridge: a single working day between a weekend and a national
	// holiday or collective leave day (e.g. Mon 24 Aug 2026, between Sun 23
	// and Tue 25 Maulid Nabi).
	RoleBridge LongWeekendRole = "bridge"
)

// DayInfo describes one calendar day.
type DayInfo struct {
	Date      time.Time       `json:"date"`
	Weekday   string          `json:"weekday"`
	Weekend   bool            `json:"weekend"`
	Holidays  []Holiday       `json:"holidays,omitempty"` // national + collective leave
	BaliDays  []Holiday       `json:"bali_days,omitempty"`
	Working   bool            `json:"working"`
	Role      LongWeekendRole `json:"long_weekend_role,omitempty"`
	RunLength int             `json:"long_weekend_run_length,omitempty"` // for arrival/in_run
	// Coverage is false when the year has no verified holiday table: the
	// holiday fields are then unknown, not "none".
	Coverage bool `json:"coverage"`
}

// Describe returns the full day context for d.
func Describe(d time.Time) DayInfo {
	d = Day(d)
	info := DayInfo{Date: d, Weekday: d.Weekday().String(), Weekend: isWeekend(d), Coverage: HasData(d.Year())}
	info.Holidays, _ = IndonesiaOn(d)
	info.BaliDays = BaliOn(d)
	info.Working = isWorking(d)
	info.Role, info.RunLength = longWeekendRole(d)
	return info
}

func isWeekend(d time.Time) bool {
	wd := d.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// isOffDay: a national holiday or collective leave day (not a weekend).
func isOffDay(d time.Time) bool {
	hs, err := IndonesiaOn(d)
	return err == nil && len(hs) > 0
}

// isWorking: not a weekend, holiday or collective leave day. Bali holy days
// don't count - they aren't national days off.
func isWorking(d time.Time) bool {
	return !isWeekend(d) && !isOffDay(d)
}

// runAround returns the length of the run of consecutive non-working days
// containing d (0 if d is a working day).
func runAround(d time.Time) int {
	if isWorking(d) {
		return 0
	}
	n := 1
	for x := d.AddDate(0, 0, -1); !isWorking(x); x = x.AddDate(0, 0, -1) {
		n++
	}
	for x := d.AddDate(0, 0, 1); !isWorking(x); x = x.AddDate(0, 0, 1) {
		n++
	}
	return n
}

// longWeekendRole applies the brief's rule: a long weekend is a run of 3+
// consecutive non-working days; derive the arrival day and bridge day from
// it - never from manual tagging.
func longWeekendRole(d time.Time) (LongWeekendRole, int) {
	if !isWorking(d) {
		if n := runAround(d); n >= 3 {
			return RoleInRun, n
		}
		return RoleNone, 0
	}
	next, prev := d.AddDate(0, 0, 1), d.AddDate(0, 0, -1)
	// Bridge: weekend on one side, holiday/leave on the other.
	if (isWeekend(prev) && isOffDay(next)) || (isOffDay(prev) && isWeekend(next)) {
		return RoleBridge, 0
	}
	if n := runAround(next); n >= 3 {
		return RoleArrival, n
	}
	return RoleNone, 0
}
