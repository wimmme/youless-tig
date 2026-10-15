// Command youless-tig reads a YouLess energy monitor, stores the readings and
// the device's internal logs in InfluxDB, and serves a YouLess-compatible API
// backed by that long-term history.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // time zones without tzdata in the image

	"github.com/wimmme/youless-tig/internal/api"
	"github.com/wimmme/youless-tig/internal/collector"
	"github.com/wimmme/youless-tig/internal/config"
	"github.com/wimmme/youless-tig/internal/influx"
	"github.com/wimmme/youless-tig/internal/store"
	"github.com/wimmme/youless-tig/internal/version"
	"github.com/wimmme/youless-tig/internal/youless"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check the running service (for Docker's HEALTHCHECK) and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version, version.Commit)
		return
	}
	if *healthcheck {
		os.Exit(checkHealth())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "youless-tig:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	loc := time.Local
	log.Info("starting", "version", version.Version, "youless", cfg.YoulessURL, "influx", cfg.InfluxURL,
		"write_url", cfg.InfluxWriteURL, "bucket", cfg.InfluxBucket, "device", cfg.DeviceName,
		"poll", cfg.PollInterval, "history", cfg.History, "tz", loc.String())

	dev, err := youless.NewClient(cfg.YoulessURL, cfg.YoulessPassword, cfg.YoulessTimeout)
	if err != nil {
		return err
	}
	db := &influx.Client{
		URL: cfg.InfluxURL, Token: cfg.InfluxToken,
		WriteURL: cfg.InfluxWriteURL, WriteToken: cfg.InfluxWriteToken,
		Org: cfg.InfluxOrg, Bucket: cfg.InfluxBucket,
	}
	st := &store.Store{DB: db, Device: cfg.DeviceName}
	col := collector.New(collector.Config{
		Loc: loc, PollInterval: cfg.PollInterval, History: cfg.History, Channels: cfg.Channels,
	}, dev, st, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: (&api.Server{
			C: col, St: st, Loc: loc, PollInterval: cfg.PollInterval, Log: log,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("API listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	done := make(chan struct{})
	go func() {
		col.Run(ctx)
		close(done)
	}()
	select {
	case err := <-errc:
		stop()
		<-done
		return err
	case <-ctx.Done():
	}
	log.Info("stopping")
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
	<-done
	return nil
}

// checkHealth asks the running service's /healthz on the configured port.
func checkHealth() int {
	addr := os.Getenv("LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "health:", resp.Status)
		return 1
	}
	return 0
}
