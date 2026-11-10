package docker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"

	"detour/internal/config"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

// API is the subset of the Docker client used by the Collector. *client.Client
// satisfies it; tests can substitute a fake.
type API interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(ctx context.Context, containerID string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ServiceList(ctx context.Context, options client.ServiceListOptions) (client.ServiceListResult, error)
	NetworkList(ctx context.Context, options client.NetworkListOptions) (client.NetworkListResult, error)
	TaskList(ctx context.Context, options client.TaskListOptions) (client.TaskListResult, error)
	NodeInspect(ctx context.Context, nodeID string, options client.NodeInspectOptions) (client.NodeInspectResult, error)
}

// Collector gathers containers and/or Swarm services as neutral Container values.
type Collector struct {
	api API
	cfg config.DockerConfig
}

// NewCollector returns a Collector reading from api according to cfg.
func NewCollector(api API, cfg config.DockerConfig) *Collector {
	return &Collector{api: api, cfg: cfg}
}

// Close closes the underlying API if it implements io.Closer (as *client.Client does).
func (c *Collector) Close() error {
	if closer, ok := c.api.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Collect returns every container and/or Swarm entry, sorted by Name then ID.
// It returns an error if any top-level list call fails, so callers can treat
// the poll as failed; individual inspect failures are logged and skipped.
func (c *Collector) Collect(ctx context.Context) ([]Container, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []Container{}
	if c.cfg.ContainersEnabled() {
		ctrs, err := c.collectContainers(ctx)
		if err != nil {
			return nil, fmt.Errorf("collect containers: %w", err)
		}
		out = append(out, ctrs...)
	}
	if c.cfg.SwarmEnabled() {
		entries, err := c.collectSwarm(ctx)
		if err != nil {
			return nil, fmt.Errorf("collect swarm: %w", err)
		}
		out = append(out, entries...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (c *Collector) collectContainers(ctx context.Context) ([]Container, error) {
	list, err := c.api.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	out := make([]Container, 0, len(list.Items))
	for _, summary := range list.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		insp, err := c.api.ContainerInspect(ctx, summary.ID, client.ContainerInspectOptions{})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			logSkip("container", summary.ID, err)
			continue
		}
		ctr := containerFromInspect(insp.Container)
		if target, ok := ctr.IsContainerNetwork(); ok {
			c.resolveConnected(ctx, &ctr, target)
		}
		out = append(out, ctr)
	}
	return out, nil
}

// resolveConnected fills ConnectedContainerID and ConnectedNetworks from the
// container whose network stack ctr shares. Failure is logged and leaves the
// fields empty.
func (c *Collector) resolveConnected(ctx context.Context, ctr *Container, target string) {
	insp, err := c.api.ContainerInspect(ctx, target, client.ContainerInspectOptions{})
	if err != nil {
		logSkip("connected container of "+ctr.Name, target, err)
		return
	}
	ctr.ConnectedContainerID = insp.Container.ID
	ctr.ConnectedNetworks = networksFromSettings(insp.Container.NetworkSettings)
}

func containerFromInspect(ci container.InspectResponse) Container {
	ctr := Container{
		ID:       ci.ID,
		Name:     trimSlash(ci.Name),
		Source:   SourceContainer,
		Ports:    []Port{},
		Networks: map[string]Network{},
	}
	ctr.ServiceName = ctr.Name
	if ci.State != nil {
		ctr.Status = string(ci.State.Status)
		if ci.State.Health != nil {
			ctr.Health = healthString(ci.State.Health.Status)
		}
	}
	if ci.Config != nil {
		ctr.Labels = ci.Config.Labels
	}
	if ci.HostConfig != nil {
		ctr.NetworkMode = string(ci.HostConfig.NetworkMode)
	}
	if ci.NetworkSettings != nil {
		ctr.Ports = portsFromMap(ci.NetworkSettings.Ports)
		ctr.Networks = networksFromSettings(ci.NetworkSettings)
	}
	return ctr
}

func (c *Collector) collectSwarm(ctx context.Context) ([]Container, error) {
	nets, err := c.api.NetworkList(ctx, client.NetworkListOptions{
		Filters: make(client.Filters).Add("scope", "swarm"),
	})
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	swarmNets := make(map[string]Network, len(nets.Items))
	for _, n := range nets.Items {
		swarmNets[n.ID] = Network{ID: n.ID, Name: n.Name}
	}

	svcs, err := c.api.ServiceList(ctx, client.ServiceListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}

	nodeIPs := make(map[string]string)
	var out []Container
	for _, svc := range svcs.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		base := serviceBase(svc)

		vip := base
		vip.ID = svc.ID
		vip.Networks = vipNetworks(svc.Endpoint.VirtualIPs, swarmNets)
		vip.Swarm = SwarmInfo{ServiceID: svc.ID, ServiceName: svc.Spec.Name, VIP: true}
		out = append(out, vip)

		if !wantsTaskEntries(svc.Spec.Labels) {
			continue
		}
		tasks, err := c.api.TaskList(ctx, client.TaskListOptions{
			Filters: make(client.Filters).Add("service", svc.ID).Add("desired-state", string(swarm.TaskStateRunning)),
		})
		if err != nil {
			return nil, fmt.Errorf("list tasks of service %s: %w", svc.Spec.Name, err)
		}
		for _, task := range tasks.Items {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if task.Status.State != swarm.TaskStateRunning {
				continue
			}
			nodeIP, err := c.nodeIP(ctx, nodeIPs, task.NodeID)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				logSkip("task "+task.ID+" node", task.NodeID, err)
				continue
			}
			entry := base
			entry.ID = task.ID
			entry.Name = taskName(svc.Spec.Name, task)
			entry.Networks = taskNetworks(task.NetworksAttachments, swarmNets)
			entry.NodeIP = nodeIP
			entry.Swarm = SwarmInfo{ServiceID: svc.ID, ServiceName: svc.Spec.Name, Task: true}
			out = append(out, entry)
		}
	}
	return out, nil
}

// nodeIP returns the address of a node, caching successful lookups for the
// duration of one Collect call.
func (c *Collector) nodeIP(ctx context.Context, cache map[string]string, nodeID string) (string, error) {
	if ip, ok := cache[nodeID]; ok {
		return ip, nil
	}
	n, err := c.api.NodeInspect(ctx, nodeID, client.NodeInspectOptions{})
	if err != nil {
		return "", err
	}
	cache[nodeID] = n.Node.Status.Addr
	return n.Node.Status.Addr, nil
}

// serviceBase holds the fields shared by a service's VIP entry and task entries.
func serviceBase(svc swarm.Service) Container {
	return Container{
		ServiceName: svc.Spec.Name,
		Name:        svc.Spec.Name,
		Source:      SourceSwarm,
		Labels:      svc.Spec.Labels,
		Ports:       swarmPorts(svc.Endpoint.Ports),
		Networks:    map[string]Network{},
	}
}

// wantsTaskEntries reports whether task entries are needed for a service. They
// are skipped when lbswarm is enabled (the VIP entry is used instead) and when
// traefik.enable is explicitly "false".
func wantsTaskEntries(labels map[string]string) bool {
	if labels["traefik.enable"] == "false" {
		return false
	}
	return labels["traefik.swarm.lbswarm"] != "true" && labels["traefik.docker.lbswarm"] != "true"
}

func taskName(service string, task swarm.Task) string {
	if task.Slot == 0 {
		return service + "." + task.ID
	}
	return service + "." + strconv.Itoa(task.Slot)
}

func vipNetworks(vips []swarm.EndpointVirtualIP, swarmNets map[string]Network) map[string]Network {
	out := make(map[string]Network, len(vips))
	for _, vip := range vips {
		n, ok := swarmNets[vip.NetworkID]
		if !ok {
			continue
		}
		if vip.Addr.IsValid() {
			n.Addr = vip.Addr.Addr().String()
		}
		out[n.Name] = n
	}
	return out
}

func taskNetworks(atts []swarm.NetworkAttachment, swarmNets map[string]Network) map[string]Network {
	out := make(map[string]Network, len(atts))
	for _, att := range atts {
		n, ok := swarmNets[att.Network.ID]
		if !ok {
			continue
		}
		if len(att.Addresses) > 0 && att.Addresses[0].IsValid() {
			n.Addr = att.Addresses[0].Addr().String()
		}
		out[n.Name] = n
	}
	return out
}

func swarmPorts(ports []swarm.PortConfig) []Port {
	out := make([]Port, 0, len(ports))
	for _, p := range ports {
		port := Port{Port: uint16(p.TargetPort), Proto: string(p.Protocol)}
		if p.PublishMode == swarm.PortConfigPublishModeHost && p.PublishedPort != 0 {
			port.HostPort = strconv.FormatUint(uint64(p.PublishedPort), 10)
		}
		out = append(out, port)
	}
	sortPorts(out)
	return out
}

func portsFromMap(pm network.PortMap) []Port {
	out := []Port{}
	for p, bindings := range pm {
		base := Port{Port: p.Num(), Proto: string(p.Proto())}
		if len(bindings) == 0 {
			out = append(out, base)
			continue
		}
		for _, b := range bindings {
			port := base
			if b.HostIP.IsValid() {
				port.HostIP = b.HostIP.String()
			}
			port.HostPort = b.HostPort
			out = append(out, port)
		}
	}
	sortPorts(out)
	return out
}

func sortPorts(ports []Port) {
	sort.SliceStable(ports, func(i, j int) bool {
		if ports[i].Port != ports[j].Port {
			return ports[i].Port < ports[j].Port
		}
		if ports[i].Proto != ports[j].Proto {
			return ports[i].Proto < ports[j].Proto
		}
		if ports[i].HostIP != ports[j].HostIP {
			return ports[i].HostIP < ports[j].HostIP
		}
		return ports[i].HostPort < ports[j].HostPort
	})
}

func networksFromSettings(ns *container.NetworkSettings) map[string]Network {
	out := map[string]Network{}
	if ns == nil {
		return out
	}
	for name, ep := range ns.Networks {
		if ep == nil {
			continue
		}
		n := Network{ID: ep.NetworkID, Name: name}
		if ep.IPAddress.IsValid() {
			n.Addr = ep.IPAddress.String()
		}
		out[name] = n
	}
	return out
}

func healthString(s container.HealthStatus) string {
	switch s {
	case container.Starting, container.Healthy, container.Unhealthy:
		return string(s)
	default:
		return ""
	}
}

func trimSlash(name string) string {
	if len(name) > 0 && name[0] == '/' {
		return name[1:]
	}
	return name
}

func logSkip(kind, id string, err error) {
	if cerrdefs.IsNotFound(err) {
		slog.Debug("docker: skipping item that disappeared", "kind", kind, "id", id, "err", err)
		return
	}
	slog.Warn("docker: skipping item", "kind", kind, "id", id, "err", err)
}
