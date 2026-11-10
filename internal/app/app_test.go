package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
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
	b, err := builder.New(cfg.Docker)
	if err != nil {
		t.Fatal(err)
	}
	store := &server.Store{}
	logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return New(cfg, coll, b, compiler.New(cfg.Dynamic), store, logger), store
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
	cfgYAML := "docker:\n  containers: false\n  swarm: false\n" + staticYAML
	a, store := newTestApp(t, cfgYAML, NopCollector{}, io.Discard)
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	conf := decodeServed(t, store.Get())
	if _, ok := conf.HTTP.Services["ext"]; !ok || len(conf.HTTP.Routers) != 0 {
		t.Errorf("unexpected config: %+v", conf.HTTP)
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
