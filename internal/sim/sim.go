// Package sim is a simulated YouLess LS120 for tests and demos. It answers the
// read endpoints (/d, /e, /f, /a, the logs) with deterministic data that follows
// the clock, using the device's paging and formatting (internal/youless), and can
// require a password like the real device.
package sim

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wimmme/youless-tig/internal/youless"
)

// Sim is one simulated device. Zero values are usable; set fields before serving.
type Sim struct {
	Loc      *time.Location
	Now      func() time.Time // default time.Now
	Password string           // empty: no login needed
	MAC      string           // default 00:00:5e:00:53:01 (documentation range)
	NoP1     bool             // no smart meter on P1: no p1…wts in /e, zeros in /f
	NoGas    bool
	NoWater  bool

	mu       sync.Mutex
	sessions map[string]bool
	requests int
}

// Requests is how many requests the simulator has answered (for tests that check
// device load).
func (s *Sim) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *Sim) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sim) loc() *time.Location {
	if s.Loc != nil {
		return s.Loc
	}
	return time.Local
}

// ServeHTTP implements the device's read API.
func (s *Sim) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/L" {
		s.login(w, r)
		return
	}
	if !s.authorised(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	switch r.URL.Path {
	case "/d":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprintf(w, `{"model":"LS120","fw":"1.6.4-PO","mac":%q}`, s.mac())
	case "/e":
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.energy())
	case "/f":
		w.Header().Set("Content-Type", "application/json")
		w.Write(s.phases())
	case "/a":
		if q.Get("f") == "j" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(s.statusJSON())
		} else {
			w.Header().Set("Content-Type", "text/plain")
			w.Write(s.statusText())
		}
	default:
		ch, ok := youless.ChannelForPath(r.URL.Path)
		if !ok || (ch == youless.Gas && s.NoGas) || (ch == youless.Water && s.NoWater) {
			notFound(w)
			return
		}
		for _, res := range youless.Resolutions {
			if v := q.Get(res.Param()); v != "" {
				page, err := strconv.Atoi(v)
				if err != nil || page < 1 || page > res.DevicePages() {
					notFound(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(youless.FormatLogPage(s.LogPage(ch, res, page)))
				return
			}
		}
		notFound(w)
	}
}

func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("<h1>404 Niet Gevonden</h1>"))
}

func (s *Sim) mac() string {
	if s.MAC != "" {
		return s.MAC
	}
	return "00:00:5e:00:53:01"
}

func (s *Sim) login(w http.ResponseWriter, r *http.Request) {
	if s.Password == "" || r.URL.Query().Get("w") != s.Password {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	b := make([]byte, 8)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]bool{}
	}
	s.sessions[tok] = true
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "tk", Value: tok, Path: "/"})
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusFound)
}

func (s *Sim) authorised(r *http.Request) bool {
	if s.Password == "" {
		return true
	}
	c, err := r.Cookie("tk")
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[c.Value]
}

// ── simulated quantities ─────────────────────────────────────────────────────

// hash01 is a deterministic pseudo-random number in [0, 1) for an integer.
func hash01(n int64, salt uint64) float64 {
	x := uint64(n)*0x9E3779B97F4A7C15 ^ salt
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 29
	return float64(x%1_000_000) / 1_000_000
}

// PowerAt is the net power (W) of the minute that starts at t: a base load,
// a daily pattern, noise, and a solar dip below zero around noon.
func (s *Sim) PowerAt(t time.Time) int {
	m := t.Unix() / 60
	lt := t.In(s.loc())
	hour := float64(lt.Hour()) + float64(lt.Minute())/60
	load := 250 + 400*math.Max(0, math.Sin((hour-6)/16*math.Pi)) + 300*hash01(m, 1)
	solar := 2500 * math.Max(0, math.Sin((hour-7)/12*math.Pi))
	return int(math.Round(load - solar))
}

// gasFlowAt is the gas flow (litre/hour) of the 10 minutes that start at t.
func (s *Sim) gasFlowAt(t time.Time) int {
	return int(math.Round(30 + 60*hash01(t.Unix()/600, 2)))
}

// waterFlowAt is the water flow (litre/hour) of the 10 minutes that start at t.
func (s *Sim) waterFlowAt(t time.Time) int {
	if hash01(t.Unix()/600, 3) < 0.8 {
		return 0
	}
	return int(math.Round(60 * hash01(t.Unix()/600, 4)))
}

// average of a per-step function over [start, start+d).
func average(start time.Time, d, step time.Duration, f func(time.Time) int) float64 {
	sum, n := 0, 0
	for t := start; t.Before(start.Add(d)); t = t.Add(step) {
		sum += f(t)
		n++
	}
	return float64(sum) / float64(n)
}

// value is the log value of an interval: an average flow for minute, 10-minute
// and hour logs, the consumption (kWh or m³) for the day log.
func (s *Sim) value(ch youless.Channel, r youless.Resolution, start time.Time, d time.Duration) *float64 {
	var v float64
	switch ch {
	case youless.Electricity:
		v = average(start, d, time.Minute, s.PowerAt)
	case youless.S0:
		v = 0
	case youless.Gas, youless.Water:
		if r == youless.Minute {
			return nil // the device keeps no minute log for gas and water
		}
		f := s.gasFlowAt
		if ch == youless.Water {
			f = s.waterFlowAt
		}
		v = average(start, d, 10*time.Minute, f)
	}
	if r == youless.Day {
		v = v * d.Hours() / 1000 // W → kWh, L/h → m³
	}
	return &v
}

// LogPage builds a log page as the device would show it now. For the day log,
// page is the month number.
func (s *Sim) LogPage(ch youless.Channel, r youless.Resolution, page int) *youless.LogPage {
	now, loc := s.now(), s.loc()
	var sp youless.Span
	if r == youless.Day {
		sp = youless.MonthSpan(page, 0, now, loc)
	} else {
		sp = youless.PageSpan(r, page, now, loc)
	}
	p := &youless.LogPage{Unit: youless.Unit(ch, r), Start: sp.Start, Interval: sp.Interval}
	for i := 0; i < sp.Count; i++ {
		start := sp.Time(i)
		p.Values = append(p.Values, s.value(ch, r, start, sp.Time(i+1).Sub(start)))
	}
	return p
}

// ── live endpoints ───────────────────────────────────────────────────────────

func (s *Sim) counters(now time.Time) (net, p1, p2, n1, n2, gas, water float64) {
	h := float64(now.Unix()-1_700_000_000) / 3600
	p1, p2 = 8000+0.15*h, 11000+0.2*h
	n1, n2 = 5000+0.1*h, 2500+0.05*h
	net = p1 + p2 - n1 - n2
	gas, water = 15000+0.12*h, 200+0.01*h
	return
}

func stamp(t time.Time) int64 {
	y, mo, d := t.Date()
	return int64(y%100)*100000000 + int64(mo)*1000000 + int64(d)*10000 + int64(t.Hour())*100 + int64(t.Minute())
}

func (s *Sim) energy() []byte {
	now := s.now()
	net, p1, p2, n1, n2, gas, water := s.counters(now)
	pwr := s.PowerAt(now.Truncate(time.Minute))
	var b strings.Builder
	fmt.Fprintf(&b, `[{"tm":%d,"net": %.3f,"pwr": %d,"ts0":1700000000,"cs0": 0.000,"ps0": 0`, now.Unix(), net, pwr)
	if !s.NoP1 {
		fmt.Fprintf(&b, `,"p1": %.3f,"p2": %.3f,"n1": %.3f,"n2": %.3f`, p1, p2, n1, n2)
		mbus := now.In(s.loc()).Truncate(5 * time.Minute)
		if !s.NoGas {
			fmt.Fprintf(&b, `,"gas": %.3f,"gts":%d`, gas, stamp(mbus))
		}
		if !s.NoWater {
			fmt.Fprintf(&b, `,"wtr": %.3f,"wts":%d`, water, stamp(mbus))
		}
	}
	b.WriteString("}]")
	return []byte(b.String())
}

func (s *Sim) phases() []byte {
	if s.NoP1 {
		return []byte(`{"tr": 0,"pa": 0,"pp": 0,"pts":0,"i1": 0.000,"i2": 0.000,"i3": 0.000,"v1": 0.000,"v2": 0.000,"v3": 0.000,"l1": 0,"l2": 0,"l3": 0}`)
	}
	now := s.now()
	pwr := s.PowerAt(now.Truncate(time.Minute))
	l1, l2 := pwr/3, pwr/3
	l3 := pwr - l1 - l2
	tr := 1
	if h := now.In(s.loc()).Hour(); h >= 7 && h < 22 {
		tr = 2
	}
	peak := time.Date(now.Year(), now.Month(), 1, 18, 15, 0, 0, s.loc())
	return []byte(fmt.Sprintf(`{"tr": %d,"pa": %d,"pp": 5120,"pts":%d,"i1": %.3f,"i2": %.3f,"i3": %.3f,"v1": 231.400,"v2": 232.100,"v3": 230.800,"l1": %d,"l2": %d,"l3": %d}`,
		tr, max(pwr, 0), stamp(peak), math.Abs(float64(l1))/230, math.Abs(float64(l2))/230, math.Abs(float64(l3))/230, l1, l2, l3))
}

func comma3(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 3, 64), ".", ",", 1)
}

func (s *Sim) statusJSON() []byte {
	now := s.now()
	net, _, _, _, _, _, _ := s.counters(now)
	return []byte(fmt.Sprintf(`{"cnt":" %s","pwr":%d,"lvl":0,"dev":"","det":"","con":"","sts":"","cs0":" 0,000","ps0":0,"raw":0}`,
		comma3(net), s.PowerAt(now.Truncate(time.Minute))))
}

func (s *Sim) statusText() []byte {
	now := s.now()
	net, _, _, _, _, _, _ := s.counters(now)
	return []byte(fmt.Sprintf(" %s kWh\n%d Watt\n0%%  &#x25A0;\n \n 0,000 kWh\n0 Watt\n0\n", comma3(net), s.PowerAt(now.Truncate(time.Minute))))
}
