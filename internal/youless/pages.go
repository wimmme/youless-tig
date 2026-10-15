package youless

import "time"

// Span is the stretch of time a log page covers: Count values from Start.
type Span struct {
	Start    time.Time
	Interval time.Duration
	Count    int
}

// Time is the start of the interval of value i (calendar days for day logs).
func (s Span) Time(i int) time.Time {
	if s.Interval == 24*time.Hour {
		return s.Start.AddDate(0, 0, i)
	}
	return s.Start.Add(time.Duration(i) * s.Interval)
}

// End is the end of the last interval.
func (s Span) End() time.Time { return s.Time(s.Count) }

// valuesPerPage of the rolling logs (minute and 10-minute).
func valuesPerPage(r Resolution) int {
	if r == Minute {
		return 30
	}
	return 48
}

// PageSpan is what an LS120 shows on a page of the minute, 10-minute and hour
// logs at time now, as observed on the device (firmware 1.6.4):
//
//   - minute (?h=N) and 10-minute (?w=N) pages are rolling: page 1 holds the 30
//     (48) most recent complete intervals, page 2 the ones before, and so on;
//   - hour pages (?d=N) are calendar days: page 1 is today from midnight up to the
//     last complete hour, page 2 yesterday, and so on.
//
// The device shows complete intervals only. Day logs use MonthSpan.
func PageSpan(r Resolution, page int, now time.Time, loc *time.Location) Span {
	now = now.In(loc)
	dt := r.Interval()
	switch r {
	case Minute, TenMinutes:
		n := valuesPerPage(r)
		last := now.Truncate(dt).Add(-dt) // start of the last complete interval
		start := last.Add(-time.Duration(n-1) * dt).Add(-time.Duration((page-1)*n) * dt)
		return Span{Start: start.In(loc), Interval: dt, Count: n}
	case Hour:
		y, m, d := now.Date()
		start := time.Date(y, m, d-(page-1), 0, 0, 0, 0, loc)
		end := time.Date(y, m, d-(page-1)+1, 0, 0, 0, 0, loc)
		if page == 1 {
			end = now.Truncate(time.Hour)
		}
		return Span{Start: start, Interval: dt, Count: int(end.Sub(start) / dt)}
	}
	return Span{}
}

// MonthSpan is the day log page of a month (?m=N): the days of that month up to
// yesterday. With year 0 it is the most recent month with that number, the
// current one included, as on the device (which keeps one year).
func MonthSpan(month int, year int, now time.Time, loc *time.Location) Span {
	now = now.In(loc)
	if year == 0 {
		year = now.Year()
		if time.Month(month) > now.Month() {
			year--
		}
	}
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 1, 0)
	if today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc); end.After(today) {
		end = today
	}
	count := 0
	for t := start; t.Before(end); t = t.AddDate(0, 0, 1) {
		count++
	}
	return Span{Start: start, Interval: 24 * time.Hour, Count: count}
}
