package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"detour/internal/config"
	"detour/internal/dynamic"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func testConf() *dynamic.Configuration {
	return &dynamic.Configuration{HTTP: &dynamic.HTTPConfiguration{
		Routers: map[string]*dynamic.Router{"web": {Rule: "Host(`a.example`)", Service: "web"}},
		Services: map[string]*dynamic.Service{"web": {LoadBalancer: &dynamic.ServersLoadBalancer{
			Servers: []dynamic.Server{{URL: "http://10.0.0.1:80"}},
		}}},
	}}
}

// fixture returns a handler backed by a store. If publish is true, a snapshot
// taken at t0 is stored.
func fixture(t *testing.T, endpoint string, publish bool) (http.Handler, *Store, *clock) {
	t.Helper()
	clk := &clock{t: t0}
	store := &Store{}
	if publish {
		sn, err := NewSnapshot(testConf(), t0)
		if err != nil {
			t.Fatal(err)
		}
		store.Set(sn)
	}
	cfg := config.ServerConfig{
		EndpointPath:   endpoint,
		UpdateInterval: config.Duration(10 * time.Second),
		StaleDuration:  config.Duration(time.Minute),
	}
	return New(store, cfg, clk.now, nil).Handler(), store, clk
}

func do(t *testing.T, h http.Handler, method, target string, hdr map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func body(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFormatSelection(t *testing.T) {
	tests := []struct {
		name   string
		target string
		accept string
		status int
		ctype  string
		json   bool
	}{
		{"default yaml", "/", "", 200, "application/yaml; charset=utf-8", false},
		{"query json", "/?format=json", "", 200, "application/json; charset=utf-8", true},
		{"query yaml", "/?format=yaml", "", 200, "application/yaml; charset=utf-8", false},
		{"accept json", "/", "application/json", 200, "application/json; charset=utf-8", true},
		{"accept json with q", "/", "text/html, application/json;q=0.9", 200, "application/json; charset=utf-8", true},
		{"accept yaml preferred", "/", "application/json;q=0.5, application/yaml", 200, "application/yaml; charset=utf-8", false},
		{"query beats accept", "/?format=yaml", "application/json", 200, "application/yaml; charset=utf-8", false},
		{"unknown format", "/?format=xml", "", 400, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store, _ := fixture(t, "/", true)
			resp := do(t, h, "GET", tt.target, map[string]string{"Accept": tt.accept})
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if tt.status != 200 {
				return
			}
			if got := resp.Header.Get("Content-Type"); got != tt.ctype {
				t.Errorf("Content-Type = %q, want %q", got, tt.ctype)
			}
			want := store.Get().YAML
			if tt.json {
				want = store.Get().JSON
			}
			if got := body(t, resp); !bytes.Equal(got, want) {
				t.Errorf("body mismatch:\n%s", got)
			}
		})
	}
}

func TestGzipNegotiation(t *testing.T) {
	tests := []struct {
		name   string
		accept string
		gzip   bool
	}{
		{"absent", "", false},
		{"gzip", "gzip", true},
		{"gzip among others", "deflate, gzip;q=0.8, br", true},
		{"gzip q=0", "gzip;q=0", false},
		{"gzip q=0 with wildcard", "gzip;q=0, *", false},
		{"wildcard", "*", true},
		{"wildcard q=0", "*;q=0", false},
		{"identity only", "identity", false},
		{"gzip q=0.5", "gzip;q=0.5", true},
		{"malformed q", "gzip;q=abc", false},
		{"similar token", "x-gzip", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store, _ := fixture(t, "/", true)
			resp := do(t, h, "GET", "/", map[string]string{"Accept-Encoding": tt.accept})
			if resp.StatusCode != 200 {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if !strings.Contains(strings.Join(resp.Header.Values("Vary"), ","), "Accept-Encoding") {
				t.Errorf("Vary = %v, want Accept-Encoding", resp.Header.Values("Vary"))
			}
			raw := body(t, resp)
			if !tt.gzip {
				if resp.Header.Get("Content-Encoding") != "" || !bytes.Equal(raw, store.Get().YAML) {
					t.Errorf("expected identity body, got Content-Encoding=%q", resp.Header.Get("Content-Encoding"))
				}
				return
			}
			if resp.Header.Get("Content-Encoding") != "gzip" {
				t.Fatalf("Content-Encoding = %q, want gzip", resp.Header.Get("Content-Encoding"))
			}
			if got := gunzip(t, raw); !bytes.Equal(got, store.Get().YAML) {
				t.Errorf("gunzipped body differs from raw body")
			}
			if resp.ContentLength != int64(len(raw)) {
				t.Errorf("Content-Length = %d, body = %d", resp.ContentLength, len(raw))
			}
		})
	}
}

func TestETagAndConditionalGet(t *testing.T) {
	h, _, _ := fixture(t, "/", true)
	plain := do(t, h, "GET", "/", nil)
	etag := plain.Header.Get("ETag")
	if !strings.HasPrefix(etag, `"`) || strings.HasPrefix(etag, "W/") {
		t.Fatalf("ETag = %q, want strong ETag", etag)
	}
	gz := do(t, h, "GET", "/", map[string]string{"Accept-Encoding": "gzip"})
	if gz.Header.Get("ETag") == etag {
		t.Errorf("gzip ETag equals identity ETag %q", etag)
	}

	tests := []struct {
		name   string
		header string
		status int
	}{
		{"match", etag, 304},
		{"weak match", "W/" + etag, 304},
		{"list with match", `"nope", ` + etag, 304},
		{"wildcard", "*", 304},
		{"mismatch", `"other"`, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := do(t, h, "GET", "/", map[string]string{"If-None-Match": tt.header})
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if tt.status == 304 && len(body(t, resp)) != 0 {
				t.Error("304 must not carry a body")
			}
		})
	}
}

func TestHeadAndMethods(t *testing.T) {
	h, store, _ := fixture(t, "/", true)

	head := do(t, h, "HEAD", "/", nil)
	if head.StatusCode != 200 || len(body(t, head)) != 0 {
		t.Errorf("HEAD status=%d, want 200 with no body", head.StatusCode)
	}
	if got := head.Header.Get("Content-Length"); got != strconv.Itoa(len(store.Get().YAML)) {
		t.Errorf("HEAD Content-Length = %q", got)
	}

	for _, m := range []string{"POST", "PUT", "DELETE"} {
		resp := do(t, h, m, "/", nil)
		if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: status=%d Allow=%q", m, resp.StatusCode, resp.Header.Get("Allow"))
		}
	}
}

func TestAvailability(t *testing.T) {
	tests := []struct {
		name       string
		publish    bool
		advance    time.Duration
		status     int
		retryAfter string
		bodyHas    string
	}{
		{"no snapshot", false, 0, 503, "10", "not yet available"},
		{"fresh", true, 0, 200, "", ""},
		{"exactly at stale boundary", true, time.Minute, 200, "", ""},
		{"stale", true, time.Minute + time.Second, 503, "10", "stale after 1m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, clk := fixture(t, "/", tt.publish)
			clk.t = clk.t.Add(tt.advance)
			resp := do(t, h, "GET", "/", nil)
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if tt.status == 503 {
				if got := resp.Header.Get("Retry-After"); got != tt.retryAfter {
					t.Errorf("Retry-After = %q, want %q", got, tt.retryAfter)
				}
				if b := string(body(t, resp)); !strings.Contains(b, tt.bodyHas) {
					t.Errorf("body %q does not contain %q", b, tt.bodyHas)
				}
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	tests := []struct {
		name    string
		publish bool
		advance time.Duration
		method  string
		status  int
		bodyHas string
	}{
		{"no snapshot", false, 0, "GET", 503, "not yet available"},
		{"fresh", true, 0, "GET", 200, "ok"},
		{"head fresh", true, 0, "HEAD", 200, ""},
		{"stale", true, 2 * time.Minute, "GET", 503, "stale after"},
		{"post", true, 0, "POST", 405, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, clk := fixture(t, "/", tt.publish)
			clk.t = clk.t.Add(tt.advance)
			resp := do(t, h, tt.method, "/healthz", nil)
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if b := string(body(t, resp)); tt.method == "GET" && !strings.Contains(b, tt.bodyHas) {
				t.Errorf("body %q does not contain %q", b, tt.bodyHas)
			}
		})
	}
}

func TestCustomEndpointPath(t *testing.T) {
	h, _, _ := fixture(t, "/traefik/config", true)
	tests := []struct {
		path   string
		status int
	}{
		{"/traefik/config", 200},
		{"/", 404},
		{"/traefik/config/extra", 404},
		{"/healthz", 200},
	}
	for _, tt := range tests {
		if resp := do(t, h, "GET", tt.path, nil); resp.StatusCode != tt.status {
			t.Errorf("GET %s = %d, want %d", tt.path, resp.StatusCode, tt.status)
		}
	}
}

func TestNewSnapshotRoundTrip(t *testing.T) {
	sn, err := NewSnapshot(testConf(), t0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gunzip(t, sn.YAMLGzip), sn.YAML) || !bytes.Equal(gunzip(t, sn.JSONGzip), sn.JSON) {
		t.Error("gzip variants do not decompress to the raw bodies")
	}
	if sn.YAMLETag == sn.JSONETag {
		t.Error("YAML and JSON share an ETag")
	}
}
