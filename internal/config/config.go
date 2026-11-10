// Package config loads and validates the Detour configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
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
	Docker  DockerConfig           `yaml:"docker"`
	Dynamic *dynamic.Configuration `yaml:"dynamic,omitempty"`
}

// ServerConfig configures the HTTP provider endpoint and polling intervals.
type ServerConfig struct {
	ListenAddr     string   `yaml:"listenAddr"`
	EndpointPath   string   `yaml:"endpointPath"`
	UpdateInterval Duration `yaml:"updateInterval"`
	StaleDuration  Duration `yaml:"staleDuration"`
}

// DockerConfig configures the Docker and Swarm label sources.
type DockerConfig struct {
	// Mode is one of docker, swarm, both, off or empty (treated as docker).
	Mode string `yaml:"mode"`
	// Containers and Swarm, when set, override the baseline derived from Mode.
	Containers *bool `yaml:"containers"`
	Swarm      *bool `yaml:"swarm"`

	Endpoint string           `yaml:"endpoint"`
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
		Docker: DockerConfig{
			Mode:        "docker",
			Endpoint:    defaultEndpoint,
			Timeout:     Duration(5 * time.Second),
			DefaultRule: defaultDefaultRule,
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

	d := c.Docker
	switch d.Mode {
	case "", "docker", "swarm", "both", "off":
	default:
		errs = append(errs, fmt.Errorf("docker.mode: unknown value %q (want docker, swarm, both or off)", d.Mode))
	}
	if scheme, rest, ok := strings.Cut(d.Endpoint, "://"); !ok || rest == "" {
		errs = append(errs, fmt.Errorf("docker.endpoint: %q must have the form scheme://address", d.Endpoint))
	} else {
		switch scheme {
		case "unix", "tcp", "http", "https":
		default:
			errs = append(errs, fmt.Errorf("docker.endpoint: unsupported scheme %q (want unix, tcp, http or https)", scheme))
		}
	}
	if d.Timeout <= 0 {
		errs = append(errs, fmt.Errorf("docker.timeout: must be greater than 0, got %s", d.Timeout.Std()))
	}
	if (d.Username == "") != (d.Password == "") {
		errs = append(errs, errors.New("docker.username and docker.password must be set together"))
	}
	if d.TLS != nil && (d.TLS.Cert == "") != (d.TLS.Key == "") {
		errs = append(errs, errors.New("docker.tls.cert and docker.tls.key must be set together"))
	}
	if err := constraints.Validate(d.Constraints); err != nil {
		errs = append(errs, fmt.Errorf("docker.constraints: %w", err))
	}
	if _, err := rule.Parse(d.DefaultRule); err != nil {
		errs = append(errs, fmt.Errorf("docker.defaultRule: %w", err))
	}
	return errors.Join(errs...)
}

// ContainersEnabled reports whether Docker container labels are collected.
// An explicit containers boolean wins over the mode baseline.
func (d DockerConfig) ContainersEnabled() bool {
	if d.Containers != nil {
		return *d.Containers
	}
	return d.Mode == "" || d.Mode == "docker" || d.Mode == "both"
}

// SwarmEnabled reports whether Swarm service labels are collected.
// An explicit swarm boolean wins over the mode baseline.
func (d DockerConfig) SwarmEnabled() bool {
	if d.Swarm != nil {
		return *d.Swarm
	}
	return d.Mode == "swarm" || d.Mode == "both"
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
