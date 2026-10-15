// Package collector reads the YouLess and stores what it reads: the live meter
// readings every poll interval and the device's internal logs (minute, 10-minute,
// hour, day), which also fill any gap while youless-tig or InfluxDB was down.
// It is the only part that talks to the device; the compatible API reads the
// caches and the store through it.
package collector

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/wimmme/youless-tig/internal/influx"
	"github.com/wimmme/youless-tig/internal/store"
	"github.com/wimmme/youless-tig/internal/youless"
)

// Device is the part of youless.Client the collector uses.
type Device interface {
	Get(ctx context.Context, pathQuery string) ([]byte, error)
}

// Config of a collector.
type Config struct {
	Loc          *time.Location
	PollInterval time.Duration
	History      bool              // read the internal logs
	Channels     []youless.Channel // nil: electricity, plus gas and water when the meter has them
	Now          func() time.Time  // nil: time.Now (tests set a clock)
}

// syncEvery is how often each log is read when nobody asks for it: a minute
// page holds 30 minutes, a 10-minute page 8 hours, an hour page a day.
var syncEvery = map[youless.Resolution]time.Duration{
	youless.Minute:     10 * time.Minute,
	youless.TenMinutes: time.Hour,
	youless.Hour:       time.Hour,
	youless.Day:        6 * time.Hour,
}

// refreshAfter limits how often an API request may make the collector read a
// log page from the device.
var refreshAfter = map[youless.Resolution]time.Duration{
	youless.Minute:     30 * time.Second,
	youless.TenMinutes: 2 * time.Minute,
	youless.Hour:       5 * time.Minute,
	youless.Day:        15 * time.Minute,
}

type logKey struct {
	ch youless.Channel
	r  youless.Resolution
}

type logState struct {
	mu          sync.Mutex
	known       bool      // last was looked up in the store
	last        time.Time // newest stored value
	synced      time.Time // last successful read from the device
	unsupported bool      // the device keeps no data for this log (gas per minute)
}

type cached struct {
	body []byte
	at   time.Time
}

// Collector polls one device.
type Collector struct {
	cfg   Config
	dev   Device
	st    *store.Store
	log   *slog.Logger
	now   func() time.Time
	ready chan struct{} // closed once the channels are known

	mu        sync.Mutex
	cache     map[string]cached
	channels  []youless.Channel
	lastE     int64
	lastGas   time.Time
	lastWater time.Time
	lastS0    int64
	lastPoll  time.Time
	lastErr   string
	logs      map[logKey]*logState
	fetchMu   sync.Mutex
}

// New returns a collector; call Run to start it.
func New(cfg Config, dev Device, st *store.Store, log *slog.Logger) *Collector {
	if cfg.Loc == nil {
		cfg.Loc = time.Local
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Collector{
		cfg: cfg, dev: dev, st: st, log: log, now: cfg.Now,
		ready: make(chan struct{}),
		cache: map[string]cached{},
		logs:  map[logKey]*logState{},
	}
}

// Run polls until ctx ends.
func (c *Collector) Run(ctx context.Context) {
	var wg sync.WaitGroup
	if c.cfg.History {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.runHistory(ctx)
		}()
	}
	t := time.NewTicker(c.cfg.PollInterval)
	defer t.Stop()
	for {
		if err := c.Poll(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn("poll failed", "err", err)
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-t.C:
		}
	}
}

// Poll reads /e (and /f with a P1 meter) once and stores new readings.
func (c *Collector) Poll(ctx context.Context) error {
	err := c.poll(ctx)
	c.mu.Lock()
	if err != nil {
		c.lastErr = err.Error()
	} else {
		c.lastErr = ""
		c.lastPoll = c.now()
	}
	c.mu.Unlock()
	return err
}

func (c *Collector) poll(ctx context.Context) error {
	eb, err := c.dev.Get(ctx, "/e")
	if err != nil {
		return err
	}
	e, err := youless.ParseEnergy(eb)
	if err != nil {
		return err
	}
	c.put("/e", eb)
	var f *youless.Phases
	if e.Import1 != nil {
		fb, err := c.dev.Get(ctx, "/f")
		if err != nil {
			return err
		}
		if f, err = youless.ParsePhases(fb); err != nil {
			return err
		}
		c.put("/f", fb)
	}
	c.decideChannels(e)

	c.mu.Lock()
	lastE, lastGas, lastWater, lastS0 := c.lastE, c.lastGas, c.lastWater, c.lastS0
	c.mu.Unlock()
	var pts []influx.Point
	if e.Time != lastE {
		pts = append(pts, c.st.ElectricityPoint(e, f, c.cfg.Loc))
	}
	gas, hasGas := c.st.MeterPoint(store.Gas, e.Gas, e.GasTime, c.cfg.Loc)
	hasGas = hasGas && c.enabled(youless.Gas) && !gas.Time.Equal(lastGas)
	if hasGas {
		pts = append(pts, gas)
	}
	water, hasWater := c.st.MeterPoint(store.Water, e.Water, e.WaterTm, c.cfg.Loc)
	hasWater = hasWater && c.enabled(youless.Water) && !water.Time.Equal(lastWater)
	if hasWater {
		pts = append(pts, water)
	}
	s0, hasS0 := c.st.S0Point(e)
	hasS0 = hasS0 && c.enabled(youless.S0) && *e.S0Time != lastS0
	if hasS0 {
		pts = append(pts, s0)
	}
	if err := c.st.Write(ctx, pts); err != nil {
		return err
	}
	c.mu.Lock()
	c.lastE = e.Time
	if hasGas {
		c.lastGas = gas.Time
	}
	if hasWater {
		c.lastWater = water.Time
	}
	if hasS0 {
		c.lastS0 = *e.S0Time
	}
	c.mu.Unlock()
	return nil
}

// decideChannels fixes the channels after the first good /e.
func (c *Collector) decideChannels(e *youless.Energy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channels != nil {
		return
	}
	if c.cfg.Channels != nil {
		c.channels = c.cfg.Channels
	} else {
		c.channels = []youless.Channel{youless.Electricity}
		if e.Gas != nil && *e.Gas > 0 {
			c.channels = append(c.channels, youless.Gas)
		}
		if e.Water != nil && *e.Water > 0 {
			c.channels = append(c.channels, youless.Water)
		}
	}
	c.log.Info("channels", "channels", c.channels)
	close(c.ready)
}

func (c *Collector) enabled(ch youless.Channel) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.channels {
		if x == ch {
			return true
		}
	}
	return false
}

// Channels are the channels being collected (nil before the first poll).
func (c *Collector) Channels() []youless.Channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]youless.Channel(nil), c.channels...)
}

// ── history ──────────────────────────────────────────────────────────────────

func (c *Collector) runHistory(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-c.ready:
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		for _, ch := range c.Channels() {
			for _, r := range youless.Resolutions {
				ls := c.state(ch, r)
				ls.mu.Lock()
				due := !ls.unsupported && c.now().Sub(ls.synced) >= syncEvery[r]
				ls.mu.Unlock()
				if !due {
					continue
				}
				if err := c.SyncLog(ctx, ch, r); err != nil && ctx.Err() == nil {
					c.log.Warn("log sync failed", "channel", ch, "resolution", r, "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Collector) state(ch youless.Channel, r youless.Resolution) *logState {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := logKey{ch, r}
	if c.logs[k] == nil {
		c.logs[k] = &logState{}
	}
	return c.logs[k]
}

// SyncLog reads the pages of one log that hold values newer than what is
// stored, newest first, and stores them. The first time (nothing stored) it
// reads everything the device keeps.
func (c *Collector) SyncLog(ctx context.Context, ch youless.Channel, r youless.Resolution) error {
	ls := c.state(ch, r)
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return c.syncLocked(ctx, ch, r, ls)
}

func (c *Collector) syncLocked(ctx context.Context, ch youless.Channel, r youless.Resolution, ls *logState) error {
	if !ls.known {
		t, err := c.st.LastLogTime(ctx, ch, r)
		switch {
		case errors.Is(err, influx.ErrNoData):
		case err != nil:
			return err
		default:
			ls.last = t
		}
		ls.known = true
	}
	now := c.now()
	var pts []influx.Point
	newest := ls.last
	pages := 0
	for i := 1; i <= r.DevicePages(); i++ {
		page := i
		if r == youless.Day { // pages are month numbers, newest first
			page = int(time.Date(now.Year(), now.Month()-time.Month(i-1), 1, 0, 0, 0, 0, c.cfg.Loc).Month())
		}
		body, err := c.dev.Get(ctx, ch.Path()+"?"+r.Param()+"="+strconv.Itoa(page)+"&f=j")
		if errors.Is(err, youless.ErrNotFound) {
			break
		}
		if err != nil {
			return err
		}
		p, err := youless.ParseLogPage(body, c.cfg.Loc)
		if err != nil {
			return err
		}
		pages++
		if i == 1 && ls.last.IsZero() && allNil(p.Values) {
			ls.unsupported = true
			c.log.Info("log has no data, skipped", "channel", ch, "resolution", r)
			return nil
		}
		got := c.st.LogPoints(ch, r, p, ls.last)
		pts = append(pts, got...)
		for _, pt := range got {
			if pt.Time.After(newest) {
				newest = pt.Time
			}
		}
		if !p.Start.After(ls.last) {
			break // this page reaches what is stored
		}
	}
	if err := c.st.Write(ctx, pts); err != nil {
		return err
	}
	if ls.last.IsZero() && len(pts) > 0 {
		c.log.Info("log backfilled", "channel", ch, "resolution", r, "pages", pages, "values", len(pts))
	}
	ls.last = newest
	ls.synced = now
	return nil
}

// Refresh reads a log from the device first if it was not read recently; the
// compatible API calls it before answering for recent pages.
func (c *Collector) Refresh(ctx context.Context, ch youless.Channel, r youless.Resolution) error {
	if !c.enabled(ch) || !c.cfg.History {
		return nil
	}
	ls := c.state(ch, r)
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.unsupported || c.now().Sub(ls.synced) < refreshAfter[r] {
		return nil
	}
	return c.syncLocked(ctx, ch, r, ls)
}

func allNil(vs []*float64) bool {
	for _, v := range vs {
		if v != nil {
			return false
		}
	}
	return true
}

// ── caches for the compatible API ────────────────────────────────────────────

func (c *Collector) put(path string, body []byte) {
	c.mu.Lock()
	c.cache[path] = cached{body: body, at: c.now()}
	c.mu.Unlock()
}

// Cached is the last body read from path and when, or nil.
func (c *Collector) Cached(path string) ([]byte, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.cache[path]
	return e.body, e.at
}

// Fetch returns the cached body of path when it is younger than maxAge, and
// reads the device otherwise.
func (c *Collector) Fetch(ctx context.Context, path string, maxAge time.Duration) ([]byte, error) {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()
	if b, at := c.Cached(path); b != nil && c.now().Sub(at) < maxAge {
		return b, nil
	}
	b, err := c.dev.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	c.put(path, b)
	return b, nil
}

// Status for the health check.
type Status struct {
	LastPoll  time.Time
	LastError string
	Channels  []youless.Channel
}

// Status reports the last poll.
func (c *Collector) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{LastPoll: c.lastPoll, LastError: c.lastErr, Channels: append([]youless.Channel(nil), c.channels...)}
}
