package server

import (
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"detour/internal/compiler"
	"detour/internal/config"
)

const (
	healthPath      = "/healthz"
	contentTypeYAML = "application/yaml; charset=utf-8"
	contentTypeJSON = "application/json; charset=utf-8"
	msgNotYet       = "configuration not yet available"
)

// Server serves the current Snapshot at the configured endpoint path and a
// health check at /healthz.
type Server struct {
	store  *Store
	cfg    atomic.Pointer[config.ServerConfig]
	now    func() time.Time
	logger *slog.Logger
}

// New creates a Server. A nil now uses time.Now and a nil logger uses slog.Default.
func New(store *Store, cfg config.ServerConfig, now func() time.Time, logger *slog.Logger) *Server {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{store: store, now: now, logger: logger}
	s.SetConfig(cfg)
	return s
}

// SetConfig replaces the endpoint and staleness settings; it is safe to call while serving.
func (s *Server) SetConfig(cfg config.ServerConfig) {
	s.cfg.Store(&cfg)
}

// Handler returns the HTTP handler for the endpoint and health check.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

// NewHTTPServer returns an http.Server for handler with conservative timeouts and header limits.
func NewHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("http request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
	switch r.URL.Path {
	case s.cfg.Load().EndpointPath:
		s.serveConfig(w, r)
	case healthPath:
		s.serveHealth(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveConfig(w http.ResponseWriter, r *http.Request) {
	if !allowReadOnly(w, r) {
		return
	}
	format, byQuery, err := requestedFormat(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h := w.Header()
	h.Add("Vary", "Accept-Encoding")
	if !byQuery {
		h.Add("Vary", "Accept")
	}

	sn := s.store.Get()
	if reason := s.unavailableReason(sn); reason != "" {
		s.unavailable(w, reason)
		return
	}

	gzipped := acceptsGzip(r.Header.Values("Accept-Encoding"))
	body, etag, contentType := sn.variant(format, gzipped)
	h.Set("ETag", etag)
	h.Set("Cache-Control", "no-store")
	if gzipped {
		h.Set("Content-Encoding", "gzip")
	}
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (s *Server) serveHealth(w http.ResponseWriter, r *http.Request) {
	if !allowReadOnly(w, r) {
		return
	}
	if reason := s.unavailableReason(s.store.Get()); reason != "" {
		s.unavailable(w, reason)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("ok\n"))
	}
}

// unavailableReason returns why no configuration can be served, or "" when
// the snapshot is present and fresh.
func (s *Server) unavailableReason(sn *Snapshot) string {
	if sn == nil {
		return msgNotYet
	}
	age := s.now().Sub(sn.UpdatedAt)
	stale := s.cfg.Load().StaleDuration.Std()
	if age > stale {
		return fmt.Sprintf("no recent configuration available: last successful update %s ago (stale after %s)",
			age.Round(time.Millisecond), stale)
	}
	return ""
}

// unavailable answers 503 with Retry-After set to the polling interval. Traefik
// treats any non-200 as a failed poll and keeps its previous configuration.
func (s *Server) unavailable(w http.ResponseWriter, reason string) {
	secs := int(math.Ceil(s.cfg.Load().UpdateInterval.Std().Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, reason, http.StatusServiceUnavailable)
}

// allowReadOnly accepts GET and HEAD and answers 405 for any other method.
func allowReadOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	return false
}

// requestedFormat resolves the output format from ?format=, falling back to the
// Accept header. byQuery reports whether the query parameter decided it.
func requestedFormat(r *http.Request) (format compiler.Format, byQuery bool, err error) {
	if values, ok := r.URL.Query()["format"]; ok {
		switch values[0] {
		case "json":
			return compiler.FormatJSON, true, nil
		case "yaml":
			return compiler.FormatYAML, true, nil
		default:
			return 0, true, fmt.Errorf("unknown format %q (want yaml or json)", values[0])
		}
	}
	if acceptsJSON(r.Header.Values("Accept")) {
		return compiler.FormatJSON, false, nil
	}
	return compiler.FormatYAML, false, nil
}

// acceptsGzip reports whether the Accept-Encoding header allows gzip. An explicit
// "gzip" entry takes precedence over the "*" wildcard; q=0 means refused.
func acceptsGzip(values []string) bool {
	m := parseAccept(values)
	if q, ok := m["gzip"]; ok {
		return q > 0
	}
	q, ok := m["*"]
	return ok && q > 0
}

// acceptsJSON reports whether JSON is preferred over YAML in the Accept header.
// Ties and absent preferences fall back to YAML.
func acceptsJSON(values []string) bool {
	m := parseAccept(values)
	jsonQ := m["application/json"]
	yamlQ := max(m["application/yaml"], m["application/x-yaml"], m["text/yaml"])
	return jsonQ > 0 && jsonQ > yamlQ
}

// parseAccept maps each lower-cased token (media type or coding) to its highest
// q-value. Missing q defaults to 1; a malformed q counts as 0.
func parseAccept(values []string) map[string]float64 {
	m := make(map[string]float64)
	for _, v := range values {
		for _, entry := range strings.Split(v, ",") {
			name, params, _ := strings.Cut(entry, ";")
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			q := 1.0
			for _, p := range strings.Split(params, ";") {
				k, val, ok := strings.Cut(strings.TrimSpace(p), "=")
				if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
					parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
					if err != nil {
						parsed = 0
					}
					q = parsed
				}
			}
			if cur, ok := m[name]; !ok || q > cur {
				m[name] = q
			}
		}
	}
	return m
}

// etagMatches implements weak comparison of an If-None-Match header against etag.
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimSpace(part)
		if p == "*" || strings.TrimPrefix(p, "W/") == etag {
			return true
		}
	}
	return false
}
