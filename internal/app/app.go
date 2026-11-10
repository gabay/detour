// Package app wires the Docker collector, builder, compiler and HTTP server into
// a poll loop that keeps the served configuration fresh.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"detour/internal/builder"
	"detour/internal/compiler"
	"detour/internal/config"
	"detour/internal/docker"
	"detour/internal/server"
)

const (
	minRefreshTimeout = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// Collector gathers the current Docker containers and Swarm services.
type Collector interface {
	Collect(ctx context.Context) ([]docker.Container, error)
}

// NopCollector is used when no label source is enabled; it yields no containers.
type NopCollector struct{}

// Collect implements Collector.
func (NopCollector) Collect(context.Context) ([]docker.Container, error) {
	return nil, nil
}

// App runs the refresh loop and the HTTP server.
type App struct {
	cfg       *config.Config
	collector Collector
	builder   *builder.Builder
	compiler  *compiler.Compiler
	store     *server.Store
	logger    *slog.Logger
	now       func() time.Time

	// failing and lastErr drive log deduplication: errors are logged at error
	// level only when they start or change, otherwise at debug level.
	failing bool
	lastErr string
}

// New creates an App. The store is shared with the HTTP server.
func New(cfg *config.Config, collector Collector, b *builder.Builder, c *compiler.Compiler, store *server.Store, logger *slog.Logger) *App {
	if logger == nil {
		logger = slog.Default()
	}
	return &App{
		cfg:       cfg,
		collector: collector,
		builder:   b,
		compiler:  c,
		store:     store,
		logger:    logger,
		now:       time.Now,
	}
}

// Refresh collects, builds, compiles and publishes one configuration snapshot.
// On failure the store is left untouched so the previous snapshot ages out.
func (a *App) Refresh(ctx context.Context) error {
	err := a.refresh(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			a.logFailure(err)
		}
		return err
	}
	if a.failing {
		a.logger.Info("recovered: docker collection succeeded again", "previousError", a.lastErr)
		a.failing = false
		a.lastErr = ""
	}
	return nil
}

func (a *App) refresh(ctx context.Context) error {
	containers, err := a.collector.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	discovered := a.builder.Build(containers)
	conf, conflicts := a.compiler.Compile(discovered)
	a.logger.Debug("configuration compiled", "containers", len(containers), "conflicts", len(conflicts))
	sn, err := server.NewSnapshot(conf, a.now())
	if err != nil {
		return fmt.Errorf("serialise: %w", err)
	}
	a.store.Set(sn)
	return nil
}

func (a *App) logFailure(err error) {
	msg := err.Error()
	switch {
	case !a.failing:
		a.logger.Error("refresh failed; serving last good configuration until it goes stale", "err", msg)
	case msg != a.lastErr:
		a.logger.Error("refresh failed with a new error", "err", msg)
	default:
		a.logger.Debug("refresh failed", "err", msg)
	}
	a.failing = true
	a.lastErr = msg
}

// Run listens on the configured address and serves until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.cfg.Server.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.cfg.Server.ListenAddr, err)
	}
	return a.Serve(ctx, ln)
}

// Serve runs the HTTP server on ln and the poll loop until ctx is cancelled or
// the server fails. The listener is closed on return.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	handler := server.New(a.store, a.cfg.Server, a.now, a.logger).Handler()
	srv := server.NewHTTPServer(ln.Addr().String(), handler)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		a.poll(ctx)
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("serve: %w", err)
		}
	}

	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown: %w", err)
	}
	<-pollDone
	return runErr
}

// poll refreshes once immediately and then at every UpdateInterval. Refreshes run
// sequentially, so ticks that fire during a slow refresh are dropped.
func (a *App) poll(ctx context.Context) {
	interval := a.cfg.Server.UpdateInterval.Std()
	timeout := max(interval, minRefreshTimeout)
	a.refreshWithTimeout(ctx, timeout)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshWithTimeout(ctx, timeout)
		}
	}
}

func (a *App) refreshWithTimeout(ctx context.Context, timeout time.Duration) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_ = a.Refresh(rctx)
}
