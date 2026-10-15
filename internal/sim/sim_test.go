package sim

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/wimmme/youless-tig/internal/youless"
)

func TestSimMatchesDeviceFormats(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Brussels")
	now := time.Date(2026, 10, 15, 1, 44, 15, 0, loc)
	s := &Sim{Loc: loc, Now: func() time.Time { return now }}
	srv := httptest.NewServer(s)
	defer srv.Close()
	get := func(p string) (int, []byte) {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	if _, b := get("/d"); func() error { _, err := youless.ParseDevice(b); return err }() != nil {
		t.Errorf("/d: %s", b)
	}
	_, b := get("/e")
	e, err := youless.ParseEnergy(b)
	if err != nil || e.Time != now.Unix() || e.GasTime == nil {
		t.Errorf("/e: %s %v", b, err)
	}
	if _, b := get("/f"); func() error { _, err := youless.ParsePhases(b); return err }() != nil {
		t.Errorf("/f: %s", b)
	}
	_, b = get("/a?f=j")
	var a map[string]any
	if err := json.Unmarshal(b, &a); err != nil || a["cnt"] == nil {
		t.Errorf("/a?f=j: %s", b)
	}
	for _, c := range []struct {
		path  string
		count int
	}{{"/V?h=1", 30}, {"/V?w=30", 48}, {"/V?d=1", 1}, {"/V?d=70", 24}, {"/V?m=10", 14}, {"/W?h=1", 30}, {"/K?m=11", 30}} {
		code, b := get(c.path)
		p, err := youless.ParseLogPage(b, loc)
		if code != 200 || err != nil || len(p.Values) != c.count {
			t.Errorf("%s: %d %v %s", c.path, code, err, b)
			continue
		}
		if string(youless.FormatLogPage(p)) != string(b) {
			t.Errorf("%s: not in the device's format", c.path)
		}
	}
	for _, p := range []string{"/V?h=21", "/V?w=31", "/V?d=71", "/V?m=13", "/V?h=0", "/V", "/Q"} {
		if code, b := get(p); code != 404 || string(b) != "<h1>404 Niet Gevonden</h1>" {
			t.Errorf("%s: %d %s", p, code, b)
		}
	}
	// A day value is the day's consumption: the average of its minutes in kWh.
	day := s.LogPage(youless.Electricity, youless.Day, 10).Values[0]
	hours := 0.0
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	for i := 0; i < 24; i++ {
		hours += *s.value(youless.Electricity, youless.Hour, start.Add(time.Duration(i)*time.Hour), time.Hour)
	}
	if d := *day - hours/1000; d > 1e-9 || d < -1e-9 {
		t.Errorf("day %v kWh, hours sum to %v Wh", *day, hours)
	}
}
