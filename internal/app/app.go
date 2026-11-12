// Package app wires the Docker collector, builder, compiler and HTTP server into
// a poll loop that keeps the served configuration fresh and applies config reloads.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"slices"
	"sync"
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

// Provider pairs one named Docker collector with its configuration builder.
type Provider struct {
	Name      string
	Collector Collector
	Builder   *builder.Builder
}

// Components are the parts built from one configuration. A reload replaces them as a unit.
type Components struct {
	Providers []Provider
	Compiler  *compiler.Compiler
}

// Factory builds the Components for cfg. A collector that implements io.Closer is
// closed when it is replaced, so a Factory must return a fresh collector on each call.
type Factory func(cfg *config.Config, logger *slog.Logger) (Components, error)

// App runs the refresh loop and the HTTP server, and applies configuration reloads.
type App struct {
	configPath string
	factory    Factory
	store      *server.Store
	srv        *server.Server
	logger     *slog.Logger
	now        func() time.Time
	reloadCh   chan struct{}

	// mu serialises Refresh and Reload and guards the fields below.
	mu         sync.Mutex
	cfg        *config.Config
	comp       Components
	listenAddr string

	// failing and lastErr drive log deduplication: errors are logged at error
	// level only when they start or change, otherwise at debug level.
	failing bool
	lastErr string
}

// New creates an App for cfg, built with factory. The store is shared with the
// HTTP server. configPath is the file reloaded by RequestReload.
func New(configPath string, cfg *config.Config, factory Factory, store *server.Store, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	comp, err := factory(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("build components: %w", err)
	}
	now := time.Now
	return &App{
		configPath: configPath,
		factory:    factory,
		store:      store,
		srv:        server.New(store, cfg.Server, now, logger),
		logger:     logger,
		now:        now,
		reloadCh:   make(chan struct{}, 1),
		cfg:        cfg,
		comp:       comp,
		listenAddr: cfg.Server.ListenAddr,
	}, nil
}

// Refresh collects, builds, compiles and publishes one configuration snapshot.
// On failure the store is left untouched so the previous snapshot ages out.
func (a *App) Refresh(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshLocked(ctx)
}

func (a *App) refreshLocked(ctx context.Context) error {
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
	type providerResult struct {
		named []builder.NamedConfiguration
		count int
		err   error
	}
	results := make([]providerResult, len(a.comp.Providers))
	var wg sync.WaitGroup
	wg.Add(len(a.comp.Providers))
	for i, p := range a.comp.Providers {
		go func(i int, p Provider) {
			defer wg.Done()
			containers, err := p.Collector.Collect(ctx)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					a.logger.Warn("docker provider collection failed", "provider", p.Name, "err", err)
				}
				results[i] = providerResult{err: fmt.Errorf("provider %s: %w", p.Name, err)}
				return
			}
			results[i] = providerResult{
				named: p.Builder.BuildNamed(p.Name, containers),
				count: len(containers),
			}
		}(i, p)
	}
	wg.Wait()

	var (
		errs            []error
		allNamed        []builder.NamedConfiguration
		totalContainers int
	)
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		allNamed = append(allNamed, r.named...)
		totalContainers += r.count
	}
	if len(errs) > 0 {
		return fmt.Errorf("collect: %w", errors.Join(errs...))
	}

	discovered := builder.Merge(allNamed, func(kind, name string, origins []string) {
		a.logger.Error("conflicting definitions, removed from configuration", "kind", kind, "name", name, "origins", origins)
	})
	conf, conflicts := a.comp.Compiler.Compile(discovered)
	a.logger.Debug("configuration compiled", "providers", len(a.comp.Providers), "containers", totalContainers, "conflicts", len(conflicts))
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

// Reload applies newCfg. An identical configuration is a no-op. A changed docker or
// dynamic section rebuilds the components; if that fails the previous configuration
// is kept and the error is returned. Otherwise the new configuration is applied and
// published by an immediate refresh, whose failure is logged and leaves the previous
// snapshot in place. server.listenAddr is not applied; a change is logged as a warning.
func (a *App) Reload(ctx context.Context, newCfg *config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if reflect.DeepEqual(a.cfg, newCfg) {
		a.logger.Debug("configuration unchanged", "path", a.configPath)
		return nil
	}
	changed := changedSections(a.cfg, newCfg)

	comp, rebuilt := a.comp, false
	if slices.ContainsFunc(changed, isComponentSection) {
		var err error
		if comp, err = a.factory(newCfg, a.logger); err != nil {
			return fmt.Errorf("build components: %w", err)
		}
		rebuilt = true
	}
	oldProviders := a.comp.Providers
	a.comp, a.cfg = comp, newCfg
	a.srv.SetConfig(newCfg.Server)
	if rebuilt {
		closeProviders(oldProviders, a.logger)
	}

	if newCfg.Server.ListenAddr != a.listenAddr {
		a.logger.Warn("server.listenAddr changed; restart required to apply", "current", a.listenAddr, "configured", newCfg.Server.ListenAddr)
	}
	a.logger.Info("configuration reloaded", "path", a.configPath, "changed", changed)
	_ = a.refreshLocked(ctx)
	return nil
}

func isComponentSection(section string) bool {
	return section == "docker" || section == "dynamic"
}

// changedSections names the top-level sections, or server fields, that differ between old and cur.
func changedSections(old, cur *config.Config) []string {
	var changed []string
	if !dockerConfigsEqual(old.Docker, cur.Docker) {
		changed = append(changed, "docker")
	}
	if !reflect.DeepEqual(old.Dynamic, cur.Dynamic) {
		changed = append(changed, "dynamic")
	}
	o, c := old.Server, cur.Server
	if o.ListenAddr != c.ListenAddr {
		changed = append(changed, "server.listenAddr")
	}
	if o.EndpointPath != c.EndpointPath {
		changed = append(changed, "server.endpointPath")
	}
	if o.UpdateInterval != c.UpdateInterval {
		changed = append(changed, "server.updateInterval")
	}
	if o.StaleDuration != c.StaleDuration {
		changed = append(changed, "server.staleDuration")
	}
	return changed
}

func dockerConfigsEqual(a, b config.DockerProviders) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func closeProviders(providers []Provider, logger *slog.Logger) {
	for _, p := range providers {
		if closer, ok := p.Collector.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				logger.Warn("closing collector", "provider", p.Name, "err", err)
			}
		}
	}
}

// RequestReload asks the poll loop to reload the configuration file. It never blocks;
// requests that arrive while one is pending are coalesced.
func (a *App) RequestReload() {
	select {
	case a.reloadCh <- struct{}{}:
	default:
	}
}

// reloadFile loads the configuration file and applies it. Failures keep the previous configuration.
func (a *App) reloadFile(ctx context.Context) {
	cfg, err := config.LoadFile(a.configPath)
	if err == nil {
		err = a.Reload(ctx, cfg)
	}
	if err != nil {
		a.logger.Error("config reload failed, keeping previous configuration", "path", a.configPath, "err", err)
	}
}

// Run listens on the configured address and serves until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.listenAddr, err)
	}
	return a.Serve(ctx, ln)
}

// Serve runs the HTTP server on ln and the poll loop until ctx is cancelled or
// the server fails. The listener is closed on return, and so is the collector.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	srv := server.NewHTTPServer(ln.Addr().String(), a.srv.Handler())

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

	a.mu.Lock()
	closeProviders(a.comp.Providers, a.logger)
	a.mu.Unlock()
	return runErr
}

// poll refreshes once immediately, then at every UpdateInterval and whenever a
// reload is requested. Refreshes and reloads run sequentially in this goroutine,
// so ticks that fire during a slow refresh are dropped.
func (a *App) poll(ctx context.Context) {
	a.refreshWithTimeout(ctx)

	interval := a.updateInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshWithTimeout(ctx)
		case <-a.reloadCh:
			a.reloadFile(ctx)
			if d := a.updateInterval(); d != interval {
				interval = d
				ticker.Reset(d)
			}
		}
	}
}

func (a *App) updateInterval() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Server.UpdateInterval.Std()
}

func (a *App) refreshWithTimeout(ctx context.Context) {
	timeout := max(a.updateInterval(), minRefreshTimeout)
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_ = a.Refresh(rctx)
}
