// Package config loads and validates the Detour configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"detour/internal/constraints"
	"detour/internal/dynamic"
	"detour/internal/rule"

	"gopkg.in/yaml.v3"
)

// Config is the root of the Detour configuration file.
type Config struct {
	Server  ServerConfig           `yaml:"server"`
	Docker  DockerProviders        `yaml:"docker,omitempty"`
	Dynamic *dynamic.Configuration `yaml:"dynamic,omitempty"`
}

// DockerProviders is a map of named Docker provider configurations where empty
// or null entries (e.g. `local:`) still receive DefaultDockerConfig().
type DockerProviders map[string]DockerConfig

// UnmarshalYAML decodes a YAML mapping of provider name -> DockerConfig.
func (p *DockerProviders) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		*p = nil
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: docker must be a map of named providers", node.Line)
	}
	out := make(DockerProviders, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		var name string
		if err := keyNode.Decode(&name); err != nil {
			return err
		}
		d := DefaultDockerConfig()
		if !(valNode.Kind == yaml.ScalarNode && valNode.Tag == "!!null") {
			if err := valNode.Decode(&d); err != nil {
				return err
			}
		}
		out[name] = d
	}
	*p = out
	return nil
}

// ServerConfig configures the HTTP provider endpoint and polling intervals.
type ServerConfig struct {
	ListenAddr     string   `yaml:"listenAddr"`
	EndpointPath   string   `yaml:"endpointPath"`
	UpdateInterval Duration `yaml:"updateInterval"`
	StaleDuration  Duration `yaml:"staleDuration"`
}

// DockerConfig configures one Docker and/or Swarm label provider.
type DockerConfig struct {
	// Containers enables collecting labels from Docker containers.
	Containers bool `yaml:"containers"`
	// Swarm enables collecting labels from Swarm services.
	Swarm bool `yaml:"swarm"`

	Endpoint string           `yaml:"endpoint"`
	Discover bool             `yaml:"discover"`
	Timeout  Duration         `yaml:"timeout"`
	Username string           `yaml:"username"`
	Password string           `yaml:"password"`
	TLS      *TLSClientConfig `yaml:"tls"`

	ExposedByDefault   bool   `yaml:"exposedByDefault"`
	Network            string `yaml:"network"`
	DefaultRule        string `yaml:"defaultRule"`
	Constraints        string `yaml:"constraints"`
	UseBindPortIP      bool   `yaml:"useBindPortIP"`
	AllowEmptyServices bool   `yaml:"allowEmptyServices"`
}

// TLSClientConfig configures TLS towards the Docker API endpoint.
type TLSClientConfig struct {
	CA                 string `yaml:"ca"`
	Cert               string `yaml:"cert"`
	Key                string `yaml:"key"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

const (
	defaultListenAddr   = ":7890"
	defaultEndpointPath = "/"
	defaultEndpoint     = "unix:///var/run/docker.sock"
	defaultDefaultRule  = "Host(`{{ normalize .Name }}`)"
)

// DefaultDockerConfig returns a DockerConfig populated with its default values.
func DefaultDockerConfig() DockerConfig {
	return DockerConfig{
		Containers:  true,
		Swarm:       false,
		Endpoint:    defaultEndpoint,
		Timeout:     Duration(5 * time.Second),
		DefaultRule: defaultDefaultRule,
	}
}

// UnmarshalYAML decodes node onto DefaultDockerConfig() while preserving KnownFields(true).
func (d *DockerConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		*d = DefaultDockerConfig()
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: docker provider configuration must be a mapping", node.Line)
	}
	type plain DockerConfig
	out := plain(DefaultDockerConfig())
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(node); err != nil {
		return err
	}
	_ = enc.Close()
	dec := yaml.NewDecoder(&buf)
	dec.KnownFields(true)
	if err := dec.Decode(&out); err != nil {
		return err
	}
	*d = DockerConfig(out)
	return nil
}

// Default returns the configuration used when no file is given or when a
// key is absent from it.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			ListenAddr:     defaultListenAddr,
			EndpointPath:   defaultEndpointPath,
			UpdateInterval: Duration(10 * time.Second),
			StaleDuration:  Duration(time.Minute),
		},
	}
}

// Parse decodes a YAML document onto the defaults, rejects unknown keys and
// validates the result. An empty document yields the defaults.
func Parse(data []byte) (*Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadFile reads, parses and validates the configuration file at path.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks all values and returns every problem found, joined.
func (c *Config) Validate() error {
	var errs []error
	s := c.Server
	if s.ListenAddr == "" {
		errs = append(errs, errors.New("server.listenAddr: must not be empty"))
	}
	if !strings.HasPrefix(s.EndpointPath, "/") {
		errs = append(errs, fmt.Errorf("server.endpointPath: %q must start with \"/\"", s.EndpointPath))
	}
	if s.EndpointPath == "/healthz" {
		errs = append(errs, errors.New("server.endpointPath: \"/healthz\" is reserved for the health check"))
	}
	if s.UpdateInterval <= 0 {
		errs = append(errs, fmt.Errorf("server.updateInterval: must be greater than 0, got %s", s.UpdateInterval.Std()))
	}
	if s.StaleDuration <= 0 {
		errs = append(errs, fmt.Errorf("server.staleDuration: must be greater than 0, got %s", s.StaleDuration.Std()))
	}

	for _, name := range slices.Sorted(maps.Keys(c.Docker)) {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, errors.New("docker: provider name must not be empty"))
			continue
		}
		prefix := "docker." + name
		d := c.Docker[name]
		scheme, rest, ok := strings.Cut(d.Endpoint, "://")
		if !ok || rest == "" {
			errs = append(errs, fmt.Errorf("%s.endpoint: %q must have the form scheme://address", prefix, d.Endpoint))
		} else {
			switch scheme {
			case "unix":
				if d.Discover {
					errs = append(errs, fmt.Errorf("%s.discover: cannot be used with unix:// endpoint %q", prefix, d.Endpoint))
				}
			case "tcp", "http", "https":
				if d.Discover {
					u, err := url.Parse(d.Endpoint)
					if err != nil || u.Hostname() == "" {
						errs = append(errs, fmt.Errorf("%s.endpoint: %q must include a hostname when discover is enabled", prefix, d.Endpoint))
					}
				}
			default:
				errs = append(errs, fmt.Errorf("%s.endpoint: unsupported scheme %q (want unix, tcp, http or https)", prefix, scheme))
			}
		}
		if d.Timeout <= 0 {
			errs = append(errs, fmt.Errorf("%s.timeout: must be greater than 0, got %s", prefix, d.Timeout.Std()))
		}
		if (d.Username == "") != (d.Password == "") {
			errs = append(errs, fmt.Errorf("%s.username and %s.password must be set together", prefix, prefix))
		}
		if d.TLS != nil && (d.TLS.Cert == "") != (d.TLS.Key == "") {
			errs = append(errs, fmt.Errorf("%s.tls.cert and %s.tls.key must be set together", prefix, prefix))
		}
		if err := constraints.Validate(d.Constraints); err != nil {
			errs = append(errs, fmt.Errorf("%s.constraints: %w", prefix, err))
		}
		if _, err := rule.Parse(d.DefaultRule); err != nil {
			errs = append(errs, fmt.Errorf("%s.defaultRule: %w", prefix, err))
		}
	}
	return errors.Join(errs...)
}

// Duration is a time.Duration that accepts Go duration strings ("30s", "2m")
// as well as plain integers, which are interpreted as seconds.
type Duration time.Duration

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: duration must be a scalar", node.Line)
	}
	if node.Tag == "!!null" {
		return nil
	}
	v, err := parseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*d = v
	return nil
}

// MarshalYAML implements yaml.Marshaler, emitting the Go duration string.
func (d Duration) MarshalYAML() (any, error) {
	return d.Std().String(), nil
}

// UnmarshalJSON implements json.Unmarshaler with the same forms as YAML.
func (d *Duration) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
	}
	v, err := parseDuration(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalJSON implements json.Marshaler, emitting the Go duration string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Std().String())
}

func parseDuration(s string) (Duration, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > math.MaxInt64/int64(time.Second) || n < math.MinInt64/int64(time.Second) {
			return 0, fmt.Errorf("duration %q is out of range", s)
		}
		return Duration(time.Duration(n) * time.Second), nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: use a Go duration such as \"10s\" or an integer number of seconds", s)
	}
	return Duration(v), nil
}
