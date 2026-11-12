package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"detour/internal/builder"
	"detour/internal/compiler"
	"detour/internal/config"
	"detour/internal/docker"
	"detour/internal/dynamic"
	"detour/internal/server"

	"gopkg.in/yaml.v3"
)

const staticYAML = `
server:
  updateInterval: 50ms
  staleDuration: 1s
docker:
  local: {}
dynamic:
  http:
    services:
      ext:
        loadBalancer:
          servers:
            - url: http://192.0.2.1:80
`

type fakeCollector struct {
	mu         sync.Mutex
	containers []docker.Container
	err        error
}

func (f *fakeCollector) Collect(context.Context) ([]docker.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.containers, f.err
}

func (f *fakeCollector) set(containers []docker.Container, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers, f.err = containers, err
}

func webContainer() docker.Container {
	return docker.Container{
		ID: "web", Name: "web", ServiceName: "web", Source: docker.SourceContainer, Status: "running",
		Labels:   map[string]string{"traefik.enable": "true"},
		Networks: map[string]docker.Network{"bridge": {Name: "bridge", Addr: "10.0.0.2"}},
		Ports:    []docker.Port{{Port: 8080, Proto: "tcp"}},
	}
}

func newTestApp(t *testing.T, cfgYAML string, coll Collector, logOut io.Writer) (*App, *server.Store) {
	t.Helper()
	cfg, err := config.Parse([]byte(cfgYAML))
	if err != nil {
		t.Fatal(err)
	}
	store := &server.Store{}
	logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, err := New("", cfg, fixedFactory(coll), store, logger)
	if err != nil {
		t.Fatal(err)
	}
	return a, store
}

func fixedFactory(coll Collector) Factory {
	return func(cfg *config.Config, _ *slog.Logger) (Components, error) {
		var providers []Provider
		for _, name := range slices.Sorted(maps.Keys(cfg.Docker)) {
			b, err := builder.New(cfg.Docker[name])
			if err != nil {
				return Components{}, err
			}
			providers = append(providers, Provider{Name: name, Collector: coll, Builder: b})
		}
		return Components{Providers: providers, Compiler: compiler.New(cfg.Dynamic)}, nil
	}
}

func decodeServed(t *testing.T, sn *server.Snapshot) *dynamic.Configuration {
	t.Helper()
	var conf dynamic.Configuration
	if err := yaml.Unmarshal(sn.YAML, &conf); err != nil {
		t.Fatalf("decode served yaml: %v\n%s", err, sn.YAML)
	}
	return &conf
}

func TestRefreshMergesStaticAndDiscovered(t *testing.T) {
	coll := &fakeCollector{containers: []docker.Container{webContainer()}}
	a, store := newTestApp(t, staticYAML, coll, io.Discard)
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	conf := decodeServed(t, store.Get())
	if _, ok := conf.HTTP.Services["ext"]; !ok {
		t.Error("static service ext missing")
	}
	if r, ok := conf.HTTP.Routers["web"]; !ok || r.Service != "web" {
		t.Errorf("discovered router web = %+v", r)
	}
}

func TestRefreshBothOffPublishesStaticOnly(t *testing.T) {
	cfgYAML := strings.Replace(staticYAML, "  local: {}", "  local:\n    containers: false\n    swarm: false", 1)
	a, store := newTestApp(t, cfgYAML, NopCollector{}, io.Discard)
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	conf := decodeServed(t, store.Get())
	if _, ok := conf.HTTP.Services["ext"]; !ok || len(conf.HTTP.Routers) != 0 {
		t.Errorf("unexpected config: %+v", conf.HTTP)
	}
}

func TestRefreshNoProvidersPublishesStaticOnly(t *testing.T) {
	cfgYAML := strings.Replace(staticYAML, "docker:\n  local: {}\n", "", 1)
	a, store := newTestApp(t, cfgYAML, NopCollector{}, io.Discard)
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	conf := decodeServed(t, store.Get())
	if _, ok := conf.HTTP.Services["ext"]; !ok || len(conf.HTTP.Routers) != 0 {
		t.Errorf("unexpected config: %+v", conf.HTTP)
	}
}

func TestRefreshMultipleProvidersMergeAndRespectOwnConfig(t *testing.T) {
	doc := `
server:
  updateInterval: 50ms
  staleDuration: 1s
docker:
  edge:
    defaultRule: "Host(` + "`{{ normalize .Name }}.edge.test`" + `)"
    exposedByDefault: true
  internal:
    defaultRule: "Host(` + "`{{ normalize .Name }}.int.test`" + `)"
    exposedByDefault: false
`
	cfg, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	collectors := map[string]Collector{
		"edge": &fakeCollector{containers: []docker.Container{
			{
				ID: "e1", Name: "api", ServiceName: "api", Source: docker.SourceContainer, Status: "running",
				Networks: map[string]docker.Network{"bridge": {Name: "bridge", Addr: "10.1.0.2"}},
				Ports:    []docker.Port{{Port: 8080, Proto: "tcp"}},
			},
			{
				ID: "s1", Name: "shared", ServiceName: "shared", Source: docker.SourceContainer, Status: "running",
				Labels:   map[string]string{"traefik.enable": "true", "traefik.http.routers.shared.rule": "Host(`shared.test`)"},
				Networks: map[string]docker.Network{"bridge": {Name: "bridge", Addr: "10.1.0.9"}},
				Ports:    []docker.Port{{Port: 80, Proto: "tcp"}},
			},
		}},
		"internal": &fakeCollector{containers: []docker.Container{
			{
				// Unlabelled: must be skipped because internal.exposedByDefault is false.
				ID: "i-skip", Name: "secret", ServiceName: "secret", Source: docker.SourceContainer, Status: "running",
				Networks: map[string]docker.Network{"bridge": {Name: "bridge", Addr: "10.2.0.5"}},
				Ports:    []docker.Port{{Port: 8080, Proto: "tcp"}},
			},
			{
				ID: "i1", Name: "admin", ServiceName: "admin", Source: docker.SourceContainer, Status: "running",
				Labels:   map[string]string{"traefik.enable": "true"},
				Networks: map[string]docker.Network{"bridge": {Name: "bridge", Addr: "10.2.0.2"}},
				Ports:    []docker.Port{{Port: 9000, Proto: "tcp"}},
			},
			{
				ID: "s2", Name: "shared", ServiceName: "shared", Source: docker.SourceContainer, Status: "running",
				Labels:   map[string]string{"traefik.enable": "true", "traefik.http.routers.shared.rule": "Host(`shared.test`)"},
				Networks: map[string]docker.Network{"bridge": {Name: "bridge", Addr: "10.2.0.9"}},
				Ports:    []docker.Port{{Port: 80, Proto: "tcp"}},
			},
		}},
	}
	factory := func(c *config.Config, _ *slog.Logger) (Components, error) {
		var providers []Provider
		for _, name := range slices.Sorted(maps.Keys(c.Docker)) {
			b, err := builder.New(c.Docker[name])
			if err != nil {
				return Components{}, err
			}
			providers = append(providers, Provider{Name: name, Collector: collectors[name], Builder: b})
		}
		return Components{Providers: providers, Compiler: compiler.New(c.Dynamic)}, nil
	}

	store := &server.Store{}
	a, err := New("", cfg, factory, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	conf := decodeServed(t, store.Get())
	if r := conf.HTTP.Routers["api"]; r == nil || r.Rule != "Host(`api.edge.test`)" {
		t.Errorf("edge router api = %+v", r)
	}
	if r := conf.HTTP.Routers["admin"]; r == nil || r.Rule != "Host(`admin.int.test`)" {
		t.Errorf("internal router admin = %+v", r)
	}
	if _, ok := conf.HTTP.Routers["secret"]; ok {
		t.Error("secret container should not be exposed on internal provider")
	}
	if svc := conf.HTTP.Services["shared"]; svc == nil || svc.LoadBalancer == nil || len(svc.LoadBalancer.Servers) != 2 {
		t.Errorf("shared service servers = %+v, want 2 merged servers", svc)
	}
}

func TestRefreshErrorAndRecovery(t *testing.T) {
	coll := &fakeCollector{containers: []docker.Container{webContainer()}}
	var logs bytes.Buffer
	a, store := newTestApp(t, staticYAML, coll, &logs)
	ctx := context.Background()
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	good := store.Get()

	coll.set(nil, errors.New("docker unreachable"))
	for range 3 {
		if err := a.Refresh(ctx); err == nil || !strings.Contains(err.Error(), "docker unreachable") {
			t.Fatalf("Refresh error = %v", err)
		}
	}
	if store.Get() != good {
		t.Error("store changed after collector error")
	}
	if n := strings.Count(logs.String(), "level=ERROR"); n != 1 {
		t.Errorf("error lines = %d, want 1 (deduplicated):\n%s", n, logs.String())
	}

	coll.set([]docker.Container{webContainer()}, nil)
	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if store.Get() == good {
		t.Error("store not updated after recovery")
	}
	if !strings.Contains(logs.String(), "recovered") {
		t.Errorf("missing recovery log:\n%s", logs.String())
	}
}

func TestStaleWithFakeClock(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	coll := &fakeCollector{containers: []docker.Container{webContainer()}}
	a, store := newTestApp(t, staticYAML, coll, io.Discard)
	now := t0
	a.now = func() time.Time { return now }
	h := server.New(store, a.cfg.Server, a.now, nil).Handler()
	ctx := context.Background()

	if err := a.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if code := statusOf(h); code != http.StatusOK {
		t.Fatalf("fresh status = %d", code)
	}
	coll.set(nil, errors.New("docker down"))
	now = t0.Add(2 * time.Second) // staleDuration is 1s in staticYAML
	_ = a.Refresh(ctx)
	if code := statusOf(h); code != http.StatusServiceUnavailable {
		t.Errorf("stale status = %d, want 503", code)
	}
}

// statusOf issues a GET / against h and returns the status code.
func statusOf(h http.Handler) int {
	rec := newRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	return rec.code
}

// TestServeEndToEnd runs the real listener and poll loop, checks that the served
// YAML and JSON decode to the same configuration, and that shutdown is clean.
func TestServeEndToEnd(t *testing.T) {
	coll := &fakeCollector{containers: []docker.Container{webContainer()}}
	a, _ := newTestApp(t, staticYAML, coll, io.Discard)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	waitStatus(t, base+"/healthz", http.StatusOK)
	yamlBody := getBody(t, base+"/")
	jsonBody := getBody(t, base+"/?format=json")

	var fromYAML, fromJSON dynamic.Configuration
	if err := yaml.Unmarshal(yamlBody, &fromYAML); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(jsonBody, &fromJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&fromYAML, &fromJSON) {
		t.Error("YAML and JSON responses decode to different configurations")
	}
	if _, ok := fromYAML.HTTP.Routers["web"]; !ok {
		t.Errorf("served config lacks discovered router: %s", yamlBody)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func waitStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s never returned %d (last err %v)", url, want, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func getBody(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, err %v", url, resp.StatusCode, err)
	}
	return b
}

type recorder struct {
	header http.Header
	code   int
}

func newRecorder() *recorder                    { return &recorder{header: http.Header{}, code: http.StatusOK} }
func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *recorder) WriteHeader(code int)        { r.code = code }
