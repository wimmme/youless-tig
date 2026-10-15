package youless

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

// The fixtures in testdata/ls120 were recorded from an LS120 (firmware
// 1.6.4-PO) in Belgium at Unix time captured, then scrubbed: MAC address and
// meter values are made up, format and timestamps are as recorded.
const captured = 1792021455

func brussels(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ls120", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseDevice(t *testing.T) {
	d, err := ParseDevice(fixture(t, "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Model != "LS120" || d.Firmware != "1.6.4-PO" || d.MAC != "00:00:5e:00:53:01" {
		t.Errorf("got %+v", d)
	}
	if _, err := ParseDevice([]byte(`{}`)); err == nil {
		t.Error("no model: want error")
	}
}

func TestParseEnergy(t *testing.T) {
	e, err := ParseEnergy(fixture(t, "e.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Time != captured || *e.Net != 33166.587 || *e.Power != 7 || *e.Import1 != 8054.668 ||
		*e.Export2 != 9069.901 || *e.Gas != 72720.465 || *e.GasTime != 2610150139 || *e.Water != 475.564 {
		t.Errorf("got %+v", e)
	}
	if _, err := ParseEnergy([]byte(`[]`)); err == nil {
		t.Error("empty array: want error")
	}
	if _, err := ParseEnergy([]byte(`[{"net":1}]`)); err == nil {
		t.Error("no tm: want error")
	}
	// Without P1 the P1 fields are absent.
	e, err = ParseEnergy([]byte(`[{"tm":1,"net": 1.000,"pwr": 5,"ts0":0,"cs0": 0.000,"ps0": 0}]`))
	if err != nil || e.Import1 != nil || e.Gas != nil {
		t.Errorf("no P1: %+v %v", e, err)
	}
}

func TestParsePhases(t *testing.T) {
	f, err := ParsePhases(fixture(t, "f.json"))
	if err != nil {
		t.Fatal(err)
	}
	if *f.Tariff != 2 || *f.CapacityPeak != 5170 || *f.Voltage3 != 234.1 || *f.Power3 != -7 || *f.Current1 != 1.53 {
		t.Errorf("got %+v", f)
	}
}

func TestParseLocalStamp(t *testing.T) {
	loc := brussels(t)
	got, ok := ParseLocalStamp(2610150139, loc)
	if want := time.Date(2026, 10, 15, 1, 39, 0, 0, loc); !ok || !got.Equal(want) {
		t.Errorf("got %v %v, want %v", got, ok, want)
	}
	for _, bad := range []int64{0, -1, 2613150139, 2610320139, 2610152439, 2610150160} {
		if _, ok := ParseLocalStamp(bad, loc); ok {
			t.Errorf("%d: want not ok", bad)
		}
	}
}

// Every log fixture parses and formats back to exactly the recorded bytes.
func TestLogPageRoundTrip(t *testing.T) {
	loc := brussels(t)
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "ls120", "[VWKZ]_*.json"))
	if len(files) < 16 {
		t.Fatalf("only %d log fixtures", len(files))
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			b := fixture(t, name)
			p, err := ParseLogPage(b, loc)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(FormatLogPage(p)); got != string(b) {
				t.Errorf("round trip:\n got %s\nwant %s", got, b)
			}
		})
	}
}

func TestParseLogPage(t *testing.T) {
	loc := brussels(t)
	p, err := ParseLogPage(fixture(t, "V_m10.json"), loc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Unit != "kWh" || p.Interval != 24*time.Hour || len(p.Values) != 14 || *p.Values[0] != 58.932 {
		t.Errorf("got %+v", p)
	}
	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, loc); !p.Start.Equal(want) {
		t.Errorf("start %v, want %v", p.Start, want)
	}
	if got := p.Time(13); !got.Equal(time.Date(2026, 10, 14, 0, 0, 0, 0, loc)) {
		t.Errorf("time 13: %v", got)
	}

	p, err = ParseLogPage(fixture(t, "W_h1.json"), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Values) != 30 || p.Values[0] != nil {
		t.Errorf("gas minute log: want 30 nil values, got %d", len(p.Values))
	}

	for _, bad := range []string{
		`{"un":"Watt","tm":"2026-10-15T01:09:00","dt":60,"val":[" 1",null," 2",null]}`,
		`{"un":"Watt","tm":"2026-10-15T01:09:00","dt":60,"val":["x",null]}`,
		`{"un":"Watt","tm":"2026-10-15T01:09:00","dt":0,"val":[null]}`,
		`{"un":"Watt","tm":"15-10-2026","dt":60,"val":[null]}`,
		`<h1>404 Niet Gevonden</h1>`,
	} {
		if _, err := ParseLogPage([]byte(bad), loc); err == nil {
			t.Errorf("%s: want error", bad)
		}
	}
}

func TestFormatLogValue(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		v    *float64
		unit string
		want string
	}{
		{nil, "Watt", "*"},
		{f(2), "Watt", " 2"},
		{f(-498), "Watt", "-498"},
		{f(10566.4), "Watt", " 10566"},
		{f(-0.4), "Watt", " 0"},
		{f(37.89), "kWh", " 37,890"},
		{f(-0.73), "kWh", "-0,730"},
		{f(0), "kWh", " 0,000"},
		{f(1.0005), "m3", " 1,001"},
		{f(905), "L", " 905"},
	} {
		if got := FormatLogValue(c.v, c.unit); got != c.want {
			t.Errorf("%v %s: got %q, want %q", c.v, c.unit, got, c.want)
		}
	}
}

func TestParseLogValue(t *testing.T) {
	v, err := ParseLogValue(" 37,890")
	if err != nil || *v != 37.89 {
		t.Errorf("got %v %v", v, err)
	}
	if v, err := ParseLogValue("*"); v != nil || err != nil {
		t.Errorf("*: got %v %v", v, err)
	}
	for _, bad := range []string{"", "NaN", "Inf", "1,2,3"} {
		if _, err := ParseLogValue(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestChannelsAndResolutions(t *testing.T) {
	for _, c := range Channels {
		if got, ok := ChannelForPath(c.Path()); !ok || got != c {
			t.Errorf("%s: path round trip", c)
		}
		if got, err := ParseChannel(string(c)); err != nil || got != c {
			t.Errorf("%s: parse", c)
		}
	}
	if _, err := ParseChannel("power"); err == nil || !strings.Contains(err.Error(), "electricity") {
		t.Errorf("unknown channel: %v", err)
	}
	for _, r := range Resolutions {
		if got, ok := ResolutionForParam(r.Param()); !ok || got != r {
			t.Errorf("%s: param round trip", r)
		}
	}
	if Unit(Gas, Day) != "m3" || Unit(Water, Hour) != "L" || Unit(Electricity, Day) != "kWh" || Unit(S0, Minute) != "Watt" {
		t.Error("units")
	}
}
