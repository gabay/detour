package builder

import (
	"fmt"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strconv"

	"detour/internal/config"
	"detour/internal/docker"
)

// ResolveAddress returns the host and port of the backend for c. serverPort, when
// set, overrides the port chosen by DefaultPort. An empty port is not an error;
// the caller decides whether it is acceptable.
//
// The host is taken, in order, from: a usable host port binding when
// useBindPortIP is set; the requested network (or the first network by name);
// the node IP for host network mode, or 127.0.0.1 when no node IP is known.
// Host network mode is not resolved through DNS (for example
// host.docker.internal).
func ResolveAddress(c docker.Container, opts Options, cfg config.DockerConfig, serverPort string) (host, port string, err error) {
	port = DefaultPort(c, serverPort)

	if cfg.UseBindPortIP {
		if ip, hostPort, ok := bindingFor(c, port); ok {
			return ip, hostPort, nil
		}
	}

	switch _, isContainer := c.IsContainerNetwork(); {
	case c.IsHostNetwork():
		host = c.NodeIP
		if host == "" {
			host = "127.0.0.1"
		}
	case isContainer:
		host = networkAddr(c, c.ConnectedNetworks, opts.Network)
	default:
		host = networkAddr(c, c.Networks, opts.Network)
	}
	if host == "" {
		return "", port, fmt.Errorf("unable to find the IP address for %q", c.Name)
	}
	return host, port, nil
}

// DefaultPort returns serverPort when set, otherwise the lowest exposed port of
// c, or "" when c exposes none.
func DefaultPort(c docker.Container, serverPort string) string {
	if serverPort != "" {
		return serverPort
	}
	if len(c.Ports) == 0 {
		return ""
	}
	lowest := c.Ports[0].Port
	for _, p := range c.Ports[1:] {
		lowest = min(lowest, p.Port)
	}
	return strconv.Itoa(int(lowest))
}

// bindingFor returns the host IP and host port of a published port that is
// bound to a specific, non-unspecified address.
func bindingFor(c docker.Container, port string) (ip, hostPort string, ok bool) {
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", "", false
	}
	for _, p := range c.Ports {
		if uint64(p.Port) != n || p.HostPort == "" {
			continue
		}
		if addr := net.ParseIP(p.HostIP); addr != nil && !addr.IsUnspecified() {
			return p.HostIP, p.HostPort, true
		}
	}
	return "", "", false
}

// networkAddr returns the address on the wanted network, falling back to the
// first network by name that has an address. It returns "" when none does.
func networkAddr(c docker.Container, nets map[string]docker.Network, want string) string {
	if want != "" {
		if n, ok := nets[want]; ok && n.Addr != "" {
			return n.Addr
		}
		slog.Warn("requested docker network has no address, falling back", "container", c.Name, "network", want)
	}
	for _, name := range slices.Sorted(maps.Keys(nets)) {
		if addr := nets[name].Addr; addr != "" {
			return addr
		}
	}
	return ""
}
