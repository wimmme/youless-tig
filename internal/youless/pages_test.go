package youless

import (
	"testing"
	"time"
)

// The page spans at the capture time match what the device showed then.
func TestPageSpanMatchesDevice(t *testing.T) {
	loc := brussels(t)
	now := time.Unix(captured, 0)
	for _, c := range []struct {
		file string
		span Span
	}{
		{"V_h1.json", PageSpan(Minute, 1, now, loc)},
		{"V_w1.json", PageSpan(TenMinutes, 1, now, loc)},
		{"V_d1.json", PageSpan(Hour, 1, now, loc)},
		{"V_d3.json", PageSpan(Hour, 3, now, loc)},
		{"V_m10.json", MonthSpan(10, 0, now, loc)},
		{"V_m9.json", MonthSpan(9, 0, now, loc)},
		{"W_w1.json", PageSpan(TenMinutes, 1, now, loc)},
		{"K_d1.json", PageSpan(Hour, 1, now, loc)},
		{"Z_m10.json", MonthSpan(10, 0, now, loc)},
	} {
		p, err := ParseLogPage(fixture(t, c.file), loc)
		if err != nil {
			t.Fatal(err)
		}
		if !c.span.Start.Equal(p.Start) || c.span.Interval != p.Interval || c.span.Count != len(p.Values) {
			t.Errorf("%s: span %v %v ×%d, device %v %v ×%d", c.file,
				c.span.Start, c.span.Interval, c.span.Count, p.Start, p.Interval, len(p.Values))
		}
	}
}

func TestPageSpanPaging(t *testing.T) {
	loc := brussels(t)
	now := time.Date(2026, 10, 15, 1, 44, 15, 0, loc)
	p1, p2 := PageSpan(Minute, 1, now, loc), PageSpan(Minute, 2, now, loc)
	if !p2.End().Equal(p1.Start) {
		t.Errorf("minute page 2 ends %v, page 1 starts %v", p2.End(), p1.Start)
	}
	w1, w30 := PageSpan(TenMinutes, 1, now, loc), PageSpan(TenMinutes, 30, now, loc)
	if got := w1.End().Sub(w30.Start); got != 30*48*10*time.Minute {
		t.Errorf("10-minute log covers %v", got)
	}
	d70 := PageSpan(Hour, 70, now, loc)
	if want := time.Date(2026, 8, 7, 0, 0, 0, 0, loc); !d70.Start.Equal(want) || d70.Count != 24 {
		t.Errorf("hour page 70: %v ×%d, want %v ×24 (as the device showed)", d70.Start, d70.Count, want)
	}
}

func TestPageSpanDaylightSaving(t *testing.T) {
	loc := brussels(t)
	// 25 October 2026: clocks go back, the day has 25 hours.
	fall := PageSpan(Hour, 2, time.Date(2026, 10, 26, 12, 0, 0, 0, loc), loc)
	if fall.Count != 25 {
		t.Errorf("autumn day: %d hours", fall.Count)
	}
	// 29 March 2026: clocks go forward, 23 hours.
	spring := PageSpan(Hour, 2, time.Date(2026, 3, 30, 12, 0, 0, 0, loc), loc)
	if spring.Count != 23 {
		t.Errorf("spring day: %d hours", spring.Count)
	}
	// Day logs step in calendar days across the change.
	m := MonthSpan(10, 2025, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), loc)
	if m.Count != 31 || m.Time(30).Day() != 31 || m.Time(30).Hour() != 0 {
		t.Errorf("October 2025: %d days, last %v", m.Count, m.Time(30))
	}
}

func TestMonthSpan(t *testing.T) {
	loc := brussels(t)
	now := time.Date(2026, 10, 15, 1, 44, 0, 0, loc)
	if s := MonthSpan(11, 0, now, loc); s.Start.Year() != 2025 || s.Count != 30 {
		t.Errorf("m=11 is November 2025: %v ×%d", s.Start, s.Count)
	}
	if s := MonthSpan(10, 0, now, loc); s.Count != 14 {
		t.Errorf("current month up to yesterday: %d", s.Count)
	}
	first := time.Date(2026, 10, 1, 0, 30, 0, 0, loc)
	if s := MonthSpan(10, 0, first, loc); s.Count != 0 {
		t.Errorf("on the 1st the month is empty: %d", s.Count)
	}
	if s := MonthSpan(2, 2024, now, loc); s.Count != 29 {
		t.Errorf("February 2024: %d days", s.Count)
	}
	if s := MonthSpan(12, 2026, now, loc); s.Count != 0 {
		t.Errorf("future month: %d days", s.Count)
	}
}
