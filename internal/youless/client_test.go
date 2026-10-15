package youless_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wimmme/youless-tig/internal/sim"
	"github.com/wimmme/youless-tig/internal/youless"
)

func newSim(t *testing.T, s *sim.Sim) *youless.Client {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	c, err := youless.NewClient(srv.URL+"/", s.Password, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Gap = 0
	return c
}

func TestClientReads(t *testing.T) {
	c := newSim(t, &sim.Sim{Loc: time.UTC})
	ctx := context.Background()
	d, _, err := c.Device(ctx)
	if err != nil || d.Model != "LS120" {
		t.Fatalf("device: %+v %v", d, err)
	}
	e, raw, err := c.Energy(ctx)
	if err != nil || e.Import1 == nil || len(raw) == 0 {
		t.Fatalf("energy: %+v %v", e, err)
	}
	if _, _, err := c.Phases(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := c.Log(ctx, youless.Electricity, youless.Minute, 1, time.UTC)
	if err != nil || len(p.Values) != 30 {
		t.Fatalf("log: %v %v", p, err)
	}
	if _, err := c.Log(ctx, youless.Electricity, youless.Minute, 21, time.UTC); !errors.Is(err, youless.ErrNotFound) {
		t.Errorf("page 21: %v, want ErrNotFound", err)
	}
}

func TestClientLogin(t *testing.T) {
	s := &sim.Sim{Loc: time.UTC, Password: "s3cret-pw"}
	c := newSim(t, s)
	if _, _, err := c.Energy(context.Background()); err != nil {
		t.Fatalf("with login: %v", err)
	}
	before := s.Requests()
	if _, _, err := c.Energy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Requests() - before; got != 1 {
		t.Errorf("logged in: %d requests for one read, want 1 (cookie reused)", got)
	}
}

func TestClientWrongPassword(t *testing.T) {
	s := &sim.Sim{Loc: time.UTC, Password: "right"}
	srv := httptest.NewServer(s)
	defer srv.Close()
	for _, pw := range []string{"", "wrong-pw-xyz"} {
		c, _ := youless.NewClient(srv.URL, pw, 5*time.Second)
		_, _, err := c.Energy(context.Background())
		if !errors.Is(err, youless.ErrUnauthorized) {
			t.Errorf("password %q: %v, want ErrUnauthorized", pw, err)
		}
	}
}

// A network error during login must not reveal the password.
func TestClientLoginErrorHidesPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/L" {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c, _ := youless.NewClient(srv.URL, "TopSecret42", 5*time.Second)
	_, _, err := c.Energy(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "TopSecret42") {
		t.Errorf("error reveals the password: %v", err)
	}
}

// Requests never overlap and keep the gap.
func TestClientSerialises(t *testing.T) {
	var active, overlaps atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if active.Add(1) > 1 {
			overlaps.Add(1)
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		w.Write([]byte(`{"model":"LS120","fw":"x","mac":"y"}`))
	}))
	defer srv.Close()
	c, _ := youless.NewClient(srv.URL, "", 5*time.Second)
	c.Gap = 20 * time.Millisecond
	start := time.Now()
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), "/d"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if overlaps.Load() > 0 {
		t.Errorf("%d overlapping requests", overlaps.Load())
	}
	if d := time.Since(start); d < 4*20*time.Millisecond {
		t.Errorf("5 requests in %v: gap not kept", d)
	}
}

func TestClientStatusErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := youless.NewClient(srv.URL, "", 5*time.Second)
	if _, err := c.Get(context.Background(), "/e"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("got %v", err)
	}
	for _, bad := range []string{"", "192.168.0.61", "ftp://x", "http://"} {
		if _, err := youless.NewClient(bad, "", time.Second); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
