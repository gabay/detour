package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
	if want.Docker.Endpoint != "unix:///var/run/docker.sock" || want.Docker.Timeout.Std() != 5*time.Second ||
		want.Docker.DefaultRule != "Host(`{{ normalize .Name }}`)" || want.Docker.ExposedByDefault ||
		want.Dynamic != nil || want.Docker.TLS != nil {
		t.Fatalf("unexpected docker defaults: %+v", want.Docker)
	}

	for name, data := range map[string]string{"empty": "", "comments only": "# nothing\n"} {
		t.Run(name, func(t *testing.T) {
			got, err := Parse([]byte(data))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if *got != *want {
				t.Errorf("Parse(%q) = %+v, want defaults", data, got)
			}
		})
	}
}

func TestModeResolution(t *testing.T) {
	tests := []struct {
		name         string
		yaml         string
		wantC, wantS bool
	}{
		{"empty document", "", true, false},
		{"mode docker", "docker:\n  mode: docker\n", true, false},
		{"mode swarm", "docker:\n  mode: swarm\n", false, true},
		{"mode both", "docker:\n  mode: both\n", true, true},
		{"mode off", "docker:\n  mode: off\n", false, false},
		{"empty mode is docker", "docker:\n  mode: \"\"\n", true, false},
		{"swarm true, no mode keeps docker baseline", "docker:\n  swarm: true\n", true, true},
		{"off with containers override", "docker:\n  mode: off\n  containers: true\n", true, false},
		{"off with swarm override", "docker:\n  mode: off\n  swarm: true\n", false, true},
		{"docker with swarm override", "docker:\n  mode: docker\n  swarm: true\n", true, true},
		{"both with containers disabled", "docker:\n  mode: both\n  containers: false\n", false, true},
		{"swarm with containers forced on, swarm off (contradiction)", "docker:\n  mode: swarm\n  containers: true\n  swarm: false\n", true, false},
		{"docker with containers off and swarm off", "docker:\n  mode: docker\n  containers: false\n  swarm: false\n", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := cfg.Docker.ContainersEnabled(); got != tt.wantC {
				t.Errorf("ContainersEnabled() = %v, want %v", got, tt.wantC)
			}
			if got := cfg.Docker.SwarmEnabled(); got != tt.wantS {
				t.Errorf("SwarmEnabled() = %v, want %v", got, tt.wantS)
			}
		})
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
		{"bad endpoint scheme", "docker:\n  endpoint: ftp://host:21\n", "docker.endpoint"},
		{"endpoint without scheme", "docker:\n  endpoint: /var/run/docker.sock\n", "docker.endpoint"},
		{"endpoint empty remainder", "docker:\n  endpoint: tcp://\n", "docker.endpoint"},
		{"cert without key", "docker:\n  tls:\n    cert: /c.pem\n", "docker.tls.cert"},
		{"key without cert", "docker:\n  tls:\n    key: /k.pem\n", "docker.tls.cert"},
		{"user without password", "docker:\n  username: bob\n", "docker.username"},
		{"password without user", "docker:\n  password: secret\n", "docker.username"},
		{"bad endpointPath", "server:\n  endpointPath: traefik\n", "server.endpointPath"},
		{"endpointPath reserved for healthz", "server:\n  endpointPath: /healthz\n", "server.endpointPath"},
		{"unknown mode", "docker:\n  mode: kubernetes\n", "docker.mode"},
		{"unparsable default rule", "docker:\n  defaultRule: 'Host(`{{ normalize .Name`)'\n", "docker.defaultRule"},
		{"unknown rule function", "docker:\n  defaultRule: '{{ bogus .Name }}'\n", "docker.defaultRule"},
		{"zero docker timeout", "docker:\n  timeout: 0s\n", "docker.timeout"},
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
	if cfg.Dynamic == nil || cfg.Dynamic.HTTP == nil || cfg.Dynamic.HTTP.Routers["legacy-app"] == nil {
		t.Fatalf("example dynamic section missing: %+v", cfg.Dynamic)
	}
}
