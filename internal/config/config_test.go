package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDefaults(t *testing.T) {
	want := Default()
	if want.Server.ListenAddr != ":7890" || want.Server.EndpointPath != "/" ||
		want.Server.UpdateInterval.Std() != 10*time.Second || want.Server.StaleDuration.Std() != time.Minute {
		t.Fatalf("unexpected server defaults: %+v", want.Server)
	}
	if len(want.Docker) != 0 || want.Dynamic != nil {
		t.Fatalf("unexpected defaults: docker=%+v dynamic=%+v", want.Docker, want.Dynamic)
	}

	d := DefaultDockerConfig()
	if d.Endpoint != "unix:///var/run/docker.sock" || d.Timeout.Std() != 5*time.Second ||
		d.DefaultRule != "Host(`{{ normalize .Name }}`)" || d.ExposedByDefault ||
		!d.Containers || d.Swarm || d.Discover || d.TLS != nil {
		t.Fatalf("unexpected docker provider defaults: %+v", d)
	}

	for name, data := range map[string]string{"empty": "", "comments only": "# nothing\n"} {
		t.Run(name, func(t *testing.T) {
			got, err := Parse([]byte(data))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Parse(%q) = %+v, want defaults", data, got)
			}
		})
	}
}

func TestSourceBooleans(t *testing.T) {
	tests := []struct {
		name         string
		yaml         string
		wantC, wantS bool
	}{
		{"empty provider block keeps defaults", "docker:\n  local: {}\n", true, false},
		{"null provider block keeps defaults", "docker:\n  local:\n", true, false},
		{"both omitted keeps defaults", "docker:\n  local:\n    endpoint: tcp://h:2375\n", true, false},
		{"containers only", "docker:\n  local:\n    containers: true\n    swarm: false\n", true, false},
		{"swarm only", "docker:\n  local:\n    containers: false\n    swarm: true\n", false, true},
		{"both enabled", "docker:\n  local:\n    containers: true\n    swarm: true\n", true, true},
		{"both disabled", "docker:\n  local:\n    containers: false\n    swarm: false\n", false, false},
		{"swarm enabled, containers omitted keeps default on", "docker:\n  local:\n    swarm: true\n", true, true},
		{"containers disabled, swarm omitted keeps default off", "docker:\n  local:\n    containers: false\n", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := cfg.Docker["local"]
			if got.Containers != tt.wantC {
				t.Errorf("Containers = %v, want %v", got.Containers, tt.wantC)
			}
			if got.Swarm != tt.wantS {
				t.Errorf("Swarm = %v, want %v", got.Swarm, tt.wantS)
			}
			if got.Timeout.Std() != 5*time.Second || got.DefaultRule != "Host(`{{ normalize .Name }}`)" {
				t.Errorf("provider defaults lost: %+v", got)
			}
		})
	}
}

func TestMultipleDockerProviders(t *testing.T) {
	doc := `
docker:
  swarm-mgr:
    containers: false
    swarm: true
    endpoint: tcp://socketproxy-mgr:2375
    network: traefik-public
  nodes:
    containers: true
    swarm: false
    endpoint: tcp://tasks.socketproxy:2375
    discover: true
    exposedByDefault: true
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Docker) != 2 {
		t.Fatalf("len(Docker) = %d, want 2", len(cfg.Docker))
	}
	mgr := cfg.Docker["swarm-mgr"]
	if mgr.Containers || !mgr.Swarm || mgr.Discover || mgr.Network != "traefik-public" || mgr.Timeout.Std() != 5*time.Second {
		t.Errorf("swarm-mgr = %+v", mgr)
	}
	nodes := cfg.Docker["nodes"]
	if !nodes.Containers || nodes.Swarm || !nodes.Discover || !nodes.ExposedByDefault || nodes.Endpoint != "tcp://tasks.socketproxy:2375" {
		t.Errorf("nodes = %+v", nodes)
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"seconds string", `"30s"`, 30 * time.Second, false},
		{"minutes string", `"2m"`, 2 * time.Minute, false},
		{"milliseconds string", `"500ms"`, 500 * time.Millisecond, false},
		{"plain integer is seconds", `5`, 5 * time.Second, false},
		{"quoted integer is seconds", `"7"`, 7 * time.Second, false},
		{"invalid string", `"soon"`, 0, true},
		{"unitless decimal", `1.5`, 0, true},
		{"negative is rejected by validation", `-5`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte("server:\n  updateInterval: " + tt.value + "\n"))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%s) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if err == nil && cfg.Server.UpdateInterval.Std() != tt.want {
				t.Errorf("UpdateInterval = %v, want %v", cfg.Server.UpdateInterval.Std(), tt.want)
			}
		})
	}
}

func TestDurationJSONRoundTrip(t *testing.T) {
	tests := []struct {
		in   string
		want Duration
	}{
		{`"45s"`, Duration(45 * time.Second)},
		{`90`, Duration(90 * time.Second)},
		{`"1m"`, Duration(time.Minute)},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var d Duration
			if err := json.Unmarshal([]byte(tt.in), &d); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if d != tt.want {
				t.Fatalf("Unmarshal(%s) = %v, want %v", tt.in, d, tt.want)
			}
			out, err := json.Marshal(d)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var back Duration
			if err := json.Unmarshal(out, &back); err != nil || back != d {
				t.Fatalf("round trip of %s: got %v, err %v", out, back, err)
			}
		})
	}
	var d Duration
	if err := json.Unmarshal([]byte(`"bogus"`), &d); err == nil {
		t.Error("Unmarshal of invalid duration succeeded")
	}
}

func TestDurationYAMLMarshal(t *testing.T) {
	out, err := yaml.Marshal(struct {
		D Duration `yaml:"d"`
	}{Duration(90 * time.Second)})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "d: 1m30s" {
		t.Errorf("Marshal = %q, want %q", got, "d: 1m30s")
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"negative update interval", "server:\n  updateInterval: -1s\n", "server.updateInterval"},
		{"zero stale duration", "server:\n  staleDuration: 0s\n", "server.staleDuration"},
		{"bad endpoint scheme", "docker:\n  p:\n    endpoint: ftp://host:21\n", "docker.p.endpoint"},
		{"endpoint without scheme", "docker:\n  p:\n    endpoint: /var/run/docker.sock\n", "docker.p.endpoint"},
		{"endpoint empty remainder", "docker:\n  p:\n    endpoint: tcp://\n", "docker.p.endpoint"},
		{"discover with unix socket", "docker:\n  p:\n    endpoint: unix:///var/run/docker.sock\n    discover: true\n", "docker.p.discover"},
		{"discover without hostname", "docker:\n  p:\n    endpoint: tcp://:2375\n    discover: true\n", "docker.p.endpoint"},
		{"cert without key", "docker:\n  p:\n    tls:\n      cert: /c.pem\n", "docker.p.tls.cert"},
		{"key without cert", "docker:\n  p:\n    tls:\n      key: /k.pem\n", "docker.p.tls.cert"},
		{"user without password", "docker:\n  p:\n    username: bob\n", "docker.p.username"},
		{"password without user", "docker:\n  p:\n    password: secret\n", "docker.p.username"},
		{"bad endpointPath", "server:\n  endpointPath: traefik\n", "server.endpointPath"},
		{"endpointPath reserved for healthz", "server:\n  endpointPath: /healthz\n", "server.endpointPath"},
		{"unknown provider field", "docker:\n  p:\n    mode: both\n", "mode"},
		{"legacy flat docker config rejected", "docker:\n  endpoint: tcp://h:2375\n", "docker provider configuration must be a mapping"},
		{"docker list rejected", "docker:\n  - endpoint: tcp://h:2375\n", "docker must be a map of named providers"},
		{"empty provider name", "docker:\n  \"\": {}\n", "provider name must not be empty"},
		{"unparsable default rule", "docker:\n  p:\n    defaultRule: 'Host(`{{ normalize .Name`)'\n", "docker.p.defaultRule"},
		{"unknown rule function", "docker:\n  p:\n    defaultRule: '{{ bogus .Name }}'\n", "docker.p.defaultRule"},
		{"zero docker timeout", "docker:\n  p:\n    timeout: 0s\n", "docker.p.timeout"},
		{"unknown field", "server:\n  bogus: 1\n", "bogus"},
		{"unknown top-level field", "serverr:\n  listenAddr: \":1\"\n", "serverr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("Parse succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestDynamicSection(t *testing.T) {
	doc := `
dynamic:
  http:
    routers:
      app:
        rule: "Host(` + "`app.example.com`" + `)"
        service: app
        middlewares: [strip]
        tls:
          options: modern
    services:
      app:
        loadBalancer:
          servers:
            - url: "http://192.168.1.10:8080"
    middlewares:
      strip:
        stripPrefix:
          prefixes: ["/api"]
  tls:
    certificates:
      - certFile: /certs/a.pem
        keyFile: /certs/a-key.pem
    options:
      modern:
        minVersion: VersionTLS12
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cfg.Dynamic
	if d == nil || d.HTTP == nil || d.TLS == nil {
		t.Fatalf("Dynamic = %+v, want http and tls sections", d)
	}
	if r := d.HTTP.Routers["app"]; r == nil || r.Service != "app" || r.Rule != "Host(`app.example.com`)" || r.TLS == nil || r.TLS.Options != "modern" {
		t.Errorf("router app = %+v", r)
	}
	if s := d.HTTP.Services["app"]; s == nil || s.LoadBalancer == nil || len(s.LoadBalancer.Servers) != 1 || s.LoadBalancer.Servers[0].URL != "http://192.168.1.10:8080" {
		t.Errorf("service app = %+v", s)
	}
	if m := d.HTTP.Middlewares["strip"]; m == nil || m.StripPrefix == nil || m.StripPrefix.Prefixes[0] != "/api" {
		t.Errorf("middleware strip = %+v", m)
	}
	if len(d.TLS.Certificates) != 1 || d.TLS.Options["modern"].MinVersion != "VersionTLS12" {
		t.Errorf("tls = %+v", d.TLS)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "detour.yaml")
	if err := os.WriteFile(path, []byte("server:\n  listenAddr: \":9000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Server.ListenAddr != ":9000" || cfg.Server.EndpointPath != "/" {
		t.Errorf("LoadFile = %+v", cfg.Server)
	}

	missing := filepath.Join(dir, "missing.yaml")
	_, err = LoadFile(missing)
	if err == nil || !strings.Contains(err.Error(), missing) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("LoadFile(missing) error = %v, want wrapped not-exist error mentioning path", err)
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("server:\n  staleDuration: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(bad); err == nil || !strings.Contains(err.Error(), bad) {
		t.Errorf("LoadFile(bad) error = %v, want error mentioning path", err)
	}
}

func TestExampleConfig(t *testing.T) {
	cfg, err := LoadFile("../../config.example.yaml")
	if err != nil {
		t.Fatalf("LoadFile(example): %v", err)
	}
	if _, ok := cfg.Docker["local"]; !ok {
		t.Fatalf("example docker.local provider missing: %+v", cfg.Docker)
	}
	if cfg.Dynamic == nil || cfg.Dynamic.HTTP == nil || cfg.Dynamic.HTTP.Routers["legacy-app"] == nil {
		t.Fatalf("example dynamic section missing: %+v", cfg.Dynamic)
	}
}
