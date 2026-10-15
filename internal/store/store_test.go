package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/wimmme/youless-tig/internal/youless"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ls120", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPointsFromFixtures(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Brussels")
	s := &Store{Device: "huis"}
	e, err := youless.ParseEnergy(fixture(t, "e.json"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := youless.ParsePhases(fixture(t, "f.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := s.ElectricityPoint(e, f, loc)
	line, err := p.Line()
	if err != nil {
		t.Fatal(err)
	}
	want := "youless_electricity,device=huis capacity_avg_w=7,capacity_peak_time=1791370800i,capacity_peak_w=5170," +
		"current_l1_a=1.53,current_l2_a=1.03,current_l3_a=1.92,export_t1_kwh=1656.272,export_t2_kwh=9069.901," +
		"import_t1_kwh=8054.668,import_t2_kwh=99373.467,net_kwh=33166.587,power_l1_w=7,power_l2_w=0,power_l3_w=-7," +
		"power_w=7,tariff=2i,voltage_l1_v=231.9,voltage_l2_v=232.9,voltage_l3_v=234.1 1792021455"
	if line != want {
		t.Errorf("\n got %s\nwant %s", line, want)
	}

	gas, ok := s.MeterPoint(Gas, e.Gas, e.GasTime, loc)
	if !ok || !gas.Time.Equal(time.Date(2026, 10, 15, 1, 39, 0, 0, loc)) || gas.Fields["total_m3"] != 72720.465 {
		t.Errorf("gas %+v", gas)
	}
	if _, ok := s.MeterPoint(Water, nil, nil, loc); ok {
		t.Error("water without reading")
	}
	s0, ok := s.S0Point(e)
	if !ok || s0.Time.Unix() != 1790163600 {
		t.Errorf("s0 %+v", s0)
	}
}

// A single-phase meter sends no voltage for L2 and L3: those phases are left out.
func TestSinglePhase(t *testing.T) {
	s := &Store{Device: "x"}
	e, _ := youless.ParseEnergy([]byte(`[{"tm":1,"net": 1.000,"pwr": 5,"p1": 1.000,"p2": 1.000,"n1": 0.000,"n2": 0.000}]`))
	f, _ := youless.ParsePhases([]byte(`{"tr": 1,"pa": 0,"pp": 0,"pts":0,"i1": 1.000,"i2": 0.000,"i3": 0.000,"v1": 230.000,"v2": 0.000,"v3": 0.000,"l1": 5,"l2": 0,"l3": 0}`))
	p := s.ElectricityPoint(e, f, time.UTC)
	for k := range p.Fields {
		if strings.Contains(k, "l2") || strings.Contains(k, "l3") || strings.HasPrefix(k, "capacity") {
			t.Errorf("unexpected field %s", k)
		}
	}
	if p.Fields["voltage_l1_v"] != 230.0 {
		t.Errorf("fields %v", p.Fields)
	}
}

func TestLogPoints(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Brussels")
	s := &Store{Device: "huis"}
	page, err := youless.ParseLogPage(fixture(t, "V_h1.json"), loc)
	if err != nil {
		t.Fatal(err)
	}
	pts := s.LogPoints(youless.Electricity, youless.Minute, page, time.Time{})
	if len(pts) != 30 || pts[0].Tags["channel"] != "electricity" || pts[0].Tags["resolution"] != "minute" || !pts[0].Time.Equal(page.Start) {
		t.Fatalf("points %+v", pts[0])
	}
	after := s.LogPoints(youless.Electricity, youless.Minute, page, page.Time(19))
	if len(after) != 10 {
		t.Errorf("after value 19: %d points", len(after))
	}
	gas, _ := youless.ParseLogPage(fixture(t, "W_h1.json"), loc)
	if n := len(s.LogPoints(youless.Gas, youless.Minute, gas, time.Time{})); n != 0 {
		t.Errorf("\"*\" values stored: %d", n)
	}
}

// The Grafana dashboard uses only measurements and fields of the data model.
func TestDashboardUsesDataModel(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "grafana", "dashboards", "youless.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Panels []struct {
			Title   string
			Targets []struct{ Query string }
		}
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	fields := map[string][]string{
		Electricity: {"power_w", "net_kwh", "import_t1_kwh", "import_t2_kwh", "export_t1_kwh", "export_t2_kwh", "tariff",
			"voltage_l1_v", "voltage_l2_v", "voltage_l3_v", "current_l1_a", "current_l2_a", "current_l3_a",
			"power_l1_w", "power_l2_w", "power_l3_w", "capacity_avg_w", "capacity_peak_w", "capacity_peak_time"},
		Gas: {"total_m3"}, Water: {"total_m3"}, S0: {"total_kwh", "power_w"}, Log: {"value"},
	}
	from := regexp.MustCompile(`FROM "([a-z_]+)"`)
	quoted := regexp.MustCompile(`(?:mean|last|spread|SELECT)\(?\s*"([a-z0-9_]+)"`)
	queries := 0
	for _, p := range d.Panels {
		for _, tg := range p.Targets {
			queries++
			m := from.FindStringSubmatch(tg.Query)
			if m == nil {
				t.Errorf("%s: no FROM in %q", p.Title, tg.Query)
				continue
			}
			known, ok := fields[m[1]]
			if !ok {
				t.Errorf("%s: unknown measurement %s", p.Title, m[1])
				continue
			}
			for _, f := range quoted.FindAllStringSubmatch(tg.Query, -1) {
				if !slices.Contains(known, f[1]) {
					t.Errorf("%s: %s has no field %s", p.Title, m[1], f[1])
				}
			}
		}
	}
	if queries < 15 {
		t.Errorf("only %d queries", queries)
	}
}

// docs/data-model.md names every measurement and field this package writes.
func TestDataModelDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile("store.go")
	names := regexp.MustCompile(`"([a-z0-9_]+_(?:w|kwh|v|a|m3|time)|tariff|value|youless_[a-z]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(names) < 10 {
		t.Fatalf("found only %d names in store.go", len(names))
	}
	for _, m := range names {
		if !strings.Contains(string(doc), "`"+m[1]+"`") {
			t.Errorf("docs/data-model.md does not mention `%s`", m[1])
		}
	}
	// Phase fields are built in a loop: voltage_l1_v … power_l3_w.
	for _, f := range []string{"voltage_l1_v", "current_l1_a", "power_l1_w"} {
		if !strings.Contains(string(doc), "`"+f+"`") {
			t.Errorf("docs/data-model.md does not mention `%s`", f)
		}
	}
}
