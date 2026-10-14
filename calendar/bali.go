package calendar

import (
	"sort"
	"time"
)

// pawukonCycle is the length of the Balinese Pawukon calendar: 30 wuku
// (weeks) of 7 days.
const pawukonCycle = 210

// galunganAnchor is a verified Galungan date (Wednesday 17 June 2026). Every
// Pawukon day is a fixed offset from it, repeating every 210 days -
// published dates for 13 Jan 2027 and 11 Aug 2027 match this anchor.
var galunganAnchor = time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)

// baliDay is a Pawukon holy day as an offset (days) from Galungan within
// the same 210-day cycle. Galungan is Wednesday of wuku Dungulan (cycle day
// 73, Sunday of wuku Sinta = day 0).
type baliDay struct {
	offset int
	name   string
	tag    string
}

var baliDays = []baliDay{
	{-1, "Penampahan Galungan", "galungan"},
	{0, "Hari Raya Galungan", "galungan"},
	{1, "Umanis Galungan", "galungan"},
	{9, "Penampahan Kuningan", "kuningan"},
	{10, "Hari Raya Kuningan", "kuningan"},
	// Saraswati is Saturday of Watugunung, the last day of the cycle (day
	// 209): 209 - 73 = 136 days after Galungan.
	{136, "Hari Raya Saraswati", "saraswati"},
	{137, "Banyu Pinaruh", "saraswati"},
	// Pagerwesi is Wednesday of Sinta, day 3 of the next cycle: 140 days
	// after Galungan.
	{140, "Hari Raya Pagerwesi", "pagerwesi"},
}

// BaliHolyDays returns the Pawukon holy days in [from, to], in date order.
func BaliHolyDays(from, to time.Time) []Holiday {
	from, to = Day(from), Day(to)
	var out []Holiday
	for _, bd := range baliDays {
		// First occurrence on or after from.
		base := galunganAnchor.AddDate(0, 0, bd.offset)
		diff := int(from.Sub(base).Hours() / 24)
		k := floorDiv(diff, pawukonCycle)
		for d := base.AddDate(0, 0, k*pawukonCycle); !d.After(to); d = d.AddDate(0, 0, pawukonCycle) {
			if d.Before(from) {
				continue
			}
			out = append(out, Holiday{Date: d, Name: bd.name, Kind: KindBaliHolyDay, Source: "Pawukon cycle (anchor Galungan 2026-06-17)", Tag: bd.tag})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}

// BaliOn returns the Pawukon holy days falling on d.
func BaliOn(d time.Time) []Holiday {
	return BaliHolyDays(d, d)
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
