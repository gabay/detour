// Package compiler merges the static dynamic configuration with the configuration
// discovered from containers. Static entries always take precedence.
package compiler

import (
	"log/slog"
	"maps"
	"slices"
	"strings"

	"detour/internal/dynamic"
)

// Conflict records a discovered entry that was dropped because the static
// configuration already defines an entry with the same kind and name.
type Conflict struct {
	Kind string
	Name string
}

// Compiler merges a static configuration with discovered configurations.
// The static configuration is never modified, so Compile may be called
// concurrently.
type Compiler struct {
	static *dynamic.Configuration
}

// New returns a Compiler for the given static configuration. A nil static
// configuration is treated as an empty one.
func New(static *dynamic.Configuration) *Compiler {
	c := static.Clone()
	if c == nil {
		c = &dynamic.Configuration{}
	}
	return &Compiler{static: c}
}

// Compile returns the static configuration plus every discovered resource whose
// key is not defined statically. Discovered resources with a statically defined
// key are dropped and reported as conflicts, sorted by kind and name. The
// result and the discovered input share no memory.
func (c *Compiler) Compile(discovered *dynamic.Configuration) (*dynamic.Configuration, []Conflict) {
	res := c.static.Clone()
	d := discovered.Clone()
	if d == nil {
		d = &dynamic.Configuration{}
	}

	var conflicts []Conflict
	if d.HTTP != nil {
		if res.HTTP == nil {
			res.HTTP = &dynamic.HTTPConfiguration{}
		}
		h, dh := res.HTTP, d.HTTP
		h.Routers = mergeMap(&conflicts, "http router", h.Routers, dh.Routers)
		h.Services = mergeMap(&conflicts, "http service", h.Services, dh.Services)
		h.Middlewares = mergeMap(&conflicts, "http middleware", h.Middlewares, dh.Middlewares)
		h.ServersTransports = mergeMap(&conflicts, "http serversTransport", h.ServersTransports, dh.ServersTransports)
	}
	if d.TCP != nil {
		if res.TCP == nil {
			res.TCP = &dynamic.TCPConfiguration{}
		}
		t, dt := res.TCP, d.TCP
		t.Routers = mergeMap(&conflicts, "tcp router", t.Routers, dt.Routers)
		t.Services = mergeMap(&conflicts, "tcp service", t.Services, dt.Services)
		t.Middlewares = mergeMap(&conflicts, "tcp middleware", t.Middlewares, dt.Middlewares)
		t.ServersTransports = mergeMap(&conflicts, "tcp serversTransport", t.ServersTransports, dt.ServersTransports)
	}
	if d.UDP != nil {
		if res.UDP == nil {
			res.UDP = &dynamic.UDPConfiguration{}
		}
		u, du := res.UDP, d.UDP
		u.Routers = mergeMap(&conflicts, "udp router", u.Routers, du.Routers)
		u.Services = mergeMap(&conflicts, "udp service", u.Services, du.Services)
	}
	if d.TLS != nil {
		if res.TLS == nil {
			res.TLS = &dynamic.TLSConfiguration{}
		}
		tls, dtls := res.TLS, d.TLS
		tls.Options = mergeMap(&conflicts, "tls option", tls.Options, dtls.Options)
		tls.Stores = mergeMap(&conflicts, "tls store", tls.Stores, dtls.Stores)
		tls.Certificates = appendCertificates(tls.Certificates, dtls.Certificates)
	}

	slices.SortFunc(conflicts, func(a, b Conflict) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Name, b.Name)
	})
	for _, cf := range conflicts {
		slog.Warn("dynamic entry dropped: static configuration takes precedence", "kind", cf.Kind, "name", cf.Name)
	}

	c.warnDanglingReferences(res, d)
	res.PruneEmpty()
	return res, conflicts
}

// mergeMap adds the discovered entries whose names are not in static, recording
// the others as conflicts. static is returned (possibly newly allocated).
func mergeMap[V any](conflicts *[]Conflict, kind string, static, discovered map[string]V) map[string]V {
	for name, v := range discovered {
		if _, ok := static[name]; ok {
			*conflicts = append(*conflicts, Conflict{Kind: kind, Name: name})
			continue
		}
		if static == nil {
			static = make(map[string]V, len(discovered))
		}
		static[name] = v
	}
	return static
}

// appendCertificates appends discovered certificates whose cert and key files
// are not already present in certs.
func appendCertificates(certs, discovered []*dynamic.CertAndStores) []*dynamic.CertAndStores {
	for _, d := range discovered {
		exists := slices.ContainsFunc(certs, func(c *dynamic.CertAndStores) bool {
			return c.CertFile == d.CertFile && c.KeyFile == d.KeyFile
		})
		if !exists {
			certs = append(certs, d)
		}
	}
	return certs
}

// warnDanglingReferences logs discovered HTTP routers whose service or
// middlewares are not defined anywhere in res. Names containing "@" are
// provider-qualified and are not checked. Routers that lost to a static router
// with the same name are skipped.
func (c *Compiler) warnDanglingReferences(res, discovered *dynamic.Configuration) {
	if discovered.HTTP == nil {
		return
	}
	for _, name := range slices.Sorted(maps.Keys(discovered.HTTP.Routers)) {
		r := discovered.HTTP.Routers[name]
		if r == nil || c.static.HTTP != nil && c.static.HTTP.Routers[name] != nil {
			continue
		}
		if r.Service != "" && !strings.Contains(r.Service, "@") && res.HTTP.Services[r.Service] == nil {
			slog.Warn("http router references unknown service", "router", name, "service", r.Service)
		}
		for _, m := range r.Middlewares {
			if !strings.Contains(m, "@") && res.HTTP.Middlewares[m] == nil {
				slog.Warn("http router references unknown middleware", "router", name, "middleware", m)
			}
		}
	}
}
