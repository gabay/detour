package docker

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"detour/internal/config"

	"github.com/moby/moby/client"
)

func TestEndpointURL(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		tls        *config.TLSClientConfig
		wantHost   string
		wantScheme string
		wantErr    bool
	}{
		{name: "unix socket", endpoint: "unix:///var/run/docker.sock", wantHost: "unix:///var/run/docker.sock", wantScheme: "http"},
		{name: "tcp plain", endpoint: "tcp://socket-proxy:2375", wantHost: "http://socket-proxy:2375", wantScheme: "http"},
		{name: "tcp with tls uses https", endpoint: "tcp://engine:2376", tls: &config.TLSClientConfig{CA: "ca.pem"}, wantHost: "https://engine:2376", wantScheme: "https"},
		{name: "http", endpoint: "http://engine:2375", wantHost: "http://engine:2375", wantScheme: "http"},
		{name: "https", endpoint: "https://engine:2376", wantHost: "https://engine:2376", wantScheme: "https"},
		{name: "missing address", endpoint: "tcp://", wantErr: true},
		{name: "no scheme", endpoint: "/var/run/docker.sock", wantErr: true},
		{name: "unsupported scheme", endpoint: "ssh://host", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, scheme, err := endpointURL(config.DockerConfig{Endpoint: tt.endpoint, TLS: tt.tls})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if host != tt.wantHost || scheme != tt.wantScheme {
				t.Errorf("got (%q, %q), want (%q, %q)", host, scheme, tt.wantHost, tt.wantScheme)
			}
		})
	}
}

func TestNewClientTLSErrors(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		tls  config.TLSClientConfig
	}{
		{"missing CA file", config.TLSClientConfig{CA: filepath.Join(dir, "nope.pem")}},
		{"CA without PEM certificates", config.TLSClientConfig{CA: garbage}},
		{"missing client key pair", config.TLSClientConfig{Cert: filepath.Join(dir, "c.pem"), Key: filepath.Join(dir, "k.pem")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(config.DockerConfig{Endpoint: "tcp://engine:2376", Timeout: config.Duration(time.Second), TLS: &tt.tls})
			if err == nil {
				t.Fatal("want error")
			}
		})
	}
}

// fakeDaemon serves the few Engine endpoints the client touches and records
// the headers of the last request.
func fakeDaemon(t *testing.T, lastHeader *http.Header) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	handle := func(w http.ResponseWriter, r *http.Request) {
		*lastHeader = r.Header.Clone()
		w.Header().Set("API-Version", "1.47")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			_, _ = w.Write([]byte("OK"))
		case strings.HasSuffix(r.URL.Path, "/version"):
			_, _ = w.Write([]byte(`{"Version":"27.0.0","ApiVersion":"1.47","MinAPIVersion":"1.24"}`))
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte(`[{"Id":"abc123","Names":["/web"],"State":"running"}]`))
		default:
			http.NotFound(w, r)
		}
	}
	mux.HandleFunc("/", handle)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNewClientAgainstFakeDaemon(t *testing.T) {
	var hdr http.Header
	srv := fakeDaemon(t, &hdr)
	cfg := config.DockerConfig{
		Endpoint: "tcp" + strings.TrimPrefix(srv.URL, "http"),
		Timeout:  config.Duration(5 * time.Second),
		Username: "detour",
		Password: "s3cret",
	}
	cli, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cli.Close()

	ctx := context.Background()
	ver, err := cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		t.Fatalf("ServerVersion: %v", err)
	}
	if ver.Version != "27.0.0" {
		t.Errorf("version = %q", ver.Version)
	}

	list, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		t.Fatalf("ContainerList: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != "abc123" {
		t.Errorf("items = %+v", list.Items)
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("detour:s3cret"))
	if got := hdr.Get("Authorization"); got != wantAuth {
		t.Errorf("Authorization = %q, want %q", got, wantAuth)
	}
	if got := hdr.Get("User-Agent"); got != "Detour/"+Version {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestNewClientNoAuthHeaderWithoutCredentials(t *testing.T) {
	var hdr http.Header
	srv := fakeDaemon(t, &hdr)
	cli, err := NewClient(config.DockerConfig{Endpoint: srv.URL, Timeout: config.Duration(time.Second)})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cli.Close()
	if _, err := cli.ServerVersion(context.Background(), client.ServerVersionOptions{}); err != nil {
		t.Fatalf("ServerVersion: %v", err)
	}
	if got := hdr.Get("Authorization"); got != "" {
		t.Errorf("unexpected Authorization header %q", got)
	}
}
