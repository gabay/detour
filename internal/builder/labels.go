// Package builder holds the helpers that turn Docker and Swarm entries into
// routable backends: label options, constraint filtering and address resolution.
package builder

import (
	"fmt"
	"strconv"
	"strings"

	"detour/internal/config"
	"detour/internal/docker"
)

const (
	labelEnable                = "traefik.enable"
	labelDockerNetwork         = "traefik.docker.network"
	labelDockerAllowNonRunning = "traefik.docker.allownonrunning"
	labelDockerLBSwarm         = "traefik.docker.lbswarm"
	labelSwarmNetwork          = "traefik.swarm.network"
	labelSwarmLBSwarm          = "traefik.swarm.lbswarm"
)

// Options are the per-container settings read from Traefik labels.
type Options struct {
	Enable          bool
	Network         string
	LBSwarm         bool
	AllowNonRunning bool
}

// ExtractOptions reads the control labels of c, applying the defaults from cfg.
// Swarm entries use the traefik.swarm.* labels (with deprecated traefik.docker.*
// aliases) and must not mix the two families; container entries use
// traefik.docker.* only.
func ExtractOptions(c docker.Container, cfg config.DockerConfig) (Options, error) {
	opts := Options{Enable: cfg.ExposedByDefault, Network: cfg.Network}

	enable, ok, err := boolLabel(c.Labels, labelEnable)
	if err != nil {
		return opts, err
	}
	if ok {
		opts.Enable = enable
	}

	if c.Source == docker.SourceSwarm {
		if err := checkSwarmLabels(c.Labels); err != nil {
			return opts, err
		}
		if v, ok, err := boolLabel(c.Labels, labelSwarmLBSwarm, labelDockerLBSwarm); err != nil {
			return opts, err
		} else if ok {
			opts.LBSwarm = v
		}
		if _, v, ok := lookup(c.Labels, labelSwarmNetwork, labelDockerNetwork); ok {
			opts.Network = v
		}
		return opts, nil
	}

	if _, v, ok := lookup(c.Labels, labelDockerNetwork); ok {
		opts.Network = v
	}
	if v, ok, err := boolLabel(c.Labels, labelDockerAllowNonRunning); err != nil {
		return opts, err
	} else if ok {
		opts.AllowNonRunning = v
	}
	return opts, nil
}

// checkSwarmLabels rejects services that define both traefik.docker.* and
// traefik.swarm.* labels, which would make the intent ambiguous.
func checkSwarmLabels(labels map[string]string) error {
	var hasDocker, hasSwarm bool
	for k := range labels {
		switch {
		case strings.HasPrefix(k, "traefik.docker."):
			hasDocker = true
		case strings.HasPrefix(k, "traefik.swarm."):
			hasSwarm = true
		}
	}
	if hasDocker && hasSwarm {
		return fmt.Errorf("both Docker and Swarm labels are defined")
	}
	return nil
}

// lookup returns the first of keys present in labels, with its value.
func lookup(labels map[string]string, keys ...string) (key, value string, ok bool) {
	for _, k := range keys {
		if v, found := labels[k]; found {
			return k, v, true
		}
	}
	return "", "", false
}

// boolLabel parses the first present key as a boolean.
func boolLabel(labels map[string]string, keys ...string) (value, ok bool, err error) {
	k, v, ok := lookup(labels, keys...)
	if !ok {
		return false, false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, true, fmt.Errorf("label %s: invalid boolean %q", k, v)
	}
	return b, true, nil
}
