package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/wimmme/youless-tig/internal/influx"
	"github.com/wimmme/youless-tig/internal/influxtest"
	"github.com/wimmme/youless-tig/internal/sim"
	"github.com/wimmme/youless-tig/internal/store"
	"github.com/wimmme/youless-tig/internal/youless"
)

// clock is a settable time shared by the simulator and the collector.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fixture struct {
	sim  *sim.Sim
	db   *influxtest.Server
	col  *Collector
	clk  *clock
	loc  *time.Location
	data *store.Store
}

func setup(t *testing.T, s *sim.Sim, channels []youless.Channel) *fixture {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 10, 15, 1, 44, 15, 0, loc)}
	s.Loc, s.Now = loc, clk.Now
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	dev, err := youless.NewClient(srv.URL, s.Password, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dev.Gap = 0
	db := influxtest.New(t)
	db.Token = "tok"
	st := &store.Store{DB: &influx.Client{URL: db.URL, Token: "tok", Org: "o", Bucket: "b"}, Device: "huis"}
	col := New(Config{Loc: loc, PollInterval: 10 * time.Second, History: true, Channels: channels}, dev, st,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	col.now = clk.Now
	return &fixture{sim: s, db: db, col: col, clk: clk, loc: loc, data: st}
}

func TestPollWritesReadings(t *testing.T) {
	f := setup(t, &sim.Sim{}, nil)
	ctx := context.Background()
	if err := f.col.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	el := f.db.Select(store.Electricity, map[string]string{"device": "huis"})
	if len(el) != 1 {
		t.Fatalf("electricity points: %d", len(el))
	}
	for _, k := range []string{"power_w", "net_kwh", "import_t1_kwh", "export_t2_kwh", "tariff", "voltage_l1_v", "power_l3_w", "capacity_peak_w", "capacity_peak_time"} {
		if _, ok := el[0].Fields[k]; !ok {
			t.Errorf("field %s missing: %v", k, el[0].Fields)
		}
	}
	if !el[0].Time.Equal(f.clk.Now().Truncate(time.Second)) {
		t.Errorf("time %v", el[0].Time)
	}
	gas := f.db.Select(store.Gas, nil)
	if len(gas) != 1 || gas[0].Time.Minute()%5 != 0 {
		t.Errorf("gas: %+v", gas)
	}
	if len(f.db.Select(store.Water, nil)) != 1 {
		t.Error("water point missing")
	}
	if len(f.db.Select(store.S0, nil)) != 0 {
		t.Error("S0 written although not enabled")
	}
	if got := f.col.Channels(); len(got) != 3 {
		t.Errorf("auto channels: %v", got)
	}

	// Same device time again: nothing new is written.
	if err := f.col.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(f.db.Select(store.Electricity, nil)); got != 1 {
		t.Errorf("after an unchanged poll: %d points", got)
	}

	// Ten seconds later: a new electricity point, same gas reading (5-minute meter).
	f.clk.Add(10 * time.Second)
	if err := f.col.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.db.Select(store.Electricity, nil)) != 2 || len(f.db.Select(store.Gas, nil)) != 1 {
		t.Error("second poll")
	}
	if b, at := f.col.Cached("/e"); b == nil || !at.Equal(f.clk.Now()) {
		t.Error("/e not cached")
	}
}

func TestPollWithoutP1(t *testing.T) {
	f := setup(t, &sim.Sim{NoP1: true}, nil)
	if err := f.col.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	el := f.db.Select(store.Electricity, nil)
	if len(el) != 1 || len(el[0].Fields) != 2 {
		t.Errorf("without P1 only power and net: %+v", el)
	}
	if got := f.col.Channels(); len(got) != 1 {
		t.Errorf("channels: %v", got)
	}
	if b, _ := f.col.Cached("/f"); b != nil {
		t.Error("/f read without P1")
	}
}

func TestPollRetriesAfterWriteFailure(t *testing.T) {
	f := setup(t, &sim.Sim{}, nil)
	f.db.SetFail(true)
	if err := f.col.Poll(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if st := f.col.Status(); st.LastError == "" || !st.LastPoll.IsZero() {
		t.Errorf("status %+v", st)
	}
	f.db.SetFail(false)
	if err := f.col.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.db.Select(store.Gas, nil)) != 1 {
		t.Error("gas reading lost after a failed write")
	}
	if st := f.col.Status(); st.LastError != "" || st.LastPoll.IsZero() {
		t.Errorf("status %+v", st)
	}
}

func logCount(f *fixture, ch youless.Channel, r youless.Resolution) int {
	return len(f.db.Select(store.Log, map[string]string{"channel": string(ch), "resolution": string(r)}))
}

func TestBackfillAndIncrementalSync(t *testing.T) {
	f := setup(t, &sim.Sim{}, nil)
	ctx := context.Background()
	if err := f.col.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, ch := range f.col.Channels() {
		for _, r := range youless.Resolutions {
			if err := f.col.SyncLog(ctx, ch, r); err != nil {
				t.Fatalf("%s %s: %v", ch, r, err)
			}
		}
	}
	// Everything the device keeps: 20×30 minutes, 30×48 ten-minute values,
	// 69 full days + 1 hour of today, and the days of the last 12 months.
	if got := logCount(f, youless.Electricity, youless.Minute); got != 600 {
		t.Errorf("minute values: %d", got)
	}
	if got := logCount(f, youless.Electricity, youless.TenMinutes); got != 1440 {
		t.Errorf("10-minute values: %d", got)
	}
	hours := 0
	for p := 1; p <= 70; p++ {
		hours += youless.PageSpan(youless.Hour, p, f.clk.Now(), f.loc).Count
	}
	if got := logCount(f, youless.Electricity, youless.Hour); got != hours || hours != 69*24+1 {
		t.Errorf("hour values: %d, want %d (69 days and today's first hour)", got, hours)
	}
	days := 0
	for i := 0; i < 12; i++ {
		days += youless.MonthSpan(int(time.Month((10-i+11)%12+1)), 0, f.clk.Now(), f.loc).Count
	}
	if got := logCount(f, youless.Electricity, youless.Day); got != days {
		t.Errorf("day values: %d, want %d", got, days)
	}
	if got := logCount(f, youless.Gas, youless.Minute); got != 0 {
		t.Errorf("gas minute values: %d (the device has none)", got)
	}
	if got := logCount(f, youless.Gas, youless.TenMinutes); got != 1440 {
		t.Errorf("gas 10-minute values: %d", got)
	}

	// Values are stored at the start of their interval, with the device's value.
	p := f.sim.LogPage(youless.Electricity, youless.Minute, 1)
	vals, err := f.data.LogValues(ctx, youless.Electricity, youless.Minute, p.Start, p.Time(30))
	if err != nil || len(vals) != 30 || vals[p.Start.Unix()] != *p.Values[0] {
		t.Errorf("stored page 1: %d values, err %v", len(vals), err)
	}

	// Fifteen minutes later only page 1 is read again.
	f.clk.Add(15 * time.Minute)
	before := f.sim.Requests()
	if err := f.col.SyncLog(ctx, youless.Electricity, youless.Minute); err != nil {
		t.Fatal(err)
	}
	if got := f.sim.Requests() - before; got != 1 {
		t.Errorf("incremental sync: %d requests, want 1", got)
	}
	if got := logCount(f, youless.Electricity, youless.Minute); got != 615 {
		t.Errorf("after 15 minutes: %d minute values", got)
	}

	// Gas minute log is skipped from now on.
	before = f.sim.Requests()
	if err := f.col.Refresh(ctx, youless.Gas, youless.Minute); err != nil {
		t.Fatal(err)
	}
	if f.sim.Requests() != before {
		t.Error("unsupported log read again")
	}
}

// After a restart the collector continues from what is stored.
func TestSyncContinuesFromStore(t *testing.T) {
	f := setup(t, &sim.Sim{}, []youless.Channel{youless.Electricity})
	ctx := context.Background()
	if err := f.col.SyncLog(ctx, youless.Electricity, youless.TenMinutes); err != nil {
		t.Fatal(err)
	}
	f.clk.Add(2 * time.Hour)
	restarted := New(f.col.cfg, f.col.dev, f.data, f.col.log)
	restarted.now = f.clk.Now
	before := f.sim.Requests()
	if err := restarted.SyncLog(ctx, youless.Electricity, youless.TenMinutes); err != nil {
		t.Fatal(err)
	}
	if got := f.sim.Requests() - before; got != 1 {
		t.Errorf("after restart: %d requests, want 1", got)
	}
	if got := logCount(f, youless.Electricity, youless.TenMinutes); got != 1440+12 {
		t.Errorf("10-minute values: %d", got)
	}
}

func TestRefreshIsThrottled(t *testing.T) {
	f := setup(t, &sim.Sim{}, []youless.Channel{youless.Electricity})
	ctx := context.Background()
	if err := f.col.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.col.Refresh(ctx, youless.Electricity, youless.Minute); err != nil {
		t.Fatal(err)
	}
	before := f.sim.Requests()
	f.clk.Add(10 * time.Second)
	f.col.Refresh(ctx, youless.Electricity, youless.Minute)
	if f.sim.Requests() != before {
		t.Error("refresh within 30 s read the device")
	}
	f.clk.Add(time.Minute)
	f.col.Refresh(ctx, youless.Electricity, youless.Minute)
	if f.sim.Requests() != before+1 {
		t.Errorf("refresh after a minute: %d requests", f.sim.Requests()-before)
	}
	// Channels that are not collected are never read.
	before = f.sim.Requests()
	f.col.Refresh(ctx, youless.Water, youless.Hour)
	if f.sim.Requests() != before {
		t.Error("disabled channel read")
	}
}

func TestFetchCaches(t *testing.T) {
	f := setup(t, &sim.Sim{}, nil)
	ctx := context.Background()
	b1, err := f.col.Fetch(ctx, "/a?f=j", 10*time.Second)
	if err != nil || len(b1) == 0 {
		t.Fatal(err)
	}
	before := f.sim.Requests()
	f.col.Fetch(ctx, "/a?f=j", 10*time.Second)
	if f.sim.Requests() != before {
		t.Error("cached /a read again")
	}
	f.clk.Add(11 * time.Second)
	f.col.Fetch(ctx, "/a?f=j", 10*time.Second)
	if f.sim.Requests() != before+1 {
		t.Error("expired /a not read")
	}
}

func TestRunStopsWithContext(t *testing.T) {
	f := setup(t, &sim.Sim{}, nil)
	f.col.cfg.PollInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.col.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.col.Status().LastPoll.IsZero() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
	if f.col.Status().LastPoll.IsZero() {
		t.Error("never polled")
	}
}

// flaky fails every nth request with a timeout-like error.
type flaky struct {
	Device
	mu    sync.Mutex
	n     int
	every int
}

func (f *flaky) Get(ctx context.Context, p string) ([]byte, error) {
	f.mu.Lock()
	f.n++
	fail := f.n%f.every == 0
	f.mu.Unlock()
	if fail {
		return nil, errors.New("youless " + p + ": context deadline exceeded")
	}
	return f.Device.Get(ctx, p)
}

// A device that sometimes does not answer still gets backfilled in one go.
func TestBackfillSurvivesLostRequests(t *testing.T) {
	retryWait = time.Millisecond
	f := setup(t, &sim.Sim{}, []youless.Channel{youless.Electricity})
	f.col.dev = &flaky{Device: f.col.dev, every: 7}
	ctx := context.Background()
	if err := f.col.SyncLog(ctx, youless.Electricity, youless.Hour); err != nil {
		t.Fatalf("backfill with lost requests: %v", err)
	}
	if got := logCount(f, youless.Electricity, youless.Hour); got != 69*24+1 {
		t.Errorf("hour values: %d", got)
	}
	// A device that never answers fails after a few attempts.
	f.col.dev = &flaky{Device: f.col.dev, every: 1}
	f.clk.Add(2 * time.Hour)
	if err := f.col.SyncLog(ctx, youless.Electricity, youless.Hour); err == nil {
		t.Error("want error from a device that never answers")
	}
}
