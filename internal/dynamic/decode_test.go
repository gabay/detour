package dynamic

import (
	"reflect"
	"testing"
)

func TestDecodeLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		check  func(t *testing.T, c *Configuration)
	}{
		{
			name: "router with service and port",
			labels: map[string]string{
				"traefik.enable":                                     "true",
				"traefik.http.routers.web.rule":                      "Host(`example.com`)",
				"traefik.http.routers.web.service":                   "web",
				"traefik.http.routers.web.entrypoints":               "web",
				"traefik.http.services.web.loadbalancer.server.port": "80",
			},
			check: func(t *testing.T, c *Configuration) {
				r := c.HTTP.Routers["web"]
				if r == nil || r.Rule != "Host(`example.com`)" || r.Service != "web" {
					t.Fatalf("router = %+v", r)
				}
				if !reflect.DeepEqual(r.EntryPoints, []string{"web"}) {
					t.Errorf("entryPoints = %v", r.EntryPoints)
				}
				lb := c.HTTP.Services["web"].LoadBalancer
				if lb == nil {
					t.Fatal("missing load balancer")
				}
				if want := []Server{{Port: "80"}}; !reflect.DeepEqual(lb.Servers, want) {
					t.Errorf("servers = %+v, want %+v", lb.Servers, want)
				}
				if lb.PassHostHeader == nil || !*lb.PassHostHeader {
					t.Errorf("passHostHeader should default to true")
				}
			},
		},
		{
			name: "passHostHeader can be disabled",
			labels: map[string]string{
				"traefik.http.services.web.loadbalancer.passhostheader": "false",
				"traefik.http.services.web.loadbalancer.server.port":    "8080",
			},
			check: func(t *testing.T, c *Configuration) {
				if pass := c.HTTP.Services["web"].LoadBalancer.PassHostHeader; pass == nil || *pass {
					t.Errorf("passHostHeader = %v, want false", pass)
				}
			},
		},
		{
			name: "middlewares",
			labels: map[string]string{
				"traefik.http.middlewares.strip.stripprefix.prefixes":             "/api,/v1",
				"traefik.http.middlewares.hdr.headers.customrequestheaders.X-Foo": "bar",
				"traefik.http.middlewares.auth.basicauth.users":                   "user:$apr1$hash",
				"traefik.http.middlewares.auth.basicauth.realm":                   "detour",
				"traefik.http.middlewares.gz.compress":                            "true",
			},
			check: func(t *testing.T, c *Configuration) {
				m := c.HTTP.Middlewares
				if got := m["strip"].StripPrefix; got == nil || !reflect.DeepEqual(got.Prefixes, []string{"/api", "/v1"}) {
					t.Errorf("stripPrefix = %+v", got)
				}
				if got := m["hdr"].Headers; got == nil || got.CustomRequestHeaders["X-Foo"] != "bar" {
					t.Errorf("headers = %+v", got)
				}
				if got := m["auth"].BasicAuth; got == nil || !reflect.DeepEqual(got.Users, []string{"user:$apr1$hash"}) || got.Realm != "detour" {
					t.Errorf("basicAuth = %+v", got)
				}
				if m["gz"].Compress == nil {
					t.Errorf("compress enabled with a bare value should be set")
				}
			},
		},
		{
			name: "tcp router and service",
			labels: map[string]string{
				"traefik.tcp.routers.db.rule":                      "HostSNI(`*`)",
				"traefik.tcp.routers.db.service":                   "db",
				"traefik.tcp.services.db.loadbalancer.server.port": "5432",
			},
			check: func(t *testing.T, c *Configuration) {
				if r := c.TCP.Routers["db"]; r == nil || r.Rule != "HostSNI(`*`)" || r.Service != "db" {
					t.Fatalf("tcp router = %+v", r)
				}
				if want := []TCPServer{{Port: "5432"}}; !reflect.DeepEqual(c.TCP.Services["db"].LoadBalancer.Servers, want) {
					t.Errorf("tcp servers = %+v, want %+v", c.TCP.Services["db"].LoadBalancer.Servers, want)
				}
			},
		},
		{
			name: "udp service",
			labels: map[string]string{
				"traefik.udp.services.dns.loadbalancer.server.port": "53",
			},
			check: func(t *testing.T, c *Configuration) {
				if want := []UDPServer{{Port: "53"}}; !reflect.DeepEqual(c.UDP.Services["dns"].LoadBalancer.Servers, want) {
					t.Errorf("udp servers = %+v, want %+v", c.UDP.Services["dns"].LoadBalancer.Servers, want)
				}
			},
		},
		{
			name: "tls default store",
			labels: map[string]string{
				"traefik.tls.stores.default.defaultcertificate.certfile": "/certs/a.crt",
				"traefik.tls.stores.default.defaultcertificate.keyfile":  "/certs/a.key",
			},
			check: func(t *testing.T, c *Configuration) {
				want := &Certificate{CertFile: "/certs/a.crt", KeyFile: "/certs/a.key"}
				if got := c.TLS.Stores["default"].DefaultCertificate; !reflect.DeepEqual(got, want) {
					t.Errorf("defaultCertificate = %+v, want %+v", got, want)
				}
			},
		},
		{
			name: "ignored labels",
			labels: map[string]string{
				"traefik.enable":         "true",
				"traefik.docker.network": "proxy",
				"traefik.swarm.replicas": "3",
				"com.example.unrelated":  "x",
			},
			check: func(t *testing.T, c *Configuration) {
				if c.HTTP == nil || c.TCP == nil || c.UDP == nil || c.TLS == nil {
					t.Fatal("sections should be pre-initialised")
				}
				c.PruneEmpty()
				if c.HTTP != nil && len(c.HTTP.Routers) > 0 {
					t.Errorf("unexpected routers: %+v", c.HTTP.Routers)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := DecodeLabels(tt.labels)
			if err != nil {
				t.Fatalf("DecodeLabels() error = %v", err)
			}
			tt.check(t, c)
		})
	}
}

func TestDecodeLabelsErrors(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
	}{
		{name: "non-numeric priority", labels: map[string]string{"traefik.http.routers.r.priority": "abc"}},
		{name: "invalid bool value", labels: map[string]string{"traefik.tcp.services.s.loadbalancer.server.tls": "maybe"}},
		{name: "unknown field under router", labels: map[string]string{"traefik.http.routers.r.nosuchfield": "x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeLabels(tt.labels); err == nil {
				t.Errorf("DecodeLabels() error = nil, want an error")
			}
		})
	}
}
