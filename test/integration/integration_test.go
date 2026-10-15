//go:build integration

// Package integration tests the complete stack (youless-tig, Telegraf, InfluxDB,
// Grafana) with the simulated YouLess. Run with tools/integration.sh; the
// addresses come from test/integration/stack.override.yml.
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var client = &http.Client{Timeout: 30 * time.Second}

func env(t *testing.T, k string) string {
	t.Helper()
	v := os.Getenv(k)
	if v == "" {
		t.Fatalf("%s not set: run through tools/integration.sh", k)
	}
	return v
}

func secret(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env(t, "IT_SECRETS"), name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func get(t *testing.T, u string) (int, string) {
	t.Helper()
	resp, err := client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// influxQL runs a query with a token and returns the first value of the first
// row, or nil.
func influxQL(t *testing.T, token, q string) (any, error) {
	t.Helper()
	form := url.Values{"db": {env(t, "IT_BUCKET")}, "q": {q}, "epoch": {"s"}}
	req, _ := http.NewRequest(http.MethodPost, env(t, "IT_INFLUX")+"/query", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Token "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
	}
	var r struct {
		Results []struct {
			Error  string
			Series []struct{ Values [][]any }
		}
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if len(r.Results) == 0 {
		return nil, nil
	}
	if r.Results[0].Error != "" {
		return nil, fmt.Errorf("%s", r.Results[0].Error)
	}
	if len(r.Results[0].Series) == 0 || len(r.Results[0].Series[0].Values) == 0 {
		return nil, nil
	}
	return r.Results[0].Series[0].Values[0][1], nil
}

func count(t *testing.T, where string) float64 {
	t.Helper()
	v, err := influxQL(t, secret(t, "youless_tig_token"), `SELECT count("value") FROM "youless_log" WHERE `+where)
	if err != nil {
		t.Fatal(err)
	}
	if v == nil {
		return 0
	}
	return v.(float64)
}

// waitBackfill waits until the first start has read the device's history.
func waitBackfill(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		day := count(t, `"channel"='electricity' AND "resolution"='day'`)
		hour := count(t, `"channel"='water' AND "resolution"='hour'`)
		if day >= 300 && hour >= 69*24 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("backfill not done after 5 minutes: %v day values, %v water hour values", day, hour)
		}
		time.Sleep(3 * time.Second)
	}
}

// The compatible API answers every log page the device has exactly like the
// device. Near a minute change the device moves on before youless-tig reads it
// again (at most every 30 s for the minute log), so a page may be retried.
func TestCompatibleAPI(t *testing.T) {
	waitBackfill(t)
	api, sim := env(t, "IT_API"), env(t, "IT_SIM")
	pages := 0
	for _, ch := range []string{"V", "W", "K"} {
		for _, r := range []struct {
			param string
			n     int
		}{{"h", 20}, {"w", 30}, {"d", 70}, {"m", 12}} {
			for page := 1; page <= r.n; page++ {
				path := fmt.Sprintf("/%s?%s=%d&f=j", ch, r.param, page)
				deadline := time.Now().Add(45 * time.Second)
				for {
					_, want := get(t, sim+path)
					code, got := get(t, api+path)
					if code == 200 && got == want {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("%s: HTTP %d\n got %s\nwant %s", path, code, got, want)
					}
					time.Sleep(2 * time.Second)
				}
				pages++
			}
		}
	}
	t.Logf("%d log pages identical to the device", pages)

	for _, p := range []string{"/d", "/a?f=j"} {
		_, want := get(t, sim+p)
		if code, got := get(t, api+p); code != 200 || got != want {
			t.Errorf("%s: %d %s, want %s", p, code, got, want)
		}
	}
	if code, body := get(t, api+"/e"); code != 200 || !strings.HasPrefix(body, `[{"tm":`) {
		t.Errorf("/e: %d %s", code, body)
	}
	if code, _ := get(t, api+"/V?d=71"); code != 404 {
		t.Errorf("/V?d=71 without archived data: %d", code)
	}
}

// Live readings reach InfluxDB through Telegraf.
func TestLiveDataThroughTelegraf(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	for {
		v, err := influxQL(t, secret(t, "youless_tig_token"),
			`SELECT count("power_w") FROM "youless_electricity" WHERE time > now() - 1m`)
		if err != nil {
			t.Fatal(err)
		}
		if v != nil && v.(float64) >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live points in the last minute: %v", v)
		}
		time.Sleep(3 * time.Second)
	}
	for _, m := range []string{"youless_gas", "youless_water"} {
		v, err := influxQL(t, secret(t, "youless_tig_token"), `SELECT last("total_m3") FROM "`+m+`"`)
		if err != nil || v == nil {
			t.Errorf("%s: %v %v", m, v, err)
		}
	}
}

// Each service's token can do only what it needs.
func TestTokensAreLeastPrivilege(t *testing.T) {
	write := func(token string) int {
		q := url.Values{"org": {env(t, "IT_ORG")}, "bucket": {env(t, "IT_BUCKET")}, "precision": {"s"}}
		req, _ := http.NewRequest(http.MethodPost, env(t, "IT_INFLUX")+"/api/v2/write?"+q.Encode(),
			strings.NewReader("token_test value=1 1700000000\n"))
		req.Header.Set("Authorization", "Token "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, f := range []string{"youless_tig_token", "grafana_token"} {
		if code := write(secret(t, f)); code/100 == 2 {
			t.Errorf("%s can write (HTTP %d)", f, code)
		}
	}
	if code := write(secret(t, "telegraf_listener_token")); code/100 == 2 {
		t.Errorf("the listener token works on InfluxDB (HTTP %d)", code)
	}
	if code := write(secret(t, "telegraf_token")); code/100 != 2 {
		t.Errorf("telegraf_token cannot write (HTTP %d)", code)
	}
	if _, err := influxQL(t, secret(t, "telegraf_token"), `SELECT count("value") FROM "youless_log"`); err == nil {
		t.Error("telegraf_token can read")
	}
	// Telegraf's listener refuses writes without its token.
	req, _ := http.NewRequest(http.MethodPost, "http://telegraf:8186/api/v2/write?bucket=x&precision=s",
		strings.NewReader("token_test value=1 1700000000\n"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		t.Errorf("listener accepts writes without a token: HTTP %d", resp.StatusCode)
	}
}

func grafana(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, env(t, "IT_GRAFANA")+path, r)
	req.SetBasicAuth(env(t, "IT_GRAFANA_USER"), env(t, "IT_GRAFANA_PASSWORD"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// Grafana's data source works and every query of the dashboard runs without
// error; the main panels have data.
func TestGrafanaDashboard(t *testing.T) {
	waitBackfill(t)
	code, b := grafana(t, "GET", "/api/datasources/uid/youless-influxdb/health", nil)
	if code != 200 || !strings.Contains(string(b), `"OK"`) {
		t.Fatalf("data source health: %d %s", code, b)
	}
	code, b = grafana(t, "GET", "/api/dashboards/uid/youless-tig", nil)
	if code != 200 {
		t.Fatalf("dashboard: %d %s", code, b)
	}
	var d struct {
		Dashboard struct {
			Panels []struct {
				Title    string
				TimeFrom string
				Targets  []struct {
					RefID string
					Query string
				}
			}
		}
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	mustHaveData := map[string]bool{"Power now": true, "Net power": true, "Voltage": true,
		"Electricity per day (net)": true, "Electricity per hour": true, "Gas per day": true, "Import today": true}
	queries := 0
	for _, p := range d.Dashboard.Panels {
		for _, tg := range p.Targets {
			q := strings.NewReplacer("$device", "youless", "$timezone", "Europe/Brussels").Replace(tg.Query)
			from := "now-30d"
			if p.TimeFrom == "now/d" {
				from = "now/d"
			}
			code, b := grafana(t, "POST", "/api/ds/query", map[string]any{
				"from": from, "to": "now",
				"queries": []map[string]any{{
					"refId": tg.RefID, "datasource": map[string]string{"uid": "youless-influxdb"},
					"query": q, "rawQuery": true, "resultFormat": "time_series", "intervalMs": 60000, "maxDataPoints": 500,
				}},
			})
			if code != 200 {
				t.Errorf("%s/%s: HTTP %d %s", p.Title, tg.RefID, code, b)
				continue
			}
			var r struct {
				Results map[string]struct {
					Error  string
					Frames []struct {
						Data struct{ Values [][]any }
					}
				}
			}
			json.Unmarshal(b, &r)
			res := r.Results[tg.RefID]
			if res.Error != "" {
				t.Errorf("%s/%s: %s\n%s", p.Title, tg.RefID, res.Error, q)
			}
			rows := 0
			for _, f := range res.Frames {
				if len(f.Data.Values) > 1 {
					rows += len(f.Data.Values[1])
				}
			}
			if mustHaveData[p.Title] && rows == 0 {
				t.Errorf("%s/%s: no data\n%s", p.Title, tg.RefID, q)
			}
			queries++
		}
	}
	t.Logf("%d dashboard queries ran", queries)
}
