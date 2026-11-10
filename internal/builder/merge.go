package builder

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	"detour/internal/dynamic"
)

// NamedConfiguration is a configuration tagged with the name that orders it and
// identifies it in conflict reports.
type NamedConfiguration struct {
	Name          string
	Configuration *dynamic.Configuration
}

// Merge combines the configurations in name order, like Traefik's merge strategy.
// A resource defined by several configurations is merged when its type supports
// it (services append their servers) or kept when the definitions are identical.
// Otherwise every definition of that resource is removed and onConflict, when
// set, is called once for it with all origin names. The inputs are not modified.
func Merge(named []NamedConfiguration, onConflict func(kind, name string, origins []string)) *dynamic.Configuration {
	sorted := slices.Clone(named)
	slices.SortStableFunc(sorted, func(a, b NamedConfiguration) int { return strings.Compare(a.Name, b.Name) })

	hr := newResources[dynamic.Router]("http router", deepEqual[dynamic.Router])
	hs := newResources[dynamic.Service]("http service", (*dynamic.Service).Merge)
	hm := newResources[dynamic.Middleware]("http middleware", deepEqual[dynamic.Middleware])
	ht := newResources[dynamic.ServersTransport]("http serversTransport", deepEqual[dynamic.ServersTransport])
	tr := newResources[dynamic.TCPRouter]("tcp router", deepEqual[dynamic.TCPRouter])
	ts := newResources[dynamic.TCPService]("tcp service", (*dynamic.TCPService).Merge)
	tm := newResources[dynamic.TCPMiddleware]("tcp middleware", deepEqual[dynamic.TCPMiddleware])
	tt := newResources[dynamic.TCPServersTransport]("tcp serversTransport", deepEqual[dynamic.TCPServersTransport])
	ur := newResources[dynamic.UDPRouter]("udp router", deepEqual[dynamic.UDPRouter])
	us := newResources[dynamic.UDPService]("udp service", (*dynamic.UDPService).Merge)
	to := newResources[dynamic.TLSOptions]("tls option", deepEqual[dynamic.TLSOptions])
	tls := newResources[dynamic.TLSStore]("tls store", deepEqual[dynamic.TLSStore])
	var certs []*dynamic.CertAndStores

	for _, nc := range sorted {
		if nc.Configuration == nil {
			continue
		}
		c, o := nc.Configuration.Clone(), nc.Name
		if h := c.HTTP; h != nil {
			addAll(hr, o, h.Routers)
			addAll(hs, o, h.Services)
			addAll(hm, o, h.Middlewares)
			addAll(ht, o, h.ServersTransports)
		}
		if t := c.TCP; t != nil {
			addAll(tr, o, t.Routers)
			addAll(ts, o, t.Services)
			addAll(tm, o, t.Middlewares)
			addAll(tt, o, t.ServersTransports)
		}
		if u := c.UDP; u != nil {
			addAll(ur, o, u.Routers)
			addAll(us, o, u.Services)
		}
		if t := c.TLS; t != nil {
			addValues(to, o, t.Options)
			addValues(tls, o, t.Stores)
			for _, cert := range t.Certificates {
				if cert != nil {
					certs = addCertificate(certs, cert)
				}
			}
		}
	}

	out := &dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{
			Routers:           hr.result(onConflict),
			Services:          hs.result(onConflict),
			Middlewares:       hm.result(onConflict),
			ServersTransports: ht.result(onConflict),
		},
		TCP: &dynamic.TCPConfiguration{
			Routers:           tr.result(onConflict),
			Services:          ts.result(onConflict),
			Middlewares:       tm.result(onConflict),
			ServersTransports: tt.result(onConflict),
		},
		UDP: &dynamic.UDPConfiguration{
			Routers:  ur.result(onConflict),
			Services: us.result(onConflict),
		},
		TLS: &dynamic.TLSConfiguration{
			Certificates: certs,
			Options:      derefMap(to.result(onConflict)),
			Stores:       derefMap(tls.result(onConflict)),
		},
	}
	out.PruneEmpty()
	return out
}

// resources collects the definitions of one kind of resource across configurations.
type resources[T any] struct {
	kind      string
	merge     func(dst, src *T) bool
	values    map[string]*T
	origins   map[string][]string
	conflicts map[string]bool
}

// newResources returns a collector for kind. merge combines src into dst and
// reports false when they cannot be combined.
func newResources[T any](kind string, merge func(dst, src *T) bool) *resources[T] {
	return &resources[T]{
		kind:      kind,
		merge:     merge,
		values:    map[string]*T{},
		origins:   map[string][]string{},
		conflicts: map[string]bool{},
	}
}

func (r *resources[T]) add(name, origin string, v *T) {
	r.origins[name] = append(r.origins[name], origin)
	if r.conflicts[name] {
		return
	}
	dst, ok := r.values[name]
	if !ok {
		r.values[name] = v
		return
	}
	if r.merge(dst, v) {
		return
	}
	delete(r.values, name)
	r.conflicts[name] = true
}

// result returns the merged definitions without the conflicting ones, and
// reports each conflict in name order.
func (r *resources[T]) result(onConflict func(kind, name string, origins []string)) map[string]*T {
	if onConflict != nil {
		for _, name := range slices.Sorted(maps.Keys(r.conflicts)) {
			onConflict(r.kind, name, r.origins[name])
		}
	}
	return r.values
}

func addAll[T any](r *resources[T], origin string, m map[string]*T) {
	for name, v := range m {
		if v != nil {
			r.add(name, origin, v)
		}
	}
}

func addValues[T any](r *resources[T], origin string, m map[string]T) {
	for name, v := range m {
		r.add(name, origin, &v)
	}
}

func derefMap[T any](m map[string]*T) map[string]T {
	out := make(map[string]T, len(m))
	for k, v := range m {
		out[k] = *v
	}
	return out
}

// addCertificate appends cert, or adds its stores to an existing certificate
// with the same certificate and key files.
func addCertificate(certs []*dynamic.CertAndStores, cert *dynamic.CertAndStores) []*dynamic.CertAndStores {
	i := slices.IndexFunc(certs, func(c *dynamic.CertAndStores) bool {
		return c.CertFile == cert.CertFile && c.KeyFile == cert.KeyFile
	})
	if i < 0 {
		return append(certs, cert)
	}
	for _, s := range cert.Stores {
		if !slices.Contains(certs[i].Stores, s) {
			certs[i].Stores = append(certs[i].Stores, s)
		}
	}
	return certs
}

func deepEqual[T any](dst, src *T) bool {
	return reflect.DeepEqual(dst, src)
}
