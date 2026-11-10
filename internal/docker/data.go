package docker

import "strings"

// Source identifies where a Container entry was collected from.
type Source string

const (
	SourceContainer Source = "container"
	SourceSwarm     Source = "swarm"
)

// Container is one routable unit: a standalone container, a Swarm task, or the
// virtual-IP entry of a Swarm service.
type Container struct {
	ID          string
	Name        string // container name without leading "/"; for swarm: service name, or "<service>.<slot>" / "<service>.<taskID>" for tasks
	ServiceName string // container name, or swarm service name
	Source      Source
	Status      string             // container state e.g. "running"; "" for swarm
	Health      string             // "", "healthy", "unhealthy", "starting"
	Labels      map[string]string  // container labels, or swarm service labels
	NetworkMode string             // container HostConfig.NetworkMode raw string
	Ports       []Port             // sorted by Port, then Proto
	Networks    map[string]Network // by network name
	NodeIP      string             // swarm task entries only: IP of the node running the task
	Swarm       SwarmInfo          // zero value for containers

	// ConnectedContainerID and ConnectedNetworks are set for NetworkMode
	// "container:<id>" and hold the target container's resolved networks.
	ConnectedContainerID string
	ConnectedNetworks    map[string]Network
}

// Port is an exposed or published port. HostIP and HostPort are empty when the
// port is not bound to the host.
type Port struct {
	Port     uint16
	Proto    string
	HostIP   string
	HostPort string
}

// Network is an attachment on a named network. Addr is empty when unknown.
type Network struct {
	ID   string
	Name string
	Addr string
}

// SwarmInfo distinguishes the entries produced for a Swarm service.
//
// A service yields one VIP entry (VIP=true, Networks holding the service
// virtual IPs) and, for services not using lbswarm, one entry per running
// task (Task=true, Networks holding the task addresses, NodeIP set). The
// builder uses the VIP entry when lbswarm is enabled and task entries otherwise.
type SwarmInfo struct {
	ServiceID   string
	ServiceName string
	VIP         bool
	Task        bool
}

// IsHostNetwork reports whether the container shares the host network stack.
func (c Container) IsHostNetwork() bool {
	return c.NetworkMode == "host"
}

// IsContainerNetwork reports whether the container shares another container's
// network stack and returns the raw target reference (ID or name).
func (c Container) IsContainerNetwork() (string, bool) {
	target, ok := strings.CutPrefix(c.NetworkMode, "container:")
	if !ok {
		return "", false
	}
	return target, true
}
