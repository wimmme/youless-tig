// Package api serves the YouLess-compatible read API: the same paths, query
// parameters and response formats as the device, so existing YouLess clients
// can point at youless-tig instead. Live data (/d, /e, /f, /a) comes from the
// collector's caches; log pages (/V, /W, /K, /Z) come from InfluxDB, which
// keeps them far longer than the device.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/wimmme/youless-tig/internal/collector"
	"github.com/wimmme/youless-tig/internal/store"
	"github.com/wimmme/youless-tig/internal/youless"
)

// maxPage keeps page arithmetic sane: a million minute pages is 57 years.
const maxPage = 1_000_000

// Server answers the API.
type Server struct {
	C            *collector.Collector
	St           *store.Store
	Loc          *time.Location
	PollInterval time.Duration
	Log          *slog.Logger
	Now          func() time.Time
	started      time.Time
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// stale is how old a live reading may be before /e answers 503.
func (s *Server) stale() time.Duration {
	return max(3*s.PollInterval, time.Minute)
}

// Handler is the HTTP handler; only GET and HEAD are allowed.
func (s *Server) Handler() http.Handler {
	s.started = s.now()
	mux := http.NewServeMux()
	mux.HandleFunc("/d", s.device)
	mux.HandleFunc("/e", s.live("/e"))
	mux.HandleFunc("/f", s.live("/f"))
	mux.HandleFunc("/a", s.status)
	for _, ch := range youless.Channels {
		mux.HandleFunc(ch.Path(), s.logPage(ch))
	}
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { notFound(w) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("<h1>404 Niet Gevonden</h1>"))
}

func unavailable(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusServiceUnavailable)
}

func writeJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	b, err := s.C.Fetch(r.Context(), "/d", time.Hour)
	if err != nil {
		s.Log.Warn("api /d", "err", err)
		unavailable(w, "device not reachable")
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, b)
}

// live serves the last /e or /f the collector read. /f without a P1 meter is
// not polled and is read on demand, at most once per poll interval.
func (s *Server) live(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, at := s.C.Cached(path)
		if b == nil && path == "/f" {
			var err error
			if b, err = s.C.Fetch(r.Context(), path, s.PollInterval); err != nil {
				unavailable(w, "device not reachable")
				return
			}
			at = s.now()
		}
		if b == nil || s.now().Sub(at) > s.stale() {
			unavailable(w, "no recent reading from the device")
			return
		}
		writeJSON(w, b)
	}
}

// status serves /a (text) and /a?f=j (JSON), read from the device at most once
// per poll interval.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	key := "/a"
	if r.URL.Query().Get("f") == "j" {
		key = "/a?f=j"
	}
	b, err := s.C.Fetch(r.Context(), key, s.PollInterval)
	if err != nil {
		s.Log.Warn("api /a", "err", err)
		unavailable(w, "device not reachable")
		return
	}
	if key == "/a" {
		w.Header().Set("Content-Type", "text/plain")
		w.Write(b)
		return
	}
	writeJSON(w, b)
}

// logPage serves a log page in the device's format. Within the device's own
// range (and for the current year of ?m=) the page has the device's length,
// with "*" for values that are not stored; beyond it a page without any stored
// value is a 404, like the device past its last page. ?y=YYYY selects the year
// of a day-log month (an extension; the device has no such parameter).
func (s *Server) logPage(ch youless.Channel) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res, pageStr := youless.Resolution(""), ""
		for _, x := range youless.Resolutions {
			if v := q.Get(x.Param()); v != "" {
				res, pageStr = x, v
				break
			}
		}
		page, err := strconv.Atoi(pageStr)
		if res == "" || err != nil || page < 1 || page > maxPage {
			notFound(w)
			return
		}
		now := s.now()
		var sp youless.Span
		beyond := page > res.DevicePages()
		if res == youless.Day {
			year := 0
			if y := q.Get("y"); y != "" {
				if year, err = strconv.Atoi(y); err != nil || year < 2000 || year > 9999 {
					notFound(w)
					return
				}
			}
			if page > 12 {
				notFound(w)
				return
			}
			sp = youless.MonthSpan(page, year, now, s.Loc)
			if year != 0 {
				if sp.Start.After(now) {
					notFound(w)
					return
				}
				beyond = sp.Start.Before(youless.MonthSpan(page, 0, now, s.Loc).Start)
			}
		} else {
			sp = youless.PageSpan(res, page, now, s.Loc)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if !beyond {
			if err := s.C.Refresh(ctx, ch, res); err != nil {
				s.Log.Warn("api: log refresh failed", "channel", ch, "resolution", res, "err", err)
			}
		}
		vals, err := s.St.LogValues(ctx, ch, res, sp.Start, sp.End())
		if err != nil {
			s.Log.Warn("api: log query failed", "channel", ch, "resolution", res, "err", err)
			unavailable(w, "storage not reachable")
			return
		}
		if beyond && len(vals) == 0 {
			notFound(w)
			return
		}
		p := &youless.LogPage{Unit: youless.Unit(ch, res), Start: sp.Start, Interval: sp.Interval}
		for i := 0; i < sp.Count; i++ {
			if v, ok := vals[sp.Time(i).Unix()]; ok {
				p.Values = append(p.Values, &v)
			} else {
				p.Values = append(p.Values, nil)
			}
		}
		writeJSON(w, youless.FormatLogPage(p))
	}
}

type health struct {
	Status    string            `json:"status"`
	LastPoll  *time.Time        `json:"last_poll,omitempty"`
	LastError string            `json:"last_error,omitempty"`
	Channels  []youless.Channel `json:"channels"`
}

// health is 200 while polls succeed (or during the first minutes after start)
// and 503 when the device has not been read for a while.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	st := s.C.Status()
	h := health{Status: "ok", LastError: st.LastError, Channels: st.Channels}
	if h.Channels == nil {
		h.Channels = []youless.Channel{}
	}
	if !st.LastPoll.IsZero() {
		t := st.LastPoll.UTC()
		h.LastPoll = &t
	}
	code := http.StatusOK
	now := s.now()
	if (st.LastPoll.IsZero() && now.Sub(s.started) > 2*time.Minute) ||
		(!st.LastPoll.IsZero() && now.Sub(st.LastPoll) > max(5*time.Minute, s.stale())) {
		h.Status, code = "device not read recently", http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(h)
}
