package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"detour/internal/config"
	"detour/internal/dynamic"
	"detour/internal/server"
	"detour/internal/watch"

	"gopkg.in/yaml.v3"
)

// syncBuffer is a log sink that is safe to read while the poll loop writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// routerYAML returns a config with one router per name, all sending to one static service.
func routerYAML(names ...string) string {
	var b strings.Builder
	b.WriteString("server:\n  updateInterval: 50ms\n  staleDuration: 1m\ndynamic:\n  http:\n    routers:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "      %s:\n        rule: 'Path(\"/%s\")'\n        service: ext\n", n, n)
	}
	b.WriteString("    services:\n      ext:\n        loadBalancer:\n          servers:\n            - url: http://192.0.2.1:80\n")
	return b.String()
}

// hasRouter reports whether the endpoint at url serves a configuration containing router name.
func hasRouter(url, name string) (bool, error) {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var conf dynamic.Configuration
	if err := yaml.Unmarshal(body, &conf); err != nil {
		return false, err
	}
	_, ok := conf.HTTP.Routers[name]
	return ok, nil
}

func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFileReloadEndToEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "detour.yaml")
	writeConfig(t, path, routerYAML("first"))
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	a, err := New(path, cfg, fixedFactory(&fakeCollector{}), &server.Store{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- a.Serve(ctx, ln) }()
	watchDone := make(chan error, 1)
	go func() { watchDone <- watch.Watch(ctx, path, 100*time.Millisecond, a.RequestReload) }()

	url := "http://" + ln.Addr().String() + "/"
	waitUntil(t, 5*time.Second, "initial router", func() bool {
		ok, _ := hasRouter(url, "first")
		return ok
	})

	// Rewrite (at most every 500ms, so the debounce can fire) until the watcher is armed and the router is served.
	writeConfig(t, path, routerYAML("first", "second"))
	lastWrite := time.Now()
	waitUntil(t, 5*time.Second, "reloaded router", func() bool {
		if time.Since(lastWrite) > 500*time.Millisecond {
			writeConfig(t, path, routerYAML("first", "second"))
			lastWrite = time.Now()
		}
		ok, _ := hasRouter(url, "second")
		return ok
	})

	writeConfig(t, path, "server: [")
	waitUntil(t, 5*time.Second, "reload error log", func() bool {
		return strings.Contains(logs.String(), invalidReloadMsg)
	})
	if ok, err := hasRouter(url, "second"); err != nil || !ok {
		t.Fatalf("after invalid file: router second served=%v err=%v", ok, err)
	}

	cancel()
	for name, ch := range map[string]chan error{"Serve": served, "Watch": watchDone} {
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("%s returned %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s did not return after cancel", name)
		}
	}
}
