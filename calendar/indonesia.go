// Package calendar answers "what kind of day was this?" for Nuanu's
// context: Indonesian national holidays and collective leave (cuti
// bersama), Bali's Pawukon holy days, weekends, and the long-weekend
// arrival/bridge rule. It is pure data and arithmetic - no network calls -
// so it is deterministic and testable.
package calendar

import (
	"fmt"
	"time"
)

// HolidayKind distinguishes official day types.
type HolidayKind string

const (
	// KindNational is a national holiday (libur nasional).
	KindNational HolidayKind = "national"
	// KindCollectiveLeave is a collective leave day (cuti bersama). Offices
	// close; for the long-weekend rule it counts as a non-working day.
	KindCollectiveLeave HolidayKind = "collective_leave"
	// KindBaliHolyDay is a Balinese Hindu day from the Pawukon cycle. Not a
	// national day off, but many Balinese staff and businesses close.
	KindBaliHolyDay HolidayKind = "bali_holy_day"
)

// Holiday is one dated day.
type Holiday struct {
	Date   time.Time   `json:"date"`
	Name   string      `json:"name"`
	Kind   HolidayKind `json:"kind"`
	Source string      `json:"source"`
	// Tag groups related days for reasoning, e.g. "nyepi", "idul_fitri".
	Tag string `json:"tag,omitempty"`
}

// ErrNoData is returned when a year is outside the verified tables, so a
// caller reports a gap instead of silently concluding "no holiday".
type ErrNoData struct{ Year int }

func (e ErrNoData) Error() string {
	return fmt.Sprintf("calendar: no verified Indonesian holiday data for %d", e.Year)
}

const (
	sourceSKB2025 = "SKB 3 Menteri 2025 (signed 2024-10-14; revised 2025-08-07 adding 18 Aug cuti bersama)"
	sourceSKB2026 = "SKB 3 Menteri 2026 (signed 2025-09-19)"
)

type row struct {
	date string
	name string
	kind HolidayKind
	tag  string
}

// indonesia holds the verified SKB tables. Add a year only from the
// official decree (or a published copy of it), never from memory - and
// never from Nager.Date, whose Indonesia data omits lunar holidays.
var indonesia = map[int]struct {
	source string
	rows   []row
}{
	2025: {sourceSKB2025, []row{
		{"2025-01-01", "Tahun Baru 2025 Masehi", KindNational, "new_year"},
		{"2025-01-27", "Isra Mikraj Nabi Muhammad SAW", KindNational, "isra_mikraj"},
		{"2025-01-28", "Cuti Bersama Tahun Baru Imlek 2576 Kongzili", KindCollectiveLeave, "imlek"},
		{"2025-01-29", "Tahun Baru Imlek 2576 Kongzili", KindNational, "imlek"},
		{"2025-03-28", "Cuti Bersama Hari Suci Nyepi (Tahun Baru Saka 1947)", KindCollectiveLeave, "nyepi"},
		{"2025-03-29", "Hari Suci Nyepi (Tahun Baru Saka 1947)", KindNational, "nyepi"},
		{"2025-03-31", "Hari Raya Idul Fitri 1446 Hijriah", KindNational, "idul_fitri"},
		{"2025-04-01", "Hari Raya Idul Fitri 1446 Hijriah", KindNational, "idul_fitri"},
		{"2025-04-02", "Cuti Bersama Idul Fitri 1446 Hijriah", KindCollectiveLeave, "idul_fitri"},
		{"2025-04-03", "Cuti Bersama Idul Fitri 1446 Hijriah", KindCollectiveLeave, "idul_fitri"},
		{"2025-04-04", "Cuti Bersama Idul Fitri 1446 Hijriah", KindCollectiveLeave, "idul_fitri"},
		{"2025-04-07", "Cuti Bersama Idul Fitri 1446 Hijriah", KindCollectiveLeave, "idul_fitri"},
		{"2025-04-18", "Wafat Yesus Kristus", KindNational, "good_friday"},
		{"2025-04-20", "Kebangkitan Yesus Kristus (Paskah)", KindNational, "easter"},
		{"2025-05-01", "Hari Buruh Internasional", KindNational, "labour_day"},
		{"2025-05-12", "Hari Raya Waisak 2569 BE", KindNational, "waisak"},
		{"2025-05-13", "Cuti Bersama Hari Raya Waisak 2569 BE", KindCollectiveLeave, "waisak"},
		{"2025-05-29", "Kenaikan Yesus Kristus", KindNational, "ascension"},
		{"2025-05-30", "Cuti Bersama Kenaikan Yesus Kristus", KindCollectiveLeave, "ascension"},
		{"2025-06-01", "Hari Lahir Pancasila", KindNational, "pancasila"},
		{"2025-06-06", "Hari Raya Idul Adha 1446 Hijriah", KindNational, "idul_adha"},
		{"2025-06-09", "Cuti Bersama Hari Raya Idul Adha 1446 Hijriah", KindCollectiveLeave, "idul_adha"},
		{"2025-06-27", "1 Muharam Tahun Baru Islam 1447 Hijriah", KindNational, "islamic_new_year"},
		{"2025-08-17", "Hari Kemerdekaan Republik Indonesia", KindNational, "independence"},
		{"2025-08-18", "Cuti Bersama HUT ke-80 RI", KindCollectiveLeave, "independence"},
		{"2025-09-05", "Maulid Nabi Muhammad SAW", KindNational, "maulid"},
		{"2025-12-25", "Hari Natal", KindNational, "christmas"},
		{"2025-12-26", "Cuti Bersama Hari Natal", KindCollectiveLeave, "christmas"},
	}},
	2026: {sourceSKB2026, []row{
		{"2026-01-01", "Tahun Baru 2026 Masehi", KindNational, "new_year"},
		{"2026-01-16", "Isra Mikraj Nabi Muhammad SAW", KindNational, "isra_mikraj"},
		{"2026-02-16", "Cuti Bersama Tahun Baru Imlek 2577 Kongzili", KindCollectiveLeave, "imlek"},
		{"2026-02-17", "Tahun Baru Imlek 2577 Kongzili", KindNational, "imlek"},
		{"2026-03-18", "Cuti Bersama Hari Suci Nyepi", KindCollectiveLeave, "nyepi"},
		{"2026-03-19", "Hari Suci Nyepi (Tahun Baru Saka 1948)", KindNational, "nyepi"},
		{"2026-03-20", "Cuti Bersama Idul Fitri 1447 Hijriah", KindCollectiveLeave, "idul_fitri"},
		{"2026-03-21", "Idul Fitri 1447 Hijriah", KindNational, "idul_fitri"},
		{"2026-03-22", "Idul Fitri 1447 Hijriah", KindNational, "idul_fitri"},
		{"2026-03-23", "Cuti Bersama Idul Fitri 1447 Hijriah", KindCollectiveLeave, "idul_fitri"},
		{"2026-03-24", "Cuti Bersama Idul Fitri 1447 Hijriah", KindCollectiveLeave, "idul_fitri"},
		{"2026-04-03", "Wafat Yesus Kristus", KindNational, "good_friday"},
		{"2026-04-05", "Kebangkitan Yesus Kristus (Paskah)", KindNational, "easter"},
		{"2026-05-01", "Hari Buruh Internasional", KindNational, "labour_day"},
		{"2026-05-14", "Kenaikan Yesus Kristus", KindNational, "ascension"},
		{"2026-05-15", "Cuti Bersama Kenaikan Yesus Kristus", KindCollectiveLeave, "ascension"},
		{"2026-05-27", "Idul Adha 1447 Hijriah", KindNational, "idul_adha"},
		{"2026-05-28", "Cuti Bersama Idul Adha 1447 Hijriah", KindCollectiveLeave, "idul_adha"},
		{"2026-05-31", "Hari Raya Waisak 2570 BE", KindNational, "waisak"},
		{"2026-06-01", "Hari Lahir Pancasila", KindNational, "pancasila"},
		{"2026-06-16", "1 Muharam Tahun Baru Islam 1448 Hijriah", KindNational, "islamic_new_year"},
		{"2026-08-17", "Proklamasi Kemerdekaan", KindNational, "independence"},
		{"2026-08-25", "Maulid Nabi Muhammad SAW", KindNational, "maulid"},
		{"2026-12-24", "Cuti Bersama Kelahiran Yesus Kristus", KindCollectiveLeave, "christmas"},
		{"2026-12-25", "Kelahiran Yesus Kristus", KindNational, "christmas"},
	}},
}

// IndonesiaYear returns the national holidays and collective leave days of
// year, in date order, or ErrNoData if the year isn't in the verified
// tables.
func IndonesiaYear(year int) ([]Holiday, error) {
	t, ok := indonesia[year]
	if !ok {
		return nil, ErrNoData{Year: year}
	}
	out := make([]Holiday, 0, len(t.rows))
	for _, r := range t.rows {
		out = append(out, Holiday{Date: mustDate(r.date), Name: r.name, Kind: r.kind, Source: t.source, Tag: r.tag})
	}
	return out, nil
}

// IndonesiaOn returns the national holidays and collective leave days
// falling on d (usually zero or one).
func IndonesiaOn(d time.Time) ([]Holiday, error) {
	all, err := IndonesiaYear(d.Year())
	if err != nil {
		return nil, err
	}
	d = Day(d)
	var out []Holiday
	for _, h := range all {
		if h.Date.Equal(d) {
			out = append(out, h)
		}
	}
	return out, nil
}

// HasData reports whether year is covered by the verified tables.
func HasData(year int) bool {
	_, ok := indonesia[year]
	return ok
}

// Day normalizes t to its calendar date at UTC midnight, so dates compare
// by value regardless of the zone they were parsed in.
func Day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func mustDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}
