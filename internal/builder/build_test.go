package builder

import (
	"encoding/json"
	"testing"

	"detour/internal/config"
	"detour/internal/docker"
	"detour/internal/dynamic"
)

func ctr(name, ip string, labels map[string]string) docker.Container {
	return docker.Container{
		ID: name, Name: name, ServiceName: name, Source: docker.SourceContainer, Status: "running",
		Labels:   labels,
		Networks: map[string]docker.Network{"bridge": {Name: "bridge", Addr: ip}},
		Ports:    []docker.Port{{Port: 8080, Proto: "tcp"}},
	}
}

func swarmEntry(id, ip string, vip bool, labels map[string]string) docker.Container {
	c := docker.Container{
		ID: id, Name: "api", ServiceName: "api", Source: docker.SourceSwarm, Labels: labels,
		Swarm:    docker.SwarmInfo{ServiceName: "api", VIP: vip, Task: !vip},
		Networks: map[string]docker.Network{"overlay": {Name: "overlay", Addr: ip}},
	}
	if ip == "" {
		c.Networks = nil
	}
	return c
}

// canonical re-encodes a JSON document through dynamic.Configuration so that
// expected and actual output compare equal regardless of key order.
func canonical(t *testing.T, doc string) string {
	t.Helper()
	var c dynamic.Configuration
	if err := json.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func labels(kv ...string) map[string]string {
	m := map[string]string{"traefik.enable": "true"}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestBuild(t *testing.T) {
	const (
		appPort   = "traefik.http.services.app.loadbalancer.server.port"
		appRouter = "traefik.http.routers.app.rule"
		hostA     = "Host(`a`)"
	)

	tests := []struct {
		name       string
		mutate     func(*config.DockerConfig)
		containers []docker.Container
		want       string
	}{
		{
			name:       "labelled service and router",
			containers: []docker.Container{ctr("web", "10.0.0.2", labels(appPort, "80", appRouter, hostA))},
			want: `{"http":{"routers":{"app":{"rule":"` + hostA + `","service":"app"}},
				"services":{"app":{"loadBalancer":{"servers":[{"url":"http://10.0.0.2:80"}]}}}}}`,
		},
		{
			name:       "default router and service from lowest port",
			containers: []docker.Container{ctr("web", "10.0.0.2", labels())},
			want: `{"http":{"routers":{"web":{"rule":"Host(` + "`web`" + `)","service":"web"}},
				"services":{"web":{"loadBalancer":{"servers":[{"url":"http://10.0.0.2:8080"}]}}}}}`,
		},
		{
			name: "several services and no router creates none",
			containers: []docker.Container{ctr("web", "10.0.0.2", labels(
				appPort, "80", "traefik.http.services.b.loadbalancer.server.port", "81"))},
			want: `{"http":{"services":{"app":{"loadBalancer":{"servers":[{"url":"http://10.0.0.2:80"}]}},
				"b":{"loadBalancer":{"servers":[{"url":"http://10.0.0.2:81"}]}}}}}`,
		},
		{
			name: "stopped with allownonrunning keeps an empty backend",
			containers: []docker.Container{{ID: "web", Name: "web", ServiceName: "web", Status: "exited",
				Labels: labels("traefik.docker.allownonrunning", "true")}},
			want: `{"http":{"routers":{"web":{"rule":"Host(` + "`web`" + `)","service":"web"}},
				"services":{"web":{"loadBalancer":{}}}}}`,
		},
		{
			name: "stopped container is dropped",
			containers: []docker.Container{{ID: "web", Name: "web", ServiceName: "web", Status: "exited",
				Labels: labels()}},
			want: `{}`,
		},
		{
			name:   "unhealthy kept with AllowEmptyServices",
			mutate: func(c *config.DockerConfig) { c.AllowEmptyServices = true },
			containers: []docker.Container{{ID: "web", Name: "web", ServiceName: "web", Status: "running",
				Health: "unhealthy", Labels: labels()}},
			want: `{"http":{"routers":{"web":{"rule":"Host(` + "`web`" + `)","service":"web"}},
				"services":{"web":{"loadBalancer":{}}}}}`,
		},
		{
			name: "unhealthy dropped by default",
			containers: []docker.Container{{ID: "web", Name: "web", ServiceName: "web", Status: "running",
				Health: "unhealthy", Labels: labels()}},
			want: `{}`,
		},
		{
			name: "https scheme and custom URL",
			containers: []docker.Container{
				ctr("web", "10.0.0.2", labels(appPort, "443", "traefik.http.services.app.loadbalancer.server.scheme", "https", appRouter, hostA)),
				ctr("api", "10.0.0.9", labels("traefik.http.services.api.loadbalancer.server.url", "http://backend:9000",
					"traefik.http.routers.api.rule", hostA)),
			},
			want: `{"http":{"routers":{"api":{"rule":"` + hostA + `","service":"api"},"app":{"rule":"` + hostA + `","service":"app"}},
				"services":{"app":{"loadBalancer":{"servers":[{"url":"https://10.0.0.2:443"}]}},
				"api":{"loadBalancer":{"servers":[{"url":"http://backend:9000"}]}}}}}`,
		},
		{
			name: "URL combined with port drops the container",
			containers: []docker.Container{ctr("web", "10.0.0.2", labels(
				"traefik.http.services.app.loadbalancer.server.url", "http://backend:9000", appPort, "80", appRouter, hostA))},
			want: `{}`,
		},
		{
			name: "replicas merge servers and share the router",
			containers: []docker.Container{
				ctr("web-a", "10.0.0.2", labels(appPort, "80", appRouter, hostA)),
				ctr("web-b", "10.0.0.3", labels(appPort, "80", appRouter, hostA)),
			},
			want: `{"http":{"routers":{"app":{"rule":"` + hostA + `","service":"app"}},
				"services":{"app":{"loadBalancer":{"servers":[{"url":"http://10.0.0.2:80"},{"url":"http://10.0.0.3:80"}]}}}}}`,
		},
		{
			name: "TCP router and service",
			containers: []docker.Container{ctr("db", "10.0.0.2", labels(
				"traefik.tcp.routers.db.rule", "HostSNI(`*`)", "traefik.tcp.services.db.loadbalancer.server.port", "5432"))},
			want: `{"tcp":{"routers":{"db":{"rule":"HostSNI(` + "`*`" + `)","service":"db"}},
				"services":{"db":{"loadBalancer":{"servers":[{"address":"10.0.0.2:5432"}]}}}}}`,
		},
		{
			name: "TCP router without rule is dropped",
			containers: []docker.Container{ctr("db", "10.0.0.2", labels(
				"traefik.tcp.routers.db.service", "db", "traefik.tcp.services.db.loadbalancer.server.port", "5432"))},
			want: `{"tcp":{"services":{"db":{"loadBalancer":{"servers":[{"address":"10.0.0.2:5432"}]}}}}}`,
		},
		{
			name: "UDP service",
			containers: []docker.Container{ctr("dns", "10.0.0.2", labels(
				"traefik.udp.services.dns.loadbalancer.server.port", "53"))},
			want: `{"udp":{"services":{"dns":{"loadBalancer":{"servers":[{"address":"10.0.0.2:53"}]}}}}}`,
		},
		{
			name: "middleware labels pass through",
			containers: []docker.Container{ctr("web", "10.0.0.2", labels(appPort, "80", appRouter, hostA,
				"traefik.http.routers.app.middlewares", "strip", "traefik.http.middlewares.strip.stripprefix.prefixes", "/api"))},
			want: `{"http":{"routers":{"app":{"rule":"` + hostA + `","service":"app","middlewares":["strip"]}},
				"middlewares":{"strip":{"stripPrefix":{"prefixes":["/api"]}}},
				"services":{"app":{"loadBalancer":{"servers":[{"url":"http://10.0.0.2:80"}]}}}}}`,
		},
		{
			name: "swarm tasks used, VIP ignored",
			containers: []docker.Container{
				swarmEntry("t1", "10.0.0.2", false, labels(appPort, "80", appRouter, hostA)),
				swarmEntry("t2", "10.0.0.3", false, labels(appPort, "80", appRouter, hostA)),
				swarmEntry("vip", "10.0.1.5", true, labels(appPort, "80", appRouter, hostA)),
			},
			want: `{"http":{"routers":{"app":{"rule":"` + hostA + `","service":"app"}},
				"services":{"app":{"loadBalancer":{"servers":[{"url":"http://10.0.0.2:80"},{"url":"http://10.0.0.3:80"}]}}}}}`,
		},
		{
			name: "swarm lbswarm uses the VIP only",
			containers: []docker.Container{
				swarmEntry("t1", "10.0.0.2", false, labels("traefik.swarm.lbswarm", "true", appPort, "80", appRouter, hostA)),
				swarmEntry("vip", "10.0.1.5", true, labels("traefik.swarm.lbswarm", "true", appPort, "80", appRouter, hostA)),
			},
			want: `{"http":{"routers":{"app":{"rule":"` + hostA + `","service":"app"}},
				"services":{"app":{"loadBalancer":{"servers":[{"url":"http://10.0.1.5:80"}]}}}}}`,
		},
		{
			name: "swarm VIP without network is skipped",
			containers: []docker.Container{
				swarmEntry("vip", "", true, labels("traefik.swarm.lbswarm", "true", appPort, "80", appRouter, hostA)),
			},
			want: `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default().Docker
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(b.Build(tt.containers))
			if err != nil {
				t.Fatal(err)
			}
			if g, w := string(got), canonical(t, tt.want); g != w {
				t.Fatalf("got\n%s\nwant\n%s", g, w)
			}
		})
	}
}

func TestNewRejectsBadDefaultRule(t *testing.T) {
	if _, err := New(config.DockerConfig{DefaultRule: "{{ normalize .Name"}); err == nil {
		t.Fatal("expected an error for an unparsable default rule")
	}
}
