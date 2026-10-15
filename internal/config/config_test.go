package config

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wimmme/youless-tig/internal/youless"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("no such file " + p)
	}
}

func minimal() map[string]string {
	return map[string]string{
		"YOULESS_URL":   "http://192.168.0.61",
		"INFLUX_URL":    "http://influxdb:8086/",
		"INFLUX_TOKEN":  "tok",
		"INFLUX_ORG":    "home",
		"INFLUX_BUCKET": "youless",
	}
}

func TestDefaults(t *testing.T) {
	c, err := Load(env(minimal()), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.InfluxURL != "http://influxdb:8086" || c.PollInterval != 10*time.Second || c.YoulessTimeout != 10*time.Second ||
		!c.History || c.Channels != nil || c.DeviceName != "youless" || c.Listen != ":8080" || c.LogLevel != slog.LevelInfo ||
		c.InfluxWriteURL != "" || c.YoulessPassword != "" {
		t.Errorf("defaults: %+v", c)
	}
}

func TestAll(t *testing.T) {
	m := minimal()
	delete(m, "INFLUX_TOKEN")
	for k, v := range map[string]string{
		"YOULESS_PASSWORD_FILE": "/run/secrets/yl", "YOULESS_TIMEOUT": "5s", "DEVICE_NAME": "huis",
		"POLL_INTERVAL": "30s", "CHANNELS": "electricity, s0", "HISTORY": "false",
		"INFLUX_TOKEN_FILE": "/secrets/tok", "INFLUX_WRITE_URL": "http://telegraf:8186",
		"INFLUX_WRITE_TOKEN": "relay", "LISTEN": ":9000", "LOG_LEVEL": "DEBUG",
	} {
		m[k] = v
	}
	c, err := Load(env(m), files(map[string]string{"/run/secrets/yl": "pw\n", "/secrets/tok": " filetok\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.YoulessPassword != "pw" || c.InfluxToken != "filetok" || c.YoulessTimeout != 5*time.Second ||
		c.DeviceName != "huis" || c.PollInterval != 30*time.Second || c.History || c.Listen != ":9000" ||
		c.InfluxWriteURL != "http://telegraf:8186" || c.InfluxWriteToken != "relay" || c.LogLevel != slog.LevelDebug ||
		len(c.Channels) != 2 || c.Channels[1] != youless.S0 {
		t.Errorf("got %+v", c)
	}
}

func TestErrors(t *testing.T) {
	_, err := Load(env(map[string]string{"POLL_INTERVAL": "1s", "CHANNELS": "power"}), files(nil))
	if err == nil {
		t.Fatal("want errors")
	}
	for _, want := range []string{"YOULESS_URL is required", "INFLUX_URL is required", "INFLUX_TOKEN (or INFLUX_TOKEN_FILE) is required",
		"INFLUX_ORG is required", "INFLUX_BUCKET is required", "POLL_INTERVAL", "CHANNELS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
	for name, change := range map[string]map[string]string{
		"both token and file": {"INFLUX_TOKEN_FILE": "/x"},
		"missing file":        {"INFLUX_TOKEN": "", "INFLUX_TOKEN_FILE": "/nope"},
		"bad url":             {"YOULESS_URL": "192.168.0.61"},
		"credentials in url":  {"INFLUX_URL": "http://admin:pw@influxdb:8086"},
		"bad duration":        {"YOULESS_TIMEOUT": "ten"},
		"bad bool":            {"HISTORY": "maybe"},
		"bad level":           {"LOG_LEVEL": "loud"},
		"long password":       {"YOULESS_PASSWORD": strings.Repeat("x", 22)},
	} {
		m := minimal()
		for k, v := range change {
			m[k] = v
		}
		if _, err := Load(env(m), files(nil)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// Errors never contain secret values.
func TestErrorsHideSecrets(t *testing.T) {
	m := minimal()
	m["INFLUX_TOKEN_FILE"] = "/x"
	m["INFLUX_TOKEN"] = "SuperSecretToken"
	_, err := Load(env(m), files(nil))
	if err == nil || strings.Contains(err.Error(), "SuperSecretToken") {
		t.Errorf("got %v", err)
	}
}
