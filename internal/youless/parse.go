package youless

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// LocalTimeLayout is how log pages write their first timestamp: local device
// time without a zone.
const LocalTimeLayout = "2006-01-02T15:04:05"

// ParseDevice parses a /d response.
func ParseDevice(body []byte) (*Device, error) {
	var d Device
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("parse /d: %w", err)
	}
	if d.Model == "" {
		return nil, errors.New("parse /d: no model")
	}
	return &d, nil
}

// ParseEnergy parses an /e response. The device sends an array of which only
// the first element is used.
func ParseEnergy(body []byte) (*Energy, error) {
	var es []Energy
	if err := json.Unmarshal(body, &es); err != nil {
		return nil, fmt.Errorf("parse /e: %w", err)
	}
	if len(es) == 0 {
		return nil, errors.New("parse /e: empty array")
	}
	if es[0].Time <= 0 {
		return nil, errors.New("parse /e: no timestamp")
	}
	return &es[0], nil
}

// ParsePhases parses an /f response.
func ParsePhases(body []byte) (*Phases, error) {
	var f Phases
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("parse /f: %w", err)
	}
	return &f, nil
}

// ParseLocalStamp converts a yymmddhhmm number (gts, wts, pts) in the device's
// local time zone. Zero means "never" and returns ok == false.
func ParseLocalStamp(v int64, loc *time.Location) (t time.Time, ok bool) {
	if v <= 0 {
		return time.Time{}, false
	}
	mi := int(v % 100)
	h := int(v / 100 % 100)
	d := int(v / 10000 % 100)
	mo := int(v / 1000000 % 100)
	y := int(v / 100000000)
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || y > 99 {
		return time.Time{}, false
	}
	return time.Date(2000+y, time.Month(mo), d, h, mi, 0, 0, loc), true
}

// LogPage is one page of a log: values from Start, Interval apart. A nil value
// is a "*" on the device: no data for that interval.
type LogPage struct {
	Unit     string
	Start    time.Time
	Interval time.Duration
	Values   []*float64
}

// Time is the start of the interval of value i. Day logs step in calendar days
// so a daylight-saving change does not shift them.
func (p *LogPage) Time(i int) time.Time {
	if p.Interval == 24*time.Hour {
		return p.Start.AddDate(0, 0, i)
	}
	return p.Start.Add(time.Duration(i) * p.Interval)
}

type rawLogPage struct {
	Unit     string    `json:"un"`
	Start    string    `json:"tm"`
	Interval int       `json:"dt"`
	Values   []*string `json:"val"`
}

// ParseLogPage parses a log page (/V?h=1 and friends). Values are text with a
// decimal comma; the array ends with null.
func ParseLogPage(body []byte, loc *time.Location) (*LogPage, error) {
	var raw rawLogPage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse log page: %w", err)
	}
	if raw.Interval <= 0 {
		return nil, fmt.Errorf("parse log page: interval %d", raw.Interval)
	}
	start, err := time.ParseInLocation(LocalTimeLayout, raw.Start, loc)
	if err != nil {
		return nil, fmt.Errorf("parse log page: tm: %w", err)
	}
	p := &LogPage{Unit: raw.Unit, Start: start, Interval: time.Duration(raw.Interval) * time.Second}
	for i, s := range raw.Values {
		if s == nil {
			if i != len(raw.Values)-1 {
				return nil, fmt.Errorf("parse log page: null at %d of %d", i, len(raw.Values))
			}
			break
		}
		v, err := ParseLogValue(*s)
		if err != nil {
			return nil, fmt.Errorf("parse log page: value %d: %w", i, err)
		}
		p.Values = append(p.Values, v)
	}
	return p, nil
}

// ParseLogValue parses one log value: " 37,890", "-498", or "*" (nil, no data).
func ParseLogValue(s string) (*float64, error) {
	s = strings.TrimSpace(s)
	if s == "*" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, fmt.Errorf("not a number: %q", s)
	}
	return &f, nil
}

// decimals is how many decimals the device writes for a unit: consumption
// (kWh, m³) has three, flows (Watt, litre) are whole numbers.
func decimals(unit string) int {
	switch unit {
	case "kWh", "m3":
		return 3
	}
	return 0
}

// FormatLogValue writes a value the way the device does: a sign column (a space
// or "-"), digits, a decimal comma; nil is "*".
func FormatLogValue(v *float64, unit string) string {
	if v == nil {
		return "*"
	}
	n := decimals(unit)
	scale := math.Pow10(n)
	r := math.Round(*v * scale)
	sign := " "
	if r < 0 {
		sign = "-"
	}
	r = math.Abs(r) // also turns a rounded -0 into 0
	digits := strconv.FormatFloat(r/scale, 'f', n, 64)
	return sign + strings.Replace(digits, ".", ",", 1)
}

// FormatLogPage writes a log page byte for byte as the device would.
func FormatLogPage(p *LogPage) []byte {
	var b strings.Builder
	b.WriteString(`{"un":`)
	b.WriteString(strconv.Quote(p.Unit))
	b.WriteString(`,"tm":"`)
	b.WriteString(p.Start.Format(LocalTimeLayout))
	b.WriteString(`","dt":`)
	b.WriteString(strconv.Itoa(int(p.Interval / time.Second)))
	b.WriteString(`,"val":[`)
	for _, v := range p.Values {
		b.WriteString(strconv.Quote(FormatLogValue(v, p.Unit)))
		b.WriteByte(',')
	}
	b.WriteString("null]}")
	return []byte(b.String())
}
