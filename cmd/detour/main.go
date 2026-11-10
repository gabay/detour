// Command detour serves merged Traefik dynamic configuration over HTTP for
// Traefik's HTTP provider.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"detour/internal/app"
	"detour/internal/builder"
	"detour/internal/compiler"
	"detour/internal/config"
	"detour/internal/docker"
	"detour/internal/server"
)

const defaultConfigPath = "config.yaml"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "detour:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("detour", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", envOr("DETOUR_CONFIG", defaultConfigPath), "path to the configuration file (env DETOUR_CONFIG)")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn or error")
	logFormat := fs.String("log-format", "text", "log format: text or json")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, "detour", docker.Version)
		return nil
	}

	logger, err := newLogger(stderr, *logLevel, *logFormat)
	if err != nil {
		return err
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	collector, closeCollector, err := newCollector(cfg)
	if err != nil {
		return err
	}
	defer closeCollector()

	b, err := builder.New(cfg.Docker)
	if err != nil {
		return fmt.Errorf("builder: %w", err)
	}
	store := &server.Store{}
	a := app.New(cfg, collector, b, compiler.New(cfg.Dynamic), store, logger)

	logger.Info("starting detour",
		"version", docker.Version,
		"config", *configPath,
		"listenAddr", cfg.Server.ListenAddr,
		"endpointPath", cfg.Server.EndpointPath,
		"updateInterval", cfg.Server.UpdateInterval.Std(),
		"staleDuration", cfg.Server.StaleDuration.Std(),
		"dockerMode", cfg.Docker.Mode,
		"containers", cfg.Docker.ContainersEnabled(),
		"swarm", cfg.Docker.SwarmEnabled(),
		"dockerEndpoint", cfg.Docker.Endpoint,
	)

	if err := a.Run(ctx); err != nil {
		return err
	}
	logger.Info("detour stopped")
	return nil
}

// newCollector creates the Docker collector only when a label source is enabled.
// The Docker daemon is not contacted here; the poller handles connection errors.
func newCollector(cfg *config.Config) (app.Collector, func(), error) {
	if !cfg.Docker.ContainersEnabled() && !cfg.Docker.SwarmEnabled() {
		return app.NopCollector{}, func() {}, nil
	}
	cli, err := docker.NewClient(cfg.Docker)
	if err != nil {
		return nil, nil, fmt.Errorf("docker client: %w", err)
	}
	c := docker.NewCollector(cli, cfg.Docker)
	return c, func() { _ = c.Close() }, nil
}

func newLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown -log-level %q (want debug, info, warn or error)", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(format) {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("unknown -log-format %q (want text or json)", format)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
