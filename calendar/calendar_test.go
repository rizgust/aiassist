package calendar

import (
	"testing"
	"time"
)

func d(s string) time.Time { return mustDate(s) }

func TestIndonesiaTables_CountsAndWeekdays(t *testing.T) {
	// Official counts: 2025 = 17 national + 10 collective leave (+1 added
	// by the Aug 2025 revision); 2026 = 17 national + 8 collective leave.
	want := map[int][2]int{2025: {17, 11}, 2026: {17, 8}}
	for year, w := range want {
		hs, err := IndonesiaYear(year)
		if err != nil {
			t.Fatal(err)
		}
		var nat, leave int
		for i, h := range hs {
			if h.Date.Year() != year {
				t.Errorf("%s in %d table", h.Date.Format("2006-01-02"), year)
			}
			if i > 0 && h.Date.Before(hs[i-1].Date) {
				t.Errorf("%d table not in date order at %s", year, h.Date.Format("2006-01-02"))
			}
			switch h.Kind {
			case KindNational:
				nat++
			case KindCollectiveLeave:
				leave++
			}
		}
		if nat != w[0] || leave != w[1] {
			t.Errorf("%d: %d national / %d leave, want %d / %d", year, nat, leave, w[0], w[1])
		}
	}
	// Spot-check weekdays stated in the decree copies.
	for date, wd := range map[string]time.Weekday{
		"2026-03-18": time.Wednesday, "2026-03-19": time.Thursday, "2026-08-17": time.Monday,
		"2026-08-25": time.Tuesday, "2025-03-29": time.Saturday, "2025-04-07": time.Monday,
	} {
		if got := d(date).Weekday(); got != wd {
			t.Errorf("%s is %s, table source says %s", date, got, wd)
		}
	}
}

func TestIndonesia_NoDataYear(t *testing.T) {
	if _, err := IndonesiaYear(2031); err == nil {
		t.Fatal("expected ErrNoData")
	}
	if info := Describe(d("2031-03-01")); info.Coverage {
		t.Fatal("Coverage should be false for an untabled year")
	}
}

func TestBaliHolyDays_MatchPublishedDates(t *testing.T) {
	cases := map[string]string{
		"2026-06-17": "Hari Raya Galungan", // anchor
		"2026-06-27": "Hari Raya Kuningan", // published: Sat 27 Jun 2026
		"2026-06-16": "Penampahan Galungan",
		"2027-01-13": "Hari Raya Galungan", // published next Galungan
		"2027-08-11": "Hari Raya Galungan", // published
		"2025-11-19": "Hari Raya Galungan", // 210 days before the anchor
		"2025-04-23": "Hari Raya Galungan",
	}
	for date, name := range cases {
		found := false
		for _, h := range BaliOn(d(date)) {
			if h.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q, got %+v", date, name, BaliOn(d(date)))
		}
	}
	// Weekday invariants of the Pawukon days.
	for _, h := range BaliHolyDays(d("2025-01-01"), d("2027-12-31")) {
		want := map[string]time.Weekday{
			"Hari Raya Galungan": time.Wednesday, "Hari Raya Kuningan": time.Saturday,
			"Hari Raya Saraswati": time.Saturday, "Hari Raya Pagerwesi": time.Wednesday,
			"Banyu Pinaruh": time.Sunday,
		}[h.Name]
		if want != 0 || h.Name == "Banyu Pinaruh" {
			if h.Date.Weekday() != want {
				t.Errorf("%s on %s is a %s, want %s", h.Name, h.Date.Format("2006-01-02"), h.Date.Weekday(), want)
			}
		}
	}
}

func TestDescribe_BriefExamples(t *testing.T) {
	cases := []struct {
		date string
		role LongWeekendRole
		why  string
	}{
		// From the spike brief.
		{"2026-08-14", RoleArrival, "Fri before Sat 15 - Mon 17 Aug (Independence Day)"},
		{"2026-08-24", RoleBridge, "Mon between Sun 23 and Tue 25 (Maulid Nabi)"},
		{"2026-08-16", RoleInRun, "inside the Independence Day long weekend"},
		// Luna Beach Club, March 2026: Nyepi then the Idul Fitri break is
		// one long run of non-working days from Wed 18 to Tue 24 March.
		{"2026-03-17", RoleArrival, "last working day before Nyepi + Idul Fitri"},
		{"2026-03-18", RoleInRun, "Nyepi collective leave"},
		{"2026-03-24", RoleInRun, "last Idul Fitri collective leave"},
		{"2026-03-25", RoleNone, "back to work"},
		{"2026-03-11", RoleNone, "ordinary Wednesday"},
	}
	for _, c := range cases {
		info := Describe(d(c.date))
		if info.Role != c.role {
			t.Errorf("%s (%s): role %q, want %q", c.date, c.why, info.Role, c.role)
		}
	}
	if info := Describe(d("2026-03-18")); info.RunLength != 7 || info.Working || len(info.Holidays) != 1 || info.Holidays[0].Tag != "nyepi" {
		t.Errorf("18 Mar 2026: %+v", info)
	}
}
