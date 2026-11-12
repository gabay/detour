package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/url"
	"slices"
	"sort"
	"sync"

	"detour/internal/config"
)

// LookupIPFunc resolves hostnames to IP addresses. net.DefaultResolver.LookupIPAddr
// satisfies it.
type LookupIPFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

// ClientFactory builds a Docker API implementation for the given endpoint config
// and optional TLS ServerName.
type ClientFactory func(cfg config.DockerConfig, serverName string) (API, error)

// DiscoveringCollector resolves the hostname in cfg.Endpoint via DNS on each
// Collect call and collects from every resolved IP address concurrently,
// caching API clients across polls.
type DiscoveringCollector struct {
	name    string
	cfg     config.DockerConfig
	scheme  string
	host    string
	port    string
	path    string
	lookup  LookupIPFunc
	makeAPI ClientFactory
	logger  *slog.Logger

	mu      sync.Mutex
	closed  bool
	clients map[string]*Collector // keyed by resolved endpoint URL
}

// NewDiscoveringCollector parses cfg.Endpoint for DNS discovery, validates any
// configured TLS files upfront, and returns a DiscoveringCollector.
func NewDiscoveringCollector(name string, cfg config.DockerConfig, lookup LookupIPFunc, makeAPI ClientFactory, logger *slog.Logger) (*DiscoveringCollector, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	if makeAPI == nil {
		makeAPI = func(c config.DockerConfig, serverName string) (API, error) {
			return NewClientWithServerName(c, serverName)
		}
	}

	scheme, host, port, path, err := parseDiscoverEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	// Validate TLS files eagerly so startup or reload fails fast on bad certs,
	// matching the non-discovering NewClient path.
	if cfg.TLS != nil {
		if _, err := tlsClientConfig(*cfg.TLS, host); err != nil {
			return nil, err
		}
	}

	return &DiscoveringCollector{
		name:    name,
		cfg:     cfg,
		scheme:  scheme,
		host:    host,
		port:    port,
		path:    path,
		lookup:  lookup,
		makeAPI: makeAPI,
		logger:  logger,
		clients: make(map[string]*Collector),
	}, nil
}

func parseDiscoverEndpoint(cfg config.DockerConfig) (scheme, host, port, path string, err error) {
	proto, rest, ok := parseScheme(cfg.Endpoint)
	if !ok {
		return "", "", "", "", fmt.Errorf("docker endpoint %q must have the form scheme://address", cfg.Endpoint)
	}
	if proto == "unix" {
		return "", "", "", "", fmt.Errorf("docker endpoint %q: DNS discovery is not supported for unix sockets", cfg.Endpoint)
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Hostname() == "" || rest == "" {
		return "", "", "", "", fmt.Errorf("docker endpoint %q: invalid host for DNS discovery", cfg.Endpoint)
	}
	port = u.Port()
	if port == "" {
		switch {
		case proto == "https" || (proto == "tcp" && cfg.TLS != nil):
			port = "2376"
		case proto == "http":
			port = "80"
		default:
			port = "2375"
		}
	}
	return proto, u.Hostname(), port, u.Path, nil
}

func parseScheme(endpoint string) (proto, rest string, ok bool) {
	for i := 0; i+3 <= len(endpoint); i++ {
		if endpoint[i:i+3] == "://" {
			return endpoint[:i], endpoint[i+3:], endpoint[:i] != "" && endpoint[i+3:] != ""
		}
	}
	return "", "", false
}

// Close closes all cached collectors.
func (d *DiscoveringCollector) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	old := d.clients
	d.clients = nil
	d.mu.Unlock()

	var errs []error
	for _, c := range old {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Collect resolves d.host, collects from every resolved endpoint concurrently,
// deduplicates entries by (Source, ID, Swarm.VIP, Swarm.Task), and returns them
// sorted by Name then ID.
func (d *DiscoveringCollector) Collect(ctx context.Context) ([]Container, error) {
	if !d.cfg.Containers && !d.cfg.Swarm {
		return []Container{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	addrs, err := d.lookup(ctx, d.host)
	if err != nil {
		d.logger.Warn("docker DNS discovery failed", "provider", d.name, "host", d.host, "err", err)
		return nil, fmt.Errorf("lookup %s: %w", d.host, err)
	}
	endpoints := d.uniqueEndpoints(addrs)
	if len(endpoints) == 0 {
		err := fmt.Errorf("lookup %s returned no addresses", d.host)
		d.logger.Warn("docker DNS discovery returned no addresses", "provider", d.name, "host", d.host)
		return nil, err
	}

	targets, err := d.syncTargets(endpoints)
	if err != nil {
		d.logger.Warn("docker client creation failed", "provider", d.name, "err", err)
		return nil, err
	}

	type result struct {
		endpoint string
		items    []Container
		err      error
	}
	results := make([]result, len(endpoints))
	var wg sync.WaitGroup
	wg.Add(len(endpoints))
	for i, ep := range endpoints {
		coll := targets[ep]
		go func(i int, ep string, coll *Collector) {
			defer wg.Done()
			items, err := coll.Collect(ctx)
			results[i] = result{endpoint: ep, items: items, err: err}
		}(i, ep, coll)
	}
	wg.Wait()

	var errs []error
	type dedupKey struct {
		source Source
		id     string
		vip    bool
		task   bool
	}
	seen := make(map[dedupKey]struct{})
	out := []Container{}
	for _, res := range results {
		if res.err != nil {
			if !errors.Is(res.err, context.Canceled) {
				d.logger.Warn("docker endpoint collection failed", "provider", d.name, "endpoint", res.endpoint, "err", res.err)
			}
			errs = append(errs, fmt.Errorf("%s: %w", res.endpoint, res.err))
			continue
		}
		for _, item := range res.items {
			k := dedupKey{source: item.Source, id: item.ID, vip: item.Swarm.VIP, task: item.Swarm.Task}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, item)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (d *DiscoveringCollector) uniqueEndpoints(addrs []net.IPAddr) []string {
	seen := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		if len(a.IP) == 0 {
			continue
		}
		ep := d.scheme + "://" + net.JoinHostPort(a.IP.String(), d.port) + d.path
		seen[ep] = struct{}{}
	}
	return slices.Sorted(maps.Keys(seen))
}

func (d *DiscoveringCollector) syncTargets(endpoints []string) (map[string]*Collector, error) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("collector is closed")
	}

	wanted := make(map[string]struct{}, len(endpoints))
	for _, ep := range endpoints {
		wanted[ep] = struct{}{}
	}

	var stale []io.Closer
	for ep, coll := range d.clients {
		if _, ok := wanted[ep]; !ok {
			stale = append(stale, coll)
			delete(d.clients, ep)
		}
	}

	serverName := d.host
	if net.ParseIP(d.host) != nil {
		serverName = ""
	}

	targets := make(map[string]*Collector, len(endpoints))
	var createErr error
	for _, ep := range endpoints {
		if coll, ok := d.clients[ep]; ok {
			targets[ep] = coll
			continue
		}
		epCfg := d.cfg
		epCfg.Endpoint = ep
		api, err := d.makeAPI(epCfg, serverName)
		if err != nil {
			createErr = fmt.Errorf("client for %s: %w", ep, err)
			break
		}
		coll := NewCollector(api, epCfg)
		d.clients[ep] = coll
		targets[ep] = coll
	}
	d.mu.Unlock()

	for _, c := range stale {
		_ = c.Close()
	}
	if createErr != nil {
		return nil, createErr
	}
	return targets, nil
}
