package docker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"detour/internal/config"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

type closableFakeAPI struct {
	*fakeAPI
	closed atomic.Bool
}

func (c *closableFakeAPI) Close() error {
	c.closed.Store(true)
	return nil
}

func TestDiscoveringCollectorFanOutAndCache(t *testing.T) {
	var (
		mu       sync.Mutex
		dnsIPs   = []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("10.0.0.2")}}
		created  = map[string]*closableFakeAPI{}
		srvNames = map[string]string{}
	)

	daemons := map[string]*fakeAPI{
		"tcp://10.0.0.1:2375": newFake(),
		"tcp://10.0.0.2:2375": newFake(),
		"tcp://10.0.0.3:2375": newFake(),
	}
	daemons["tcp://10.0.0.1:2375"].containers = []container.Summary{{ID: "c1"}, {ID: "shared"}}
	daemons["tcp://10.0.0.1:2375"].inspect = map[string]container.InspectResponse{
		"c1":     ctrInspect("c1", "web-1", "running", map[string]string{"traefik.enable": "true"}),
		"shared": ctrInspect("shared", "shared", "running", map[string]string{"traefik.enable": "true"}),
	}
	daemons["tcp://10.0.0.2:2375"].containers = []container.Summary{{ID: "c2"}, {ID: "shared"}}
	daemons["tcp://10.0.0.2:2375"].inspect = map[string]container.InspectResponse{
		"c2":     ctrInspect("c2", "web-2", "running", map[string]string{"traefik.enable": "true"}),
		"shared": ctrInspect("shared", "shared", "running", map[string]string{"traefik.enable": "true"}),
	}
	daemons["tcp://10.0.0.3:2375"].containers = []container.Summary{{ID: "c3"}}
	daemons["tcp://10.0.0.3:2375"].inspect = map[string]container.InspectResponse{
		"c3": ctrInspect("c3", "web-3", "running", map[string]string{"traefik.enable": "true"}),
	}

	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "tasks.socketproxy" {
			t.Errorf("lookup host = %q, want tasks.socketproxy", host)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]net.IPAddr(nil), dnsIPs...), nil
	}
	makeAPI := func(cfg config.DockerConfig, serverName string) (API, error) {
		mu.Lock()
		defer mu.Unlock()
		f, ok := daemons[cfg.Endpoint]
		if !ok {
			return nil, errors.New("unexpected endpoint " + cfg.Endpoint)
		}
		c := &closableFakeAPI{fakeAPI: f}
		created[cfg.Endpoint] = c
		srvNames[cfg.Endpoint] = serverName
		return c, nil
	}

	cfg := config.DefaultDockerConfig()
	cfg.Endpoint = "tcp://tasks.socketproxy:2375"
	cfg.Discover = true

	coll, err := NewDiscoveringCollector("nodes", cfg, lookup, makeAPI, nil)
	if err != nil {
		t.Fatalf("NewDiscoveringCollector: %v", err)
	}

	// First poll resolves 10.0.0.1 and 10.0.0.2, deduplicates "shared".
	got, err := coll.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect #1: %v", err)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if want := []string{"shared", "c1", "c2"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("Collect #1 ids = %v, want %v", ids, want)
	}
	if srvNames["tcp://10.0.0.1:2375"] != "tasks.socketproxy" {
		t.Errorf("serverName = %q, want tasks.socketproxy", srvNames["tcp://10.0.0.1:2375"])
	}
	firstClient1 := created["tcp://10.0.0.1:2375"]
	firstClient2 := created["tcp://10.0.0.2:2375"]

	// Second poll: 10.0.0.2 disappears, 10.0.0.3 appears; 10.0.0.1 client is reused and 10.0.0.2 closed.
	mu.Lock()
	dnsIPs = []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("10.0.0.3")}, {IP: net.ParseIP("10.0.0.1")}}
	mu.Unlock()

	got, err = coll.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect #2: %v", err)
	}
	ids = nil
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if want := []string{"shared", "c1", "c3"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("Collect #2 ids = %v, want %v", ids, want)
	}
	if created["tcp://10.0.0.1:2375"] != firstClient1 || firstClient1.closed.Load() {
		t.Error("client for 10.0.0.1 was recreated or closed")
	}
	if !firstClient2.closed.Load() {
		t.Error("stale client for 10.0.0.2 was not closed")
	}

	if err := coll.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !firstClient1.closed.Load() || !created["tcp://10.0.0.3:2375"].closed.Load() {
		t.Error("Close did not close remaining cached clients")
	}
}

func TestDiscoveringCollectorFailuresLogWarnings(t *testing.T) {
	cfg := config.DefaultDockerConfig()
	cfg.Endpoint = "tcp://tasks.socketproxy:2375"
	cfg.Discover = true

	t.Run("dns error", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		coll, err := NewDiscoveringCollector("nodes", cfg, func(context.Context, string) ([]net.IPAddr, error) {
			return nil, errors.New("nxdomain")
		}, nil, logger)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := coll.Collect(context.Background()); err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "provider=nodes") {
			t.Errorf("missing warning log: %s", logs.String())
		}
	})

	t.Run("empty dns answer", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		coll, err := NewDiscoveringCollector("nodes", cfg, func(context.Context, string) ([]net.IPAddr, error) {
			return nil, nil
		}, nil, logger)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := coll.Collect(context.Background()); err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "no addresses") {
			t.Errorf("missing warning log: %s", logs.String())
		}
	})

	t.Run("partial target failure fails poll and logs warning", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		good := newFake()
		bad := newFake()
		bad.containerErr = errors.New("connection refused")

		coll, err := NewDiscoveringCollector("nodes", cfg,
			func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("10.0.0.2")}}, nil
			},
			func(c config.DockerConfig, _ string) (API, error) {
				if strings.Contains(c.Endpoint, "10.0.0.1") {
					return good, nil
				}
				return bad, nil
			},
			logger,
		)
		if err != nil {
			t.Fatal(err)
		}
		got, err := coll.Collect(context.Background())
		if err == nil || got != nil {
			t.Fatalf("want error and nil slice, got %v, %v", got, err)
		}
		if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "10.0.0.2:2375") {
			t.Errorf("missing endpoint warning in logs: %s", logs.String())
		}
	})
}

func TestDiscoveringCollectorIPv6AndDefaultPorts(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	cfg := config.DefaultDockerConfig()
	cfg.Endpoint = "https://tasks.socketproxy"
	cfg.Discover = true

	coll, err := NewDiscoveringCollector("v6", cfg,
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("fd00::1")}}, nil
		},
		func(c config.DockerConfig, _ string) (API, error) {
			mu.Lock()
			seen = append(seen, c.Endpoint)
			mu.Unlock()
			f := newFake()
			f.networks = []network.Summary{}
			return f, nil
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coll.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://[fd00::1]:2376"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("endpoints = %v, want %v", seen, want)
	}
}
