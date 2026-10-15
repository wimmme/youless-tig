// Package config reads the settings from environment variables. Secrets can be
// given directly (INFLUX_TOKEN) or as a file (INFLUX_TOKEN_FILE, e.g. a Docker
// secret), never both. Every variable is listed in .env.example.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wimmme/youless-tig/internal/youless"
)

// Config is the validated configuration.
type Config struct {
	YoulessURL      string
	YoulessPassword string
	YoulessTimeout  time.Duration
	DeviceName      string
	PollInterval    time.Duration
	Channels        []youless.Channel // nil: automatic
	History         bool

	InfluxURL        string
	InfluxToken      string
	InfluxOrg        string
	InfluxBucket     string
	InfluxWriteURL   string // empty: InfluxURL
	InfluxWriteToken string // empty: InfluxToken

	Listen   string
	LogLevel slog.Level
}

// Load reads the configuration with getenv (os.Getenv) and readFile
// (os.ReadFile); all problems are reported together.
func Load(getenv func(string) string, readFile func(string) ([]byte, error)) (*Config, error) {
	l := loader{getenv: getenv, readFile: readFile}
	c := &Config{
		YoulessURL:      l.url("YOULESS_URL", true),
		YoulessPassword: l.secret("YOULESS_PASSWORD", false),
		YoulessTimeout:  l.duration("YOULESS_TIMEOUT", 10*time.Second, time.Second, 2*time.Minute),
		DeviceName:      l.str("DEVICE_NAME", "youless"),
		PollInterval:    l.duration("POLL_INTERVAL", 10*time.Second, 2*time.Second, time.Hour),
		History:         l.boolean("HISTORY", true),

		InfluxURL:        l.url("INFLUX_URL", true),
		InfluxToken:      l.secret("INFLUX_TOKEN", true),
		InfluxOrg:        l.str("INFLUX_ORG", ""),
		InfluxBucket:     l.str("INFLUX_BUCKET", ""),
		InfluxWriteURL:   l.url("INFLUX_WRITE_URL", false),
		InfluxWriteToken: l.secret("INFLUX_WRITE_TOKEN", false),

		Listen: l.str("LISTEN", ":8080"),
	}
	if c.InfluxOrg == "" {
		l.fail("INFLUX_ORG is required")
	}
	if c.InfluxBucket == "" {
		l.fail("INFLUX_BUCKET is required")
	}
	if strings.ContainsAny(c.DeviceName, "\n\r") || c.DeviceName == "" {
		l.fail("DEVICE_NAME must be one non-empty line")
	}
	if c.YoulessPassword != "" && len(c.YoulessPassword) > 21 {
		l.fail("YOULESS_PASSWORD is longer than the 21 characters a YouLess accepts")
	}
	if v := strings.TrimSpace(getenv("CHANNELS")); v != "" && v != "auto" {
		for _, s := range strings.Split(v, ",") {
			ch, err := youless.ParseChannel(strings.TrimSpace(s))
			if err != nil {
				l.fail("CHANNELS: " + err.Error())
				continue
			}
			c.Channels = append(c.Channels, ch)
		}
	}
	switch strings.ToLower(l.str("LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn", "warning":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		l.fail("LOG_LEVEL: want debug, info, warn or error")
	}
	if len(l.errs) > 0 {
		return nil, errors.Join(l.errs...)
	}
	return c, nil
}

// FromEnv loads from the process environment.
func FromEnv() (*Config, error) { return Load(os.Getenv, os.ReadFile) }

type loader struct {
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	errs     []error
}

func (l *loader) fail(msg string) { l.errs = append(l.errs, errors.New(msg)) }

func (l *loader) str(key, def string) string {
	if v := strings.TrimSpace(l.getenv(key)); v != "" {
		return v
	}
	return def
}

func (l *loader) url(key string, required bool) string {
	v := strings.TrimRight(strings.TrimSpace(l.getenv(key)), "/")
	if v == "" {
		if required {
			l.fail(key + " is required")
		}
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		l.fail(fmt.Sprintf("%s: want http(s)://host[:port] without credentials, got %q", key, v))
	}
	return v
}

// secret reads KEY or KEY_FILE.
func (l *loader) secret(key string, required bool) string {
	v, file := l.getenv(key), strings.TrimSpace(l.getenv(key+"_FILE"))
	switch {
	case v != "" && file != "":
		l.fail(fmt.Sprintf("set %s or %s_FILE, not both", key, key))
		return ""
	case file != "":
		b, err := l.readFile(file)
		if err != nil {
			l.fail(fmt.Sprintf("%s_FILE: %v", key, err))
			return ""
		}
		v = string(b)
	}
	v = strings.TrimSpace(v)
	if v == "" && required {
		l.fail(fmt.Sprintf("%s (or %s_FILE) is required", key, key))
	}
	return v
}

func (l *loader) duration(key string, def, lo, hi time.Duration) time.Duration {
	v := strings.TrimSpace(l.getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.fail(fmt.Sprintf("%s: %q is not a duration like 10s or 1m", key, v))
		return def
	}
	if d < lo || d > hi {
		l.fail(fmt.Sprintf("%s: %s is outside %s…%s", key, d, lo, hi))
		return def
	}
	return d
}

func (l *loader) boolean(key string, def bool) bool {
	v := strings.TrimSpace(l.getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.fail(fmt.Sprintf("%s: want true or false, got %q", key, v))
		return def
	}
	return b
}
