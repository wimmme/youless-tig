package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/wimmme/youless-tig/internal/collector"
	"github.com/wimmme/youless-tig/internal/influx"
	"github.com/wimmme/youless-tig/internal/influxtest"
	"github.com/wimmme/youless-tig/internal/sim"
	"github.com/wimmme/youless-tig/internal/store"
	"github.com/wimmme/youless-tig/internal/youless"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	sim    *sim.Sim
	device *httptest.Server
	api    *httptest.Server
	db     *influxtest.Server
	col    *collector.Collector
	clk    *clock
	loc    *time.Location
}

func setup(t *testing.T, history bool) *env {
	t.Helper()
	loc, _ := time.LoadLocation("Europe/Brussels")
	clk := &clock{t: time.Date(2026, 10, 15, 1, 44, 15, 0, loc)}
	s := &sim.Sim{Loc: loc, Now: clk.Now}
	device := httptest.NewServer(s)
	t.Cleanup(device.Close)
	dev, _ := youless.NewClient(device.URL, "", 5*time.Second)
	dev.Gap = 0
	db := influxtest.New(t)
	st := &store.Store{DB: &influx.Client{URL: db.URL, Org: "o", Bucket: "b"}, Device: "youless"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	col := collector.New(collector.Config{Loc: loc, PollInterval: 10 * time.Second, History: history, Now: clk.Now}, dev, st, log)
	srv := &Server{C: col, St: st, Loc: loc, PollInterval: 10 * time.Second, Log: log, Now: clk.Now}
	api := httptest.NewServer(srv.Handler())
	t.Cleanup(api.Close)
	return &env{sim: s, device: device, api: api, db: db, col: col, clk: clk, loc: loc}
}

func get(t *testing.T, base, path string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (e *env) backfill(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := e.col.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, ch := range e.col.Channels() {
		for _, r := range youless.Resolutions {
			if err := e.col.SyncLog(ctx, ch, r); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Every log page the device has is answered byte for byte as the device does.
func TestLogPagesMatchDevice(t *testing.T) {
	e := setup(t, true)
	e.backfill(t)
	for _, ch := range youless.Channels {
		if ch == youless.S0 {
			continue // not collected by default
		}
		for _, r := range youless.Resolutions {
			for page := 1; page <= r.DevicePages(); page++ {
				path := fmt.Sprintf("%s?%s=%d&f=j", ch.Path(), r.Param(), page)
				_, want, _ := get(t, e.device.URL, path)
				code, got, h := get(t, e.api.URL, path)
				if code != 200 || got != want {
					t.Fatalf("%s: HTTP %d\n got %s\nwant %s", path, code, got, want)
				}
				if h.Get("Content-Type") != "application/json" {
					t.Fatalf("%s: content type %q", path, h.Get("Content-Type"))
				}
			}
		}
	}
}

// Later on, recent pages are brought up to date from the device first.
func TestRecentPageIsRefreshed(t *testing.T) {
	e := setup(t, true)
	e.backfill(t)
	e.clk.Add(7 * time.Minute)
	for _, path := range []string{"/V?h=1", "/V?w=1", "/V?d=1"} {
		_, want, _ := get(t, e.device.URL, path)
		if _, got, _ := get(t, e.api.URL, path); got != want {
			t.Errorf("%s after 7 minutes:\n got %s\nwant %s", path, got, want)
		}
	}
}

// Beyond the device's range the API answers from the archive.
func TestLogPagesBeyondDevice(t *testing.T) {
	e := setup(t, true)
	e.backfill(t)
	if code, _, _ := get(t, e.device.URL, "/V?d=71"); code != 404 {
		t.Fatalf("device page 71: %d", code)
	}
	if code, _, _ := get(t, e.api.URL, "/V?d=71"); code != 404 {
		t.Errorf("page 71 without stored data: %d, want 404", code)
	}
	// Store 24 hours of a day 100 days ago, as an earlier run would have.
	day := youless.PageSpan(youless.Hour, 101, e.clk.Now(), e.loc)
	for i := 0; i < day.Count; i++ {
		e.db.Add(influxtest.Point{Measurement: store.Log,
			Tags:   map[string]string{"device": "youless", "channel": "electricity", "resolution": "hour"},
			Fields: map[string]any{"value": float64(100 + i)}, Time: day.Time(i)})
	}
	code, body, _ := get(t, e.api.URL, "/V?d=101")
	want := youless.FormatLogPage(&youless.LogPage{Unit: "Watt", Start: day.Start, Interval: time.Hour})
	if code != 200 || !strings.HasPrefix(body, string(want[:strings.Index(string(want), "[")])) || !strings.Contains(body, `" 100"," 101"`) {
		t.Errorf("page 101: %d %s", code, body)
	}

	// ?y= selects a year of the day log.
	m := youless.MonthSpan(3, 2024, e.clk.Now(), e.loc)
	e.db.Add(influxtest.Point{Measurement: store.Log,
		Tags:   map[string]string{"device": "youless", "channel": "electricity", "resolution": "day"},
		Fields: map[string]any{"value": 12.345}, Time: m.Time(4)})
	code, body, _ = get(t, e.api.URL, "/V?m=3&y=2024")
	if code != 200 || !strings.Contains(body, `"tm":"2024-03-01T00:00:00"`) || !strings.Contains(body, `"*"," 12,345","*"`) {
		t.Errorf("m=3&y=2024: %d %s", code, body)
	}
	if code, _, _ := get(t, e.api.URL, "/V?m=3&y=2023"); code != 404 {
		t.Errorf("year without data: %d", code)
	}
	if code, _, _ := get(t, e.api.URL, "/V?m=12&y=2030"); code != 404 {
		t.Errorf("future year: %d", code)
	}
}

func TestBadLogRequests(t *testing.T) {
	e := setup(t, true)
	for _, path := range []string{"/V", "/V?f=j", "/V?h=0", "/V?h=-1", "/V?h=x", "/V?m=13", "/V?m=1&y=99", "/V?h=99999999", "/X?h=1", "/nothing"} {
		code, body, _ := get(t, e.api.URL, path)
		if code != 404 || body != "<h1>404 Niet Gevonden</h1>" {
			t.Errorf("%s: %d %q", path, code, body)
		}
	}
}

func TestLiveEndpoints(t *testing.T) {
	e := setup(t, false)
	if code, _, _ := get(t, e.api.URL, "/e"); code != 503 {
		t.Errorf("/e before the first poll: %d", code)
	}
	if err := e.col.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/e", "/f", "/d", "/a?f=j"} {
		_, want, _ := get(t, e.device.URL, p)
		code, got, h := get(t, e.api.URL, p)
		if code != 200 || got != want || h.Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %q %q, want %q", p, code, h.Get("Content-Type"), got, want)
		}
	}
	_, want, _ := get(t, e.device.URL, "/a")
	if code, got, h := get(t, e.api.URL, "/a"); code != 200 || got != want || h.Get("Content-Type") != "text/plain" {
		t.Errorf("/a: %d %q", code, got)
	}
	if _, _, h := get(t, e.api.URL, "/d"); h.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("/d without CORS header (the device sends it)")
	}
	// A minute without a successful poll: /e is stale.
	e.clk.Add(time.Minute + time.Second)
	if code, _, _ := get(t, e.api.URL, "/e"); code != 503 {
		t.Errorf("stale /e: %d", code)
	}
}

func TestMethodsAndHeaders(t *testing.T) {
	e := setup(t, false)
	resp, err := http.Post(e.api.URL+"/e", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d", resp.StatusCode)
	}
	// Device settings paths are not forwarded.
	for _, p := range []string{"/L?w=x", "/S?t=1", "/M?c=1"} {
		if code, _, _ := get(t, e.api.URL, p); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
	before := e.sim.Requests()
	get(t, e.api.URL, "/L?w=x")
	if e.sim.Requests() != before {
		t.Error("request reached the device")
	}
	if _, _, h := get(t, e.api.URL, "/healthz"); h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff header missing")
	}
}

func TestHealth(t *testing.T) {
	e := setup(t, false)
	code, body, _ := get(t, e.api.URL, "/healthz")
	if code != 200 || !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("starting: %d %s", code, body)
	}
	e.clk.Add(3 * time.Minute)
	if code, _, _ := get(t, e.api.URL, "/healthz"); code != 503 {
		t.Errorf("never polled after 3 minutes: %d", code)
	}
	if err := e.col.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, body, _ = get(t, e.api.URL, "/healthz")
	var h health
	json.Unmarshal([]byte(body), &h)
	if code != 200 || h.LastPoll == nil || len(h.Channels) != 3 {
		t.Errorf("after a poll: %d %s", code, body)
	}
	e.clk.Add(6 * time.Minute)
	if code, _, _ := get(t, e.api.URL, "/healthz"); code != 503 {
		t.Errorf("no poll for 6 minutes: %d", code)
	}
}

// When InfluxDB is down, log pages answer 503 and live data keeps working.
func TestStorageDown(t *testing.T) {
	e := setup(t, true)
	if err := e.col.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.db.SetFail(true)
	if code, _, _ := get(t, e.api.URL, "/V?h=1"); code != 503 {
		t.Errorf("log page: %d", code)
	}
	if code, _, _ := get(t, e.api.URL, "/e"); code != 200 {
		t.Errorf("/e: %d", code)
	}
}
