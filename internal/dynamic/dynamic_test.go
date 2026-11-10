package dynamic

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestClone(t *testing.T) {
	orig, err := DecodeLabels(map[string]string{
		"traefik.http.routers.web.rule":                                   "Host(`example.com`)",
		"traefik.http.routers.web.middlewares":                            "hdr",
		"traefik.http.services.web.loadbalancer.server.port":              "80",
		"traefik.http.middlewares.hdr.headers.customrequestheaders.X-Foo": "bar",
	})
	if err != nil {
		t.Fatal(err)
	}
	orig.HTTP.Services["web"].LoadBalancer.Servers[0].Scheme = "http"

	clone := orig.Clone()
	if !reflect.DeepEqual(orig, clone) {
		t.Fatalf("clone differs from original")
	}

	// json:"-" fields must survive the copy.
	if got := clone.HTTP.Services["web"].LoadBalancer.Servers[0]; got.Port != "80" || got.Scheme != "http" {
		t.Errorf("clone lost json:\"-\" fields: %+v", got)
	}

	// Mutating the original must not leak into the clone.
	orig.HTTP.Routers["web"].Middlewares[0] = "changed"
	orig.HTTP.Services["web"].LoadBalancer.Servers[0].Port = "9999"
	orig.HTTP.Middlewares["hdr"].Headers.CustomRequestHeaders["X-Foo"] = "changed"
	if got := clone.HTTP.Routers["web"].Middlewares[0]; got != "hdr" {
		t.Errorf("slice shared with clone: %q", got)
	}
	if got := clone.HTTP.Services["web"].LoadBalancer.Servers[0].Port; got != "80" {
		t.Errorf("struct slice shared with clone: %q", got)
	}
	if got := clone.HTTP.Middlewares["hdr"].Headers.CustomRequestHeaders["X-Foo"]; got != "bar" {
		t.Errorf("map shared with clone: %q", got)
	}

	var nilConf *Configuration
	if nilConf.Clone() != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestPruneEmpty(t *testing.T) {
	tests := []struct {
		name string
		conf *Configuration
		want string
	}{
		{
			name: "all sections empty",
			conf: &Configuration{HTTP: &HTTPConfiguration{}, TCP: &TCPConfiguration{}, UDP: &UDPConfiguration{}, TLS: &TLSConfiguration{}},
			want: `{}`,
		},
		{
			name: "empty maps inside sections",
			conf: &Configuration{HTTP: &HTTPConfiguration{Routers: map[string]*Router{}}, TLS: &TLSConfiguration{Options: map[string]TLSOptions{}}},
			want: `{}`,
		},
		{
			name: "non-empty section is kept",
			conf: &Configuration{
				HTTP: &HTTPConfiguration{Routers: map[string]*Router{"r": {Service: "s"}}},
				TCP:  &TCPConfiguration{},
			},
			want: `{"http":{"routers":{"r":{"service":"s"}}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.conf.PruneEmpty()
			b, err := json.Marshal(tt.conf)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want {
				t.Errorf("got %s, want %s", b, tt.want)
			}
		})
	}
}

func TestMarshalOmitsLabelOnlyFields(t *testing.T) {
	conf := &Configuration{HTTP: &HTTPConfiguration{Services: map[string]*Service{
		"web": {LoadBalancer: &ServersLoadBalancer{Servers: []Server{{URL: "http://10.0.0.1:80", Port: "80", Scheme: "http"}}}},
	}}}

	j, err := json.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	y, err := yaml.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}

	for name, out := range map[string]string{"json": string(j), "yaml": string(y)} {
		if !strings.Contains(out, "http://10.0.0.1:80") {
			t.Errorf("%s: url missing: %s", name, out)
		}
		if strings.Contains(out, "port") || strings.Contains(out, "scheme") {
			t.Errorf("%s: label-only fields serialized: %s", name, out)
		}
	}
}

const staticConfigYAML = `
http:
  routers:
    api:
      rule: "Host(` + "`api.example.com`" + `)"
      entryPoints: [websecure]
      middlewares: [strip, auth]
      service: api
      tls:
        certResolver: letsencrypt
        domains:
          - main: example.com
            sans: [www.example.com]
  services:
    api:
      loadBalancer:
        passHostHeader: true
        healthCheck:
          path: /health
          interval: 10s
        servers:
          - url: http://10.0.0.1:8080
          - url: http://10.0.0.2:8080
            weight: 2
  middlewares:
    strip:
      stripPrefix:
        prefixes: [/api]
    auth:
      basicAuth:
        users: ["admin:$apr1$hash"]
    plug:
      plugin:
        myplugin:
          key: value
  serversTransports:
    insecure:
      insecureSkipVerify: true
tcp:
  routers:
    db:
      rule: HostSNI(` + "`*`" + `)
      service: db
      tls:
        passthrough: true
  services:
    db:
      loadBalancer:
        servers:
          - address: 10.0.0.3:5432
udp:
  routers:
    dns:
      entryPoints: [dns]
      service: dns
  services:
    dns:
      loadBalancer:
        servers:
          - address: 10.0.0.4:53
tls:
  options:
    default:
      minVersion: VersionTLS12
  stores:
    default:
      defaultCertificate:
        certFile: /certs/a.crt
        keyFile: /certs/a.key
`

func TestYAMLRoundTrip(t *testing.T) {
	var first Configuration
	if err := yaml.Unmarshal([]byte(staticConfigYAML), &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got := first.HTTP.Services["api"].LoadBalancer.Servers[1].Weight; got == nil || *got != 2 {
		t.Fatalf("weight not decoded: %v", got)
	}
	if got := first.HTTP.Middlewares["plug"].Plugin["myplugin"]["key"]; got != "value" {
		t.Fatalf("plugin not decoded: %v", got)
	}

	out, err := yaml.Marshal(&first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var second Configuration
	if err := yaml.Unmarshal(out, &second); err != nil {
		t.Fatalf("re-unmarshal: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(&first, &second) {
		t.Errorf("round trip changed the configuration:\n%s", out)
	}
}
