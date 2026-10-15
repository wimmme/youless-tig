package influx

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLine(t *testing.T) {
	ts := time.Unix(1792021455, 0)
	got, err := Point{
		Measurement: "my meas,x",
		Tags:        map[string]string{"device": "huis 1", "b": "x=y,z", "empty": ""},
		Fields: map[string]any{
			"f": 1.5, "i": int64(-3), "n": 7, "b": true, "s": `say "hi" \o/`, "z": 0.0,
		},
		Time: ts,
	}.Line()
	if err != nil {
		t.Fatal(err)
	}
	want := `my\ meas\,x,b=x\=y\,z,device=huis\ 1 b=true,f=1.5,i=-3i,n=7i,s="say \"hi\" \\o/",z=0 1792021455`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	for _, p := range []Point{
		{Measurement: "m", Fields: map[string]any{}},
		{Measurement: "m", Fields: map[string]any{"x": math.NaN()}},
		{Measurement: "m", Fields: map[string]any{"x": math.Inf(1)}},
		{Measurement: "m", Fields: map[string]any{"x": []int{1}}},
		{Fields: map[string]any{"x": 1.0}},
	} {
		if _, err := p.Line(); err == nil {
			t.Errorf("%+v: want error", p)
		}
	}
}

func TestWrite(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotPath, gotAuth = r.URL.String(), r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL + "/", Token: "tok", Org: "home org", Bucket: "youless"}
	ctx := context.Background()
	if err := c.Write(ctx, nil); err != nil || requests != 0 {
		t.Fatalf("empty write: %v, %d requests", err, requests)
	}
	pts := []Point{{Measurement: "m", Fields: map[string]any{"v": 1.0}, Time: time.Unix(10, 0)}}
	if err := c.Write(ctx, pts); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v2/write?bucket=youless&org=home+org&precision=s" || gotAuth != "Token tok" || gotBody != "m v=1 10\n" {
		t.Errorf("path %q auth %q body %q", gotPath, gotAuth, gotBody)
	}
	// Writes through Telegraf use the write URL and token.
	write := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer write.Close()
	c.WriteURL, c.WriteToken = write.URL, "relay"
	requests = 0
	if err := c.Write(ctx, pts); err != nil || requests != 0 || gotAuth != "Token relay" {
		t.Errorf("write URL: %v, %d requests to InfluxDB, auth %q", err, requests, gotAuth)
	}
}

func TestWriteBatches(t *testing.T) {
	var lines []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lines = append(lines, strings.Count(string(b), "\n"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	pts := make([]Point, batchSize+10)
	for i := range pts {
		pts[i] = Point{Measurement: "m", Fields: map[string]any{"v": float64(i)}, Time: time.Unix(int64(i), 0)}
	}
	if err := (&Client{URL: srv.URL}).Write(context.Background(), pts); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != batchSize || lines[1] != 10 {
		t.Errorf("batches %v", lines)
	}
}

func TestWriteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"unauthorized","message":"unauthorized access"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	err := (&Client{URL: srv.URL}).Write(context.Background(), []Point{{Measurement: "m", Fields: map[string]any{"v": 1.0}}})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "unauthorized access") {
		t.Errorf("got %v", err)
	}
}

func TestQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.URL.Path != "/query" || r.Form.Get("db") != "youless" || r.Form.Get("epoch") != "s" || r.Header.Get("Authorization") != "Token tok" {
			http.Error(w, "bad request "+r.URL.String(), 400)
			return
		}
		switch r.Form.Get("q") {
		case "ok":
			w.Write([]byte(`{"results":[{"statement_id":0,"series":[{"name":"youless_log","columns":["time","value"],"values":[[1792021440,2.5],[1792021500,null]]}]}]}`))
		case "empty":
			w.Write([]byte(`{"results":[{"statement_id":0}]}`))
		case "bad":
			w.Write([]byte(`{"results":[{"statement_id":0,"error":"error parsing query"}]}`))
		}
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, Token: "tok", Bucket: "youless"}
	ctx := context.Background()
	s, err := c.Query(ctx, "ok")
	if err != nil || len(s) != 1 || len(s[0].Values) != 2 {
		t.Fatalf("got %+v %v", s, err)
	}
	if n, ok := s[0].Values[0][0].(json.Number); !ok || n.String() != "1792021440" {
		t.Errorf("time %v", s[0].Values[0][0])
	}
	if s, err := c.Query(ctx, "empty"); err != nil || len(s) != 0 {
		t.Errorf("empty: %v %v", s, err)
	}
	if _, err := c.Query(ctx, "bad"); err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Errorf("bad: %v", err)
	}
	if err := c.Ping(ctx); err == nil {
		t.Error("ping on a server without /ping: want error")
	}
}

func TestQuote(t *testing.T) {
	if got := QuoteString(`it's a \ test`); got != `'it\'s a \\ test'` {
		t.Errorf("QuoteString: %s", got)
	}
	if got := QuoteIdent(`a"b`); got != `"a\"b"` {
		t.Errorf("QuoteIdent: %s", got)
	}
	if got := Time(time.Date(2026, 10, 15, 3, 0, 0, 0, time.FixedZone("x", 7200))); got != `'2026-10-15T01:00:00Z'` {
		t.Errorf("Time: %s", got)
	}
}
