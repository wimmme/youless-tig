// Package influxtest is an in-memory stand-in for InfluxDB in tests: it accepts
// line protocol on /api/v2/write and answers the InfluxQL statements of
// internal/store on /query. It parses only what youless-tig writes.
package influxtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Point is a stored point; field values are float64, int64, bool or string.
type Point struct {
	Measurement string
	Tags        map[string]string
	Fields      map[string]any
	Time        time.Time
}

// Server is a fake InfluxDB.
type Server struct {
	*httptest.Server
	Token string // required token ("Authorization: Token …"); empty: none

	mu     sync.Mutex
	points map[string]Point // series key + time → point (a rewrite replaces, as in InfluxDB)
	Writes int              // number of write requests
	Fail   bool             // answer every request with 500
}

// New starts a fake InfluxDB; it closes with the test.
func New(t interface{ Cleanup(func()) }) *Server {
	s := &Server{points: map[string]Point{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	fail := s.Fail
	s.mu.Unlock()
	if fail {
		http.Error(w, `{"message":"fail"}`, http.StatusInternalServerError)
		return
	}
	if r.URL.Path == "/ping" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.Token != "" && r.Header.Get("Authorization") != "Token "+s.Token {
		http.Error(w, `{"code":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/api/v2/write":
		s.write(w, r)
	case "/query":
		s.query(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) write(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("precision") != "s" || r.URL.Query().Get("bucket") == "" {
		http.Error(w, "want precision=s and a bucket", http.StatusBadRequest)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var pts []Point
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		p, err := parseLine(line)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		pts = append(pts, p)
	}
	s.mu.Lock()
	for _, p := range pts {
		s.points[seriesKey(p)+"@"+strconv.FormatInt(p.Time.Unix(), 10)] = p
	}
	s.Writes++
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func seriesKey(p Point) string {
	keys := make([]string, 0, len(p.Tags))
	for k := range p.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(p.Measurement)
	for _, k := range keys {
		b.WriteString("," + k + "=" + p.Tags[k])
	}
	return b.String()
}

// parseLine handles line protocol without escaped characters.
func parseLine(line string) (Point, error) {
	parts := strings.Split(line, " ")
	if len(parts) != 3 {
		return Point{}, fmt.Errorf("line %q: want 3 parts", line)
	}
	head := strings.Split(parts[0], ",")
	p := Point{Measurement: head[0], Tags: map[string]string{}, Fields: map[string]any{}}
	for _, kv := range head[1:] {
		k, v, _ := strings.Cut(kv, "=")
		p.Tags[k] = v
	}
	for _, kv := range strings.Split(parts[1], ",") {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasSuffix(v, "i"):
			n, err := strconv.ParseInt(strings.TrimSuffix(v, "i"), 10, 64)
			if err != nil {
				return Point{}, err
			}
			p.Fields[k] = n
		case v == "true" || v == "false":
			p.Fields[k] = v == "true"
		case strings.HasPrefix(v, `"`):
			p.Fields[k] = strings.Trim(v, `"`)
		default:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return Point{}, err
			}
			p.Fields[k] = f
		}
	}
	sec, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return Point{}, err
	}
	p.Time = time.Unix(sec, 0)
	return p, nil
}

var (
	reLast  = regexp.MustCompile(`^SELECT last\("value"\) FROM "(\w+)" WHERE "device"='(.*?)' AND "channel"='(.*?)' AND "resolution"='(.*?)'$`)
	reRange = regexp.MustCompile(`^SELECT "value" FROM "(\w+)" WHERE "device"='(.*?)' AND "channel"='(.*?)' AND "resolution"='(.*?)' AND time >= '(.*?)' AND time < '(.*?)'$`)
)

type series struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Values  [][]any  `json:"values"`
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	q := r.Form.Get("q")
	if r.Form.Get("epoch") != "s" || r.Form.Get("db") == "" {
		http.Error(w, "want epoch=s and db", http.StatusBadRequest)
		return
	}
	var from, to time.Time
	var m []string
	if m = reLast.FindStringSubmatch(q); m == nil {
		if m = reRange.FindStringSubmatch(q); m == nil {
			json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"error": "unsupported query: " + q}}})
			return
		}
		from, _ = time.Parse(time.RFC3339, m[5])
		to, _ = time.Parse(time.RFC3339, m[6])
	}
	isRange := len(m) == 7
	sel := s.Select(m[1], map[string]string{"device": m[2], "channel": m[3], "resolution": m[4]})
	var rows [][]any
	for _, p := range sel {
		if isRange && (p.Time.Before(from) || !p.Time.Before(to)) {
			continue
		}
		rows = append(rows, []any{p.Time.Unix(), p.Fields["value"]})
	}
	if !isRange && len(rows) > 0 { // last()
		rows = rows[len(rows)-1:]
	}
	res := map[string]any{"statement_id": 0}
	if len(rows) > 0 {
		cols := []string{"time", "value"}
		if !isRange {
			cols[1] = "last"
		}
		res["series"] = []series{{Name: m[1], Columns: cols, Values: rows}}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": []any{res}})
}

// Select returns the points of a measurement whose tags include all of tags,
// oldest first.
func (s *Server) Select(measurement string, tags map[string]string) []Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Point
next:
	for _, p := range s.points {
		if p.Measurement != measurement {
			continue
		}
		for k, v := range tags {
			if p.Tags[k] != v {
				continue next
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// Add stores points directly, as if written earlier.
func (s *Server) Add(pts ...Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range pts {
		s.points[seriesKey(p)+"@"+strconv.FormatInt(p.Time.Unix(), 10)] = p
	}
}

// SetFail makes every request fail (true) or work again (false).
func (s *Server) SetFail(fail bool) {
	s.mu.Lock()
	s.Fail = fail
	s.mu.Unlock()
}

// WriteCount is the number of write requests so far.
func (s *Server) WriteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Writes
}
