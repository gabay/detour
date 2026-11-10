package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"detour/internal/builder"
	"detour/internal/compiler"
	"detour/internal/config"
	"detour/internal/docker"
	"detour/internal/server"
)

const invalidReloadMsg = "config reload failed, keeping previous configuration"

type trackedCollector struct {
	closed atomic.Bool
	calls  atomic.Int32
}

func (c *trackedCollector) Collect(context.Context) ([]docker.Container, error) {
	c.calls.Add(1)
	return nil, nil
}

func (c *trackedCollector) Close() error {
	c.closed.Store(true)
	return nil
}

// fakeFactory records every collector it builds; set err to make it fail.
type fakeFactory struct {
	mu    sync.Mutex
	err   error
	built []*trackedCollector
}

func (f *fakeFactory) factory(cfg *config.Config) (Components, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Components{}, f.err
	}
	b, err := builder.New(cfg.Docker)
	if err != nil {
		return Components{}, err
	}
	c := &trackedCollector{}
	f.built = append(f.built, c)
	return Components{Collector: c, Builder: b, Compiler: compiler.New(cfg.Dynamic)}, nil
}

func (f *fakeFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.built)
}

func mustParse(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newReloadApp(t *testing.T, yaml string, logOut io.Writer) (*App, *fakeFactory) {
	t.Helper()
	f := &fakeFactory{}
	logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, err := New("", mustParse(t, yaml), f.factory, &server.Store{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a, f
}

func TestReloadUnchangedIsNoop(t *testing.T) {
	a, f := newReloadApp(t, staticYAML, io.Discard)
	before := a.store.Get()
	if err := a.Reload(context.Background(), mustParse(t, staticYAML)); err != nil {
		t.Fatal(err)
	}
	if n := f.count(); n != 1 {
		t.Errorf("factory calls = %d, want 1", n)
	}
	if a.store.Get() != before {
		t.Error("unchanged config republished a snapshot")
	}
}

func TestReloadDockerSwapsCollector(t *testing.T) {
	a, f := newReloadApp(t, staticYAML, io.Discard)
	old := f.built[0]
	if err := a.Reload(context.Background(), mustParse(t, staticYAML+"docker:\n  network: other\n")); err != nil {
		t.Fatal(err)
	}
	if !old.closed.Load() {
		t.Error("old collector was not closed")
	}
	cur := f.built[len(f.built)-1]
	if a.comp.Collector.(*trackedCollector) != cur || cur.calls.Load() == 0 {
		t.Error("new collector is not in use after reload")
	}
}

func TestReloadDynamicPublishedImmediately(t *testing.T) {
	a, _ := newReloadApp(t, staticYAML, io.Discard)
	if err := a.Reload(context.Background(), mustParse(t, strings.Replace(staticYAML, "ext:", "ext2:", 1))); err != nil {
		t.Fatal(err)
	}
	conf := decodeServed(t, a.store.Get())
	if _, ok := conf.HTTP.Services["ext2"]; !ok {
		t.Errorf("reloaded service not published: %+v", conf.HTTP.Services)
	}
	if _, ok := conf.HTTP.Services["ext"]; ok {
		t.Error("old service still published")
	}
}

func TestReloadAppliesUpdateInterval(t *testing.T) {
	a, _ := newReloadApp(t, staticYAML, io.Discard)
	if err := a.Reload(context.Background(), mustParse(t, strings.Replace(staticYAML, "50ms", "2s", 1))); err != nil {
		t.Fatal(err)
	}
	if got := a.updateInterval(); got != 2*time.Second {
		t.Errorf("updateInterval = %v, want 2s", got)
	}
}

func TestReloadListenAddrWarnsAndIsNotApplied(t *testing.T) {
	var logs bytes.Buffer
	a, _ := newReloadApp(t, staticYAML, &logs)
	listen := a.listenAddr
	if err := a.Reload(context.Background(), mustParse(t, strings.Replace(staticYAML, "server:", "server:\n  listenAddr: \":9999\"", 1))); err != nil {
		t.Fatal(err)
	}
	if a.listenAddr != listen {
		t.Errorf("listenAddr applied: %s", a.listenAddr)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "restart required") {
		t.Errorf("missing restart warning in logs:\n%s", logs.String())
	}
}

func TestReloadFactoryErrorKeepsOldConfig(t *testing.T) {
	a, f := newReloadApp(t, staticYAML, io.Discard)
	old, oldCfg, oldSnap := f.built[0], a.cfg, a.store.Get()
	f.err = errors.New("docker down")
	if err := a.Reload(context.Background(), mustParse(t, staticYAML+"docker:\n  network: other\n")); err == nil {
		t.Fatal("Reload succeeded despite factory error")
	}
	if a.cfg != oldCfg || a.store.Get() != oldSnap || old.closed.Load() {
		t.Error("factory error changed the running configuration")
	}
}

func TestReloadFileInvalidKeepsPrevious(t *testing.T) {
	cases := map[string]string{
		"syntax":     "server: [",
		"validation": "server:\n  endpointPath: nope\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "detour.yaml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			a, _ := newReloadApp(t, staticYAML, &logs)
			a.configPath = path
			oldCfg, oldSnap := a.cfg, a.store.Get()
			a.reloadFile(context.Background())
			if a.cfg != oldCfg || a.store.Get() != oldSnap {
				t.Error("invalid file changed the running configuration")
			}
			if !strings.Contains(logs.String(), invalidReloadMsg) {
				t.Errorf("missing error log:\n%s", logs.String())
			}
		})
	}
}

func TestReloadConcurrentWithRefresh(t *testing.T) {
	a, _ := newReloadApp(t, staticYAML, io.Discard)
	variants := []*config.Config{
		mustParse(t, staticYAML),
		mustParse(t, strings.Replace(staticYAML, "ext:", "ext2:", 1)),
		mustParse(t, staticYAML+"docker:\n  network: other\n"),
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := range 30 {
				if err := a.Reload(ctx, variants[i%len(variants)]); err != nil {
					t.Errorf("Reload: %v", err)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 30 {
				_ = a.Refresh(ctx)
				a.RequestReload()
			}
		}()
	}
	wg.Wait()
}
