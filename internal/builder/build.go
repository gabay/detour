package builder

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"text/template"

	"detour/internal/config"
	"detour/internal/docker"
	"detour/internal/dynamic"
	"detour/internal/rule"
)

// Builder turns containers into a dynamic configuration. It is safe for
// concurrent use.
type Builder struct {
	cfg  config.DockerConfig
	rule *template.Template
}

// New returns a Builder for cfg. It fails when cfg.DefaultRule does not parse.
func New(cfg config.DockerConfig) (*Builder, error) {
	tpl, err := rule.Parse(cfg.DefaultRule)
	if err != nil {
		return nil, fmt.Errorf("docker.defaultRule: %w", err)
	}
	return &Builder{cfg: cfg, rule: tpl}, nil
}

// Build returns the configuration of containers, in the given order, merged
// into one. Containers that cannot be turned into backends are logged and
// skipped. The result is never nil.
func (b *Builder) Build(containers []docker.Container) *dynamic.Configuration {
	return Merge(b.BuildNamed("", containers), func(kind, name string, origins []string) {
		slog.Error("conflicting definitions, removed from configuration", "kind", kind, "name", name, "origins", origins)
	})
}

// BuildNamed builds the unmerged NamedConfiguration entries for containers.
// When provider is non-empty, each entry's origin name is prefixed with
// "<provider>/" so conflict reports identify which Docker provider produced it.
func (b *Builder) BuildNamed(provider string, containers []docker.Container) []NamedConfiguration {
	var named []NamedConfiguration
	for _, c := range containers {
		if conf, ok := b.containerConfig(c); ok {
			origin := ServiceName(c) + "-" + c.ID
			if provider != "" {
				origin = provider + "/" + origin
			}
			named = append(named, NamedConfiguration{Name: origin, Configuration: conf})
		}
	}
	return named
}

// containerConfig builds the configuration of one container, following Traefik's
// docker provider. It reports false when the container must be skipped.
func (b *Builder) containerConfig(c docker.Container) (*dynamic.Configuration, bool) {
	opts, err := ExtractOptions(c, b.cfg)
	if err != nil {
		slog.Warn("invalid labels, skipping container", "container", c.Name, "err", err)
		return nil, false
	}
	if ok, reason := Keep(c, opts, b.cfg); !ok {
		slog.Debug("skipping container", "container", c.Name, "reason", reason)
		return nil, false
	}
	if reason := skipSwarmEntry(c, opts); reason != "" {
		slog.Debug("skipping swarm entry", "container", c.Name, "reason", reason)
		return nil, false
	}

	conf, err := dynamic.DecodeLabels(c.Labels)
	if err != nil {
		slog.Warn("invalid labels, skipping container", "container", c.Name, "err", err)
		return nil, false
	}

	name, active := ServiceName(c), isActive(c)
	l4 := len(conf.TCP.Routers) > 0 || len(conf.TCP.Services) > 0 || len(conf.UDP.Routers) > 0 || len(conf.UDP.Services) > 0

	if err := b.buildTCP(c, opts, conf.TCP, name, active); err != nil {
		slog.Warn("invalid TCP configuration, skipping container", "container", c.Name, "err", err)
		return nil, false
	}
	if err := b.buildUDP(c, opts, conf.UDP, name, active); err != nil {
		slog.Warn("invalid UDP configuration, skipping container", "container", c.Name, "err", err)
		return nil, false
	}
	h := conf.HTTP
	if l4 && len(h.Routers) == 0 && len(h.Middlewares) == 0 && len(h.Services) == 0 {
		return conf, true
	}
	if err := b.buildHTTP(c, opts, h, name, active); err != nil {
		slog.Warn("invalid HTTP configuration, skipping container", "container", c.Name, "err", err)
		return nil, false
	}
	return conf, true
}

// isActive reports whether c should receive backend servers. Stopped or
// unhealthy containers keep their routes, with empty load balancers.
func isActive(c docker.Container) bool {
	return (c.Status == "" || c.Status == "running") && (c.Health == "" || c.Health == "healthy")
}

// skipSwarmEntry returns why a Swarm entry is not used, or "" when it is. A
// service uses its VIP entry with lbswarm and its task entries otherwise.
func skipSwarmEntry(c docker.Container, opts Options) string {
	if c.Source != docker.SourceSwarm {
		return ""
	}
	switch {
	case c.Swarm.VIP && !opts.LBSwarm:
		return "service uses task addresses"
	case c.Swarm.Task && opts.LBSwarm:
		return "service uses the virtual IP"
	case len(c.Networks) == 0 && c.Swarm.VIP:
		return "service has no virtual IP (dnsrr or no network)"
	case len(c.Networks) == 0:
		return "task has no network address"
	}
	return ""
}

func (b *Builder) buildTCP(c docker.Container, opts Options, t *dynamic.TCPConfiguration, name string, active bool) error {
	if len(t.Routers) == 0 && len(t.Services) == 0 {
		return nil
	}
	if len(t.Services) == 0 {
		t.Services = map[string]*dynamic.TCPService{name: {LoadBalancer: &dynamic.TCPServersLoadBalancer{}}}
	}
	if active {
		for svcName, svc := range t.Services {
			if err := b.fillTCPService(c, opts, svc); err != nil {
				return fmt.Errorf("service %q: %w", svcName, err)
			}
		}
	}
	for routerName, r := range t.Routers {
		if r.Rule == "" {
			slog.Error("TCP router has no rule, removed", "router", routerName, "container", c.Name)
			delete(t.Routers, routerName)
			continue
		}
		if r.Service == "" {
			sole, ok := soleKey(t.Services)
			if !ok {
				slog.Error("TCP router has no service and several are defined, removed", "router", routerName, "container", c.Name)
				delete(t.Routers, routerName)
				continue
			}
			r.Service = sole
		}
	}
	return nil
}

func (b *Builder) buildUDP(c docker.Container, opts Options, u *dynamic.UDPConfiguration, name string, active bool) error {
	if len(u.Routers) == 0 && len(u.Services) == 0 {
		return nil
	}
	if len(u.Services) == 0 {
		u.Services = map[string]*dynamic.UDPService{name: {LoadBalancer: &dynamic.UDPServersLoadBalancer{}}}
	}
	if active {
		for svcName, svc := range u.Services {
			if err := b.fillUDPService(c, opts, svc); err != nil {
				return fmt.Errorf("service %q: %w", svcName, err)
			}
		}
	}
	for routerName, r := range u.Routers {
		if r.Service == "" {
			sole, ok := soleKey(u.Services)
			if !ok {
				slog.Error("UDP router has no service and several are defined, removed", "router", routerName, "container", c.Name)
				delete(u.Routers, routerName)
				continue
			}
			r.Service = sole
		}
	}
	return nil
}

func (b *Builder) buildHTTP(c docker.Container, opts Options, h *dynamic.HTTPConfiguration, name string, active bool) error {
	if len(h.Services) == 0 {
		lb := &dynamic.ServersLoadBalancer{}
		h.Services = map[string]*dynamic.Service{name: {LoadBalancer: lb}}
	}
	if active {
		for svcName, svc := range h.Services {
			if err := b.fillHTTPService(c, opts, svc); err != nil {
				return fmt.Errorf("service %q: %w", svcName, err)
			}
		}
	}
	if len(h.Routers) == 0 {
		if len(h.Services) > 1 {
			slog.Info("several services and no router labels, no router created", "container", c.Name)
		} else {
			h.Routers = map[string]*dynamic.Router{name: {}}
		}
	}
	for routerName, r := range h.Routers {
		if r.Rule == "" {
			ruleText, err := rule.Execute(b.rule, rule.RuleModel{Name: name, ContainerName: c.Name, Labels: c.Labels})
			if err != nil || ruleText == "" {
				slog.Error("unable to build the default rule, router removed", "router", routerName, "container", c.Name, "err", err)
				delete(h.Routers, routerName)
				continue
			}
			r.Rule = ruleText
		}
		if r.Service == "" {
			sole, ok := soleKey(h.Services)
			if !ok {
				slog.Error("HTTP router has no service and several are defined, removed", "router", routerName, "container", c.Name)
				delete(h.Routers, routerName)
				continue
			}
			r.Service = sole
		}
	}
	return nil
}

func (b *Builder) fillTCPService(c docker.Container, opts Options, svc *dynamic.TCPService) error {
	if svc.LoadBalancer == nil {
		return errors.New("no load balancer defined")
	}
	lb := svc.LoadBalancer
	if len(lb.Servers) == 0 {
		lb.Servers = []dynamic.TCPServer{{}}
	}
	srv := &lb.Servers[0]
	addr, err := b.hostPort(c, opts, srv.Port)
	if err != nil {
		return err
	}
	srv.Port = ""
	srv.Address = addr
	return nil
}

func (b *Builder) fillUDPService(c docker.Container, opts Options, svc *dynamic.UDPService) error {
	if svc.LoadBalancer == nil {
		return errors.New("no load balancer defined")
	}
	lb := svc.LoadBalancer
	if len(lb.Servers) == 0 {
		lb.Servers = []dynamic.UDPServer{{}}
	}
	srv := &lb.Servers[0]
	addr, err := b.hostPort(c, opts, srv.Port)
	if err != nil {
		return err
	}
	srv.Port = ""
	srv.Address = addr
	return nil
}

func (b *Builder) fillHTTPService(c docker.Container, opts Options, svc *dynamic.Service) error {
	if svc.LoadBalancer == nil {
		return errors.New("no load balancer defined")
	}
	lb := svc.LoadBalancer
	if len(lb.Servers) == 0 {
		lb.Servers = []dynamic.Server{{}}
	}
	srv := &lb.Servers[0]
	if srv.URL != "" {
		if srv.Scheme != "" || srv.Port != "" {
			return errors.New("server URL cannot be combined with a scheme or port")
		}
		return nil
	}
	addr, err := b.hostPort(c, opts, srv.Port)
	if err != nil {
		return err
	}
	scheme := srv.Scheme
	if scheme == "" {
		scheme = "http"
	}
	srv.Scheme, srv.Port = "", ""
	srv.URL = scheme + "://" + addr
	return nil
}

// hostPort resolves "host:port" for c, using port when set and the default port
// otherwise. It fails when no port is available.
func (b *Builder) hostPort(c docker.Container, opts Options, port string) (string, error) {
	host, port, err := ResolveAddress(c, opts, b.cfg, port)
	if err != nil {
		return "", err
	}
	if port == "" {
		return "", fmt.Errorf("no port found for %q, set the server port label", c.Name)
	}
	return net.JoinHostPort(host, port), nil
}

// soleKey returns the only key of m, or false when m has none or several.
func soleKey[V any](m map[string]V) (string, bool) {
	if len(m) != 1 {
		return "", false
	}
	for k := range m {
		return k, true
	}
	return "", false
}
