package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"detour/internal/config"
)

func TestBuildComponentsMultipleProvidersAndDiscover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.47")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			_, _ = w.Write([]byte("OK"))
		case strings.HasSuffix(r.URL.Path, "/version"):
			_, _ = w.Write([]byte(`{"Version":"27.0.0","ApiVersion":"1.47","MinAPIVersion":"1.24"}`))
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tcpEndpoint := "tcp" + strings.TrimPrefix(srv.URL, "http")
	doc := `
docker:
  disabled:
    containers: false
    swarm: false
  direct:
    endpoint: ` + tcpEndpoint + `
  discovered:
    endpoint: ` + tcpEndpoint + `
    discover: true
`
	cfg, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	comp, err := buildComponents(cfg, logger)
	if err != nil {
		t.Fatalf("buildComponents: %v", err)
	}
	defer func() {
		for _, p := range comp.Providers {
			if c, ok := p.Collector.(io.Closer); ok {
				_ = c.Close()
			}
		}
	}()

	if len(comp.Providers) != 3 {
		t.Fatalf("len(Providers) = %d, want 3", len(comp.Providers))
	}
	for _, p := range comp.Providers {
		if _, err := p.Collector.Collect(context.Background()); err != nil {
			t.Errorf("provider %s Collect: %v", p.Name, err)
		}
	}
}
