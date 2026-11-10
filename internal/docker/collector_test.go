package docker

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"detour/internal/config"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

// fakeAPI is an in-memory Docker API. Calls are counted per method so tests can
// assert which endpoints were touched.
type fakeAPI struct {
	containers    []container.Summary
	inspect       map[string]container.InspectResponse
	inspectErr    map[string]error
	containerErr  error
	networks      []network.Summary
	networkErr    error
	services      []swarm.Service
	serviceErr    error
	tasks         map[string][]swarm.Task // by service ID
	nodes         map[string]swarm.Node
	calls         map[string]int
	nodeInspected []string
}

func (f *fakeAPI) hit(name string) { f.calls[name]++ }

func (f *fakeAPI) ContainerList(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
	f.hit("ContainerList")
	return client.ContainerListResult{Items: f.containers}, f.containerErr
}

func (f *fakeAPI) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	f.hit("ContainerInspect")
	if err := f.inspectErr[id]; err != nil {
		return client.ContainerInspectResult{}, err
	}
	ci, ok := f.inspect[id]
	if !ok {
		return client.ContainerInspectResult{}, cerrdefs.ErrNotFound
	}
	return client.ContainerInspectResult{Container: ci}, nil
}

func (f *fakeAPI) ServiceList(_ context.Context, _ client.ServiceListOptions) (client.ServiceListResult, error) {
	f.hit("ServiceList")
	return client.ServiceListResult{Items: f.services}, f.serviceErr
}

func (f *fakeAPI) NetworkList(_ context.Context, _ client.NetworkListOptions) (client.NetworkListResult, error) {
	f.hit("NetworkList")
	return client.NetworkListResult{Items: f.networks}, f.networkErr
}

func (f *fakeAPI) TaskList(_ context.Context, opts client.TaskListOptions) (client.TaskListResult, error) {
	f.hit("TaskList")
	var out []swarm.Task
	for svcID := range opts.Filters["service"] {
		out = append(out, f.tasks[svcID]...)
	}
	return client.TaskListResult{Items: out}, nil
}

func (f *fakeAPI) NodeInspect(_ context.Context, id string, _ client.NodeInspectOptions) (client.NodeInspectResult, error) {
	f.hit("NodeInspect")
	f.nodeInspected = append(f.nodeInspected, id)
	n, ok := f.nodes[id]
	if !ok {
		return client.NodeInspectResult{}, cerrdefs.ErrNotFound
	}
	return client.NodeInspectResult{Node: n}, nil
}

func newFake() *fakeAPI {
	return &fakeAPI{
		inspect:    map[string]container.InspectResponse{},
		inspectErr: map[string]error{},
		tasks:      map[string][]swarm.Task{},
		nodes:      map[string]swarm.Node{},
		calls:      map[string]int{},
	}
}

func boolPtr(b bool) *bool { return &b }

func ctrInspect(id, name, state string, labels map[string]string) container.InspectResponse {
	return container.InspectResponse{
		ID:         id,
		Name:       "/" + name,
		State:      &container.State{Status: container.ContainerState(state)},
		Config:     &container.Config{Labels: labels},
		HostConfig: &container.HostConfig{NetworkMode: "bridge"},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"bridge": {NetworkID: "net-bridge", IPAddress: netip.MustParseAddr("172.17.0.2")},
			},
		},
	}
}

func TestCollectContainers(t *testing.T) {
	healthy := ctrInspect("id-b", "b-web", "running", map[string]string{"traefik.enable": "true"})
	healthy.State.Health = &container.Health{Status: container.Healthy}
	healthy.NetworkSettings.Ports = network.PortMap{
		network.MustParsePort("443/tcp"): []network.PortBinding{{HostIP: netip.MustParseAddr("0.0.0.0"), HostPort: "8443"}},
		network.MustParsePort("80/tcp"):  nil,
		network.MustParsePort("53/udp"):  nil,
	}

	unhealthy := ctrInspect("id-a", "a-api", "running", nil)
	unhealthy.State.Health = &container.Health{Status: container.Unhealthy}

	stopped := ctrInspect("id-c", "c-old", "exited", nil)

	hostNet := ctrInspect("id-d", "d-host", "running", nil)
	hostNet.HostConfig.NetworkMode = "host"

	attached := ctrInspect("id-e", "e-sidecar", "running", nil)
	attached.HostConfig.NetworkMode = "container:id-b"

	tests := []struct {
		name    string
		setup   func(*fakeAPI)
		wantIDs []string
		wantErr bool
		check   func(t *testing.T, got []Container)
	}{
		{
			name: "states health labels sorted by name",
			setup: func(f *fakeAPI) {
				f.containers = []container.Summary{{ID: "id-b"}, {ID: "id-c"}, {ID: "id-a"}}
				f.inspect = map[string]container.InspectResponse{"id-a": unhealthy, "id-b": healthy, "id-c": stopped}
			},
			wantIDs: []string{"id-a", "id-b", "id-c"},
			check: func(t *testing.T, got []Container) {
				if got[0].Health != "unhealthy" || got[0].Status != "running" {
					t.Errorf("a-api: health=%q status=%q", got[0].Health, got[0].Status)
				}
				if got[1].Health != "healthy" || got[1].Labels["traefik.enable"] != "true" {
					t.Errorf("b-web: health=%q labels=%v", got[1].Health, got[1].Labels)
				}
				if got[2].Status != "exited" || got[2].Name != "c-old" || got[2].ServiceName != "c-old" {
					t.Errorf("c-old: %+v", got[2])
				}
				if got[1].Source != SourceContainer || got[1].Swarm != (SwarmInfo{}) {
					t.Errorf("source/swarm not zero for container: %+v", got[1])
				}
			},
		},
		{
			name: "ports sorted and host bindings kept",
			setup: func(f *fakeAPI) {
				f.containers = []container.Summary{{ID: "id-b"}}
				f.inspect = map[string]container.InspectResponse{"id-b": healthy}
			},
			wantIDs: []string{"id-b"},
			check: func(t *testing.T, got []Container) {
				want := []Port{
					{Port: 53, Proto: "udp"},
					{Port: 80, Proto: "tcp"},
					{Port: 443, Proto: "tcp", HostIP: "0.0.0.0", HostPort: "8443"},
				}
				if !reflect.DeepEqual(got[0].Ports, want) {
					t.Errorf("ports = %+v, want %+v", got[0].Ports, want)
				}
			},
		},
		{
			name: "networks populated",
			setup: func(f *fakeAPI) {
				f.containers = []container.Summary{{ID: "id-a"}}
				f.inspect = map[string]container.InspectResponse{"id-a": unhealthy}
			},
			wantIDs: []string{"id-a"},
			check: func(t *testing.T, got []Container) {
				want := map[string]Network{"bridge": {ID: "net-bridge", Name: "bridge", Addr: "172.17.0.2"}}
				if !reflect.DeepEqual(got[0].Networks, want) {
					t.Errorf("networks = %+v", got[0].Networks)
				}
			},
		},
		{
			name: "host network mode",
			setup: func(f *fakeAPI) {
				f.containers = []container.Summary{{ID: "id-d"}}
				f.inspect = map[string]container.InspectResponse{"id-d": hostNet}
			},
			wantIDs: []string{"id-d"},
			check: func(t *testing.T, got []Container) {
				if !got[0].IsHostNetwork() {
					t.Errorf("IsHostNetwork = false, NetworkMode=%q", got[0].NetworkMode)
				}
			},
		},
		{
			name: "container network mode resolves connected networks",
			setup: func(f *fakeAPI) {
				f.containers = []container.Summary{{ID: "id-e"}}
				f.inspect = map[string]container.InspectResponse{"id-e": attached, "id-b": healthy}
			},
			wantIDs: []string{"id-e"},
			check: func(t *testing.T, got []Container) {
				target, ok := got[0].IsContainerNetwork()
				if !ok || target != "id-b" {
					t.Errorf("IsContainerNetwork = %q, %v", target, ok)
				}
				if got[0].ConnectedContainerID != "id-b" {
					t.Errorf("ConnectedContainerID = %q", got[0].ConnectedContainerID)
				}
				if _, ok := got[0].ConnectedNetworks["bridge"]; !ok {
					t.Errorf("ConnectedNetworks = %+v", got[0].ConnectedNetworks)
				}
			},
		},
		{
			name: "inspect failure skips only that container",
			setup: func(f *fakeAPI) {
				f.containers = []container.Summary{{ID: "id-a"}, {ID: "id-x"}, {ID: "id-b"}}
				f.inspect = map[string]container.InspectResponse{"id-a": unhealthy, "id-b": healthy}
				f.inspectErr = map[string]error{"id-x": errors.New("boom")}
			},
			wantIDs: []string{"id-a", "id-b"},
		},
		{
			name: "vanished container is skipped",
			setup: func(f *fakeAPI) {
				f.containers = []container.Summary{{ID: "id-gone"}, {ID: "id-b"}}
				f.inspect = map[string]container.InspectResponse{"id-b": healthy}
			},
			wantIDs: []string{"id-b"},
		},
		{
			name: "unresolvable connected container keeps entry without networks",
			setup: func(f *fakeAPI) {
				f.containers = []container.Summary{{ID: "id-e"}}
				f.inspect = map[string]container.InspectResponse{"id-e": attached}
			},
			wantIDs: []string{"id-e"},
			check: func(t *testing.T, got []Container) {
				if got[0].ConnectedContainerID != "" || got[0].ConnectedNetworks != nil {
					t.Errorf("expected unresolved connected container, got %+v", got[0])
				}
			},
		},
		{
			name: "list failure is an error",
			setup: func(f *fakeAPI) {
				f.containerErr = errors.New("daemon down")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			tt.setup(f)
			cfg := config.DockerConfig{Mode: "docker"}
			got, err := NewCollector(f, cfg).Collect(context.Background())
			if tt.wantErr {
				if err == nil || got != nil {
					t.Fatalf("want error and nil result, got %v, %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			ids := make([]string, len(got))
			for i, c := range got {
				ids[i] = c.ID
			}
			if !reflect.DeepEqual(ids, tt.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tt.wantIDs)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// Fixture for swarm tests: an overlay network "ov" (swarm scope), a non-swarm
// network "local" that appears on a task but must be filtered out, and nodes.
func swarmFixture() *fakeAPI {
	f := newFake()
	f.networks = []network.Summary{{Network: network.Network{ID: "net-ov", Name: "ov", Scope: "swarm"}}}
	f.nodes = map[string]swarm.Node{
		"node1": {ID: "node1", Status: swarm.NodeStatus{Addr: "10.0.0.1"}},
		"node2": {ID: "node2", Status: swarm.NodeStatus{Addr: "10.0.0.2"}},
	}
	web := swarm.Service{
		ID: "svc-web",
		Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{
			Name:   "web",
			Labels: map[string]string{"traefik.enable": "true"},
		}},
		Endpoint: swarm.Endpoint{
			VirtualIPs: []swarm.EndpointVirtualIP{
				{NetworkID: "net-ov", Addr: netip.MustParsePrefix("10.0.1.5/24")},
				{NetworkID: "net-ingress-missing", Addr: netip.MustParsePrefix("10.255.0.9/16")},
			},
			Ports: []swarm.PortConfig{{Protocol: "tcp", TargetPort: 80, PublishedPort: 8080}},
		},
	}

	api := swarm.Service{
		ID: "svc-api",
		Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{
			Name:   "api",
			Labels: map[string]string{"traefik.swarm.lbswarm": "true"},
		}},
	}
	glob := swarm.Service{
		ID: "svc-agent",
		Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{
			Name:   "agent",
			Labels: map[string]string{},
		}},
	}
	f.services = []swarm.Service{web, api, glob}

	att := func(netID, addr string) swarm.NetworkAttachment {
		return swarm.NetworkAttachment{
			Network:   swarm.Network{ID: netID},
			Addresses: []netip.Prefix{netip.MustParsePrefix(addr)},
		}
	}
	f.tasks["svc-web"] = []swarm.Task{
		{ID: "t-web-2", ServiceID: "svc-web", Slot: 2, NodeID: "node2",
			Status: swarm.TaskStatus{State: swarm.TaskStateRunning}, NetworksAttachments: []swarm.NetworkAttachment{att("net-ov", "10.0.1.7/24"), att("net-local", "192.168.1.5/24")}},
		{ID: "t-web-1", ServiceID: "svc-web", Slot: 1, NodeID: "node1",
			Status: swarm.TaskStatus{State: swarm.TaskStateRunning}, NetworksAttachments: []swarm.NetworkAttachment{att("net-ov", "10.0.1.6/24")}},
		{ID: "t-web-3", ServiceID: "svc-web", Slot: 3, NodeID: "node1",
			Status: swarm.TaskStatus{State: swarm.TaskStateFailed}, NetworksAttachments: []swarm.NetworkAttachment{att("net-ov", "10.0.1.8/24")}},
	}
	f.tasks["svc-agent"] = []swarm.Task{
		{ID: "t-agent-x", ServiceID: "svc-agent", Slot: 0, NodeID: "node1",
			Status: swarm.TaskStatus{State: swarm.TaskStateRunning}, NetworksAttachments: []swarm.NetworkAttachment{att("net-ov", "10.0.1.9/24")}},
	}
	return f
}

func TestCollectSwarm(t *testing.T) {
	f := swarmFixture()
	got, err := NewCollector(f, config.DockerConfig{Mode: "swarm"}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	byID := map[string]Container{}
	var names []string
	for _, c := range got {
		byID[c.ID] = c
		names = append(names, c.Name)
	}
	wantNames := []string{"agent", "agent.t-agent-x", "api", "web", "web.1", "web.2"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("names = %v, want %v", names, wantNames)
	}

	vip := byID["svc-web"]
	if !vip.Swarm.VIP || vip.Swarm.Task || vip.Source != SourceSwarm || vip.Status != "" {
		t.Errorf("web VIP entry: %+v", vip.Swarm)
	}
	wantVIP := map[string]Network{"ov": {ID: "net-ov", Name: "ov", Addr: "10.0.1.5"}}
	if !reflect.DeepEqual(vip.Networks, wantVIP) {
		t.Errorf("web VIP networks = %+v, want %+v", vip.Networks, wantVIP)
	}
	if !reflect.DeepEqual(vip.Ports, []Port{{Port: 80, Proto: "tcp"}}) {
		t.Errorf("web ports = %+v", vip.Ports)
	}

	t1 := byID["t-web-1"]
	if t1.Name != "web.1" || !t1.Swarm.Task || t1.Swarm.VIP || t1.NodeIP != "10.0.0.1" || t1.ServiceName != "web" {
		t.Errorf("web.1 entry: %+v", t1)
	}
	if _, ok := t1.Networks["ov"]; !ok || len(t1.Networks) != 1 {
		t.Errorf("web.1 networks = %+v", t1.Networks)
	}
	if t1.Networks["ov"].Addr != "10.0.1.6" {
		t.Errorf("web.1 addr = %q", t1.Networks["ov"].Addr)
	}

	t2 := byID["t-web-2"]
	if t2.Networks["ov"].Addr != "10.0.1.7" || t2.NodeIP != "10.0.0.2" {
		t.Errorf("web.2 entry: %+v", t2)
	}
	if _, ok := byID["t-web-3"]; ok {
		t.Error("failed task must be ignored")
	}

	g := byID["t-agent-x"]
	if g.Name != "agent.t-agent-x" {
		t.Errorf("global task name = %q", g.Name)
	}

	if _, ok := byID["svc-api"]; !ok {
		t.Error("lbswarm service should still produce a VIP entry")
	}
	for _, c := range got {
		if c.Swarm.ServiceID == "svc-api" && c.Swarm.Task {
			t.Error("lbswarm service must not produce task entries")
		}
	}

	if f.calls["TaskList"] != 2 {
		t.Errorf("TaskList calls = %d, want 2 (web, agent)", f.calls["TaskList"])
	}
}

func TestCollectSwarmNodeLookupCached(t *testing.T) {
	f := swarmFixture()
	if _, err := NewCollector(f, config.DockerConfig{Mode: "swarm"}).Collect(context.Background()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// node1 hosts web.1 and agent task; it must be inspected once.
	count := map[string]int{}
	for _, id := range f.nodeInspected {
		count[id]++
	}
	if count["node1"] != 1 || count["node2"] != 1 {
		t.Errorf("node inspections = %v, want one each", count)
	}
}

func TestCollectSwarmNodeFailureSkipsTask(t *testing.T) {
	f := swarmFixture()
	delete(f.nodes, "node2")
	got, err := NewCollector(f, config.DockerConfig{Mode: "swarm"}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, c := range got {
		if c.ID == "t-web-2" {
			t.Error("task on unresolvable node should be skipped")
		}
	}
}

func TestCollectSwarmListFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeAPI)
	}{
		{"networks", func(f *fakeAPI) { f.networkErr = errors.New("x") }},
		{"services", func(f *fakeAPI) { f.serviceErr = errors.New("x") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := swarmFixture()
			tt.setup(f)
			got, err := NewCollector(f, config.DockerConfig{Mode: "swarm"}).Collect(context.Background())
			if err == nil || got != nil {
				t.Fatalf("want error and nil result, got %d entries, err %v", len(got), err)
			}
		})
	}
}

func TestCollectBothFailingSourceReturnsError(t *testing.T) {
	f := swarmFixture()
	f.containers = []container.Summary{{ID: "id-b"}}
	f.inspect = map[string]container.InspectResponse{"id-b": ctrInspect("id-b", "b", "running", nil)}
	f.serviceErr = errors.New("swarm unavailable")

	got, err := NewCollector(f, config.DockerConfig{Mode: "both"}).Collect(context.Background())
	if err == nil {
		t.Fatal("want error when swarm listing fails")
	}
	if got != nil {
		t.Errorf("want no partial results, got %d", len(got))
	}
}

func TestCollectBothEnabled(t *testing.T) {
	f := swarmFixture()
	f.containers = []container.Summary{{ID: "id-b"}}
	f.inspect = map[string]container.InspectResponse{"id-b": ctrInspect("id-b", "b-local", "running", nil)}
	got, err := NewCollector(f, config.DockerConfig{Mode: "both"}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("got %d entries, want 7", len(got))
	}
	for _, c := range got {
		if c.Source == SourceContainer && c.Name != "b-local" {
			t.Errorf("container entry %q", c.Name)
		}
	}
}

func TestCollectDisabledMakesNoCalls(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.DockerConfig
	}{
		{"mode off", config.DockerConfig{Mode: "off"}},
		{"containers and swarm explicitly false", config.DockerConfig{Mode: "both", Containers: boolPtr(false), Swarm: boolPtr(false)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := swarmFixture()
			f.containers = []container.Summary{{ID: "id-b"}}
			got, err := NewCollector(f, tt.cfg).Collect(context.Background())
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if got == nil || len(got) != 0 {
				t.Errorf("got %v, want empty non-nil slice", got)
			}
			if len(f.calls) != 0 {
				t.Errorf("unexpected API calls: %v", f.calls)
			}
		})
	}
}

func TestCollectDeterministicOrder(t *testing.T) {
	f := newFake()
	f.containers = []container.Summary{{ID: "id-2"}, {ID: "id-1"}}
	f.inspect = map[string]container.InspectResponse{
		"id-1": ctrInspect("id-1", "same", "running", nil),
		"id-2": ctrInspect("id-2", "same", "running", nil),
	}
	c := NewCollector(f, config.DockerConfig{Mode: "docker"})
	for range 5 {
		got, err := c.Collect(context.Background())
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		if got[0].ID != "id-1" || got[1].ID != "id-2" {
			t.Fatalf("order = %s, %s", got[0].ID, got[1].ID)
		}
	}
}

func TestCollectContextCanceled(t *testing.T) {
	f := swarmFixture()
	f.containers = []container.Summary{{ID: "id-b"}}
	f.inspect = map[string]container.InspectResponse{"id-b": ctrInspect("id-b", "b", "running", nil)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, mode := range []string{"docker", "swarm", "both"} {
		got, err := NewCollector(f, config.DockerConfig{Mode: mode}).Collect(ctx)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Errorf("mode %s: got %v, %v; want context.Canceled", mode, got, err)
		}
	}
}

func TestContainerHelpers(t *testing.T) {
	tests := []struct {
		mode     string
		wantHost bool
		wantTgt  string
		wantCont bool
	}{
		{"host", true, "", false},
		{"container:abc", false, "abc", true},
		{"bridge", false, "", false},
		{"", false, "", false},
	}
	for _, tt := range tests {
		c := Container{NetworkMode: tt.mode}
		tgt, ok := c.IsContainerNetwork()
		if c.IsHostNetwork() != tt.wantHost || tgt != tt.wantTgt || ok != tt.wantCont {
			t.Errorf("mode %q: host=%v target=%q ok=%v", tt.mode, c.IsHostNetwork(), tgt, ok)
		}
	}
}
