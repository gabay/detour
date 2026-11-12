// Package docker collects standalone containers and Swarm services from a
// Docker Engine API endpoint and converts them to a neutral data model.
package docker

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"detour/internal/config"

	"github.com/moby/moby/client"
)

// Version is reported in the User-Agent header. Override it at build time with
// -ldflags "-X detour/internal/docker.Version=...".
var Version = "dev"

// NewClient builds a Docker API client from the configuration file only; the
// DOCKER_* environment variables are deliberately ignored. API version
// negotiation is left at the client default (enabled).
func NewClient(cfg config.DockerConfig) (*client.Client, error) {
	return NewClientWithServerName(cfg, "")
}

// NewClientWithServerName builds a Docker API client like NewClient, using
// serverName as the TLS ServerName when non-empty (for example when connecting
// to an IP resolved through DNS discovery).
func NewClientWithServerName(cfg config.DockerConfig, serverName string) (*client.Client, error) {
	host, scheme, err := endpointURL(cfg)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{}
	if cfg.TLS != nil {
		tlsCfg, err := tlsClientConfig(*cfg.TLS, serverName)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsCfg
	} else if scheme == "https" && serverName != "" {
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		}
	}

	// WithHTTPClient must precede WithHost, which configures the transport's dialer.
	opts := []client.Opt{
		client.WithHTTPClient(&http.Client{Transport: transport}),
		client.WithHost(host),
		client.WithScheme(scheme),
		client.WithTimeout(cfg.Timeout.Std()),
		client.WithUserAgent("Detour/" + Version),
	}
	if cfg.Username != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
		opts = append(opts, client.WithHTTPHeaders(map[string]string{"Authorization": "Basic " + auth}))
	}

	c, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return c, nil
}

// endpointURL converts cfg.Endpoint into the host string understood by the
// client and the HTTP scheme used for requests. tcp:// is plain HTTP unless a
// TLS block is configured, in which case it is HTTPS.
func endpointURL(cfg config.DockerConfig) (host, scheme string, err error) {
	proto, addr, ok := strings.Cut(cfg.Endpoint, "://")
	if !ok || addr == "" {
		return "", "", fmt.Errorf("docker endpoint %q must have the form scheme://address", cfg.Endpoint)
	}
	switch proto {
	case "unix":
		return cfg.Endpoint, "http", nil
	case "http", "https":
		return cfg.Endpoint, proto, nil
	case "tcp":
		scheme = "http"
		if cfg.TLS != nil {
			scheme = "https"
		}
		return scheme + "://" + addr, scheme, nil
	default:
		return "", "", fmt.Errorf("docker endpoint %q: unsupported scheme %q", cfg.Endpoint, proto)
	}
}

func tlsClientConfig(t config.TLSClientConfig, serverName string) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: t.InsecureSkipVerify, //nolint:gosec // explicitly requested by the operator
		ServerName:         serverName,
	}
	if t.CA != "" {
		pem, err := os.ReadFile(t.CA)
		if err != nil {
			return nil, fmt.Errorf("docker tls ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("docker tls ca %s: no PEM certificates found", t.CA)
		}
		tlsCfg.RootCAs = pool
	}
	if t.Cert != "" {
		cert, err := tls.LoadX509KeyPair(t.Cert, t.Key)
		if err != nil {
			return nil, fmt.Errorf("docker tls client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}
