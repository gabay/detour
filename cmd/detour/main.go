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
	"time"

	"detour/internal/app"
	"detour/internal/builder"
	"detour/internal/compiler"
	"detour/internal/config"
	"detour/internal/docker"
	"detour/internal/server"
	"detour/internal/watch"
)

const (
	defaultConfigPath = "config.yaml"
	reloadDebounce    = 250 * time.Millisecond
)

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

	store := &server.Store{}
	a, err := app.New(*configPath, cfg, buildComponents, store, logger)
	if err != nil {
		return err
	}

	logger.Info("starting detour",
		"version", docker.Version,
		"config", *configPath,
		"listenAddr", cfg.Server.ListenAddr,
		"endpointPath", cfg.Server.EndpointPath,
		"updateInterval", cfg.Server.UpdateInterval.Std(),
		"staleDuration", cfg.Server.StaleDuration.Std(),
		"containers", cfg.Docker.Containers,
		"swarm", cfg.Docker.Swarm,
		"dockerEndpoint", cfg.Docker.Endpoint,
	)

	go func() {
		if err := watch.Watch(ctx, *configPath, reloadDebounce, a.RequestReload); err != nil {
			logger.Error("config watch disabled; reload with SIGHUP", "path", *configPath, "err", err)
		}
	}()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				logger.Info("reload requested by SIGHUP")
				a.RequestReload()
			}
		}
	}()

	if err := a.Run(ctx); err != nil {
		return err
	}
	logger.Info("detour stopped")
	return nil
}

// buildComponents creates the collector, builder and compiler for cfg. The Docker
// daemon is not contacted here; the poller handles connection errors.
func buildComponents(cfg *config.Config) (app.Components, error) {
	coll, err := newCollector(cfg)
	if err != nil {
		return app.Components{}, err
	}
	b, err := builder.New(cfg.Docker)
	if err != nil {
		if closer, ok := coll.(io.Closer); ok {
			_ = closer.Close()
		}
		return app.Components{}, fmt.Errorf("builder: %w", err)
	}
	return app.Components{Collector: coll, Builder: b, Compiler: compiler.New(cfg.Dynamic)}, nil
}

// newCollector creates the Docker collector only when a label source is enabled.
func newCollector(cfg *config.Config) (app.Collector, error) {
	if !cfg.Docker.Containers && !cfg.Docker.Swarm {
		return app.NopCollector{}, nil
	}
	cli, err := docker.NewClient(cfg.Docker)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return docker.NewCollector(cli, cfg.Docker), nil
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
