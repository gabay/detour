package builder

import (
	"testing"

	"detour/internal/config"
	"detour/internal/docker"
)

func TestResolveAddress(t *testing.T) {
	nets := map[string]docker.Network{
		"proxy": {Name: "proxy", Addr: "172.18.0.2"},
		"back":  {Name: "back", Addr: "10.0.0.5"},
	}
	bound := []docker.Port{{Port: 80, Proto: "tcp", HostIP: "192.168.1.10", HostPort: "8080"}}
	unspecified := []docker.Port{{Port: 80, Proto: "tcp", HostIP: "0.0.0.0", HostPort: "8080"}}

	tests := []struct {
		name       string
		c          docker.Container
		opts       Options
		cfg        config.DockerConfig
		serverPort string
		wantHost   string
		wantPort   string
		wantErr    bool
	}{
		{
			name: "explicit server port wins", c: docker.Container{Networks: nets, Ports: []docker.Port{{Port: 3000}}},
			opts: Options{Network: "proxy"}, serverPort: "9000", wantHost: "172.18.0.2", wantPort: "9000",
		},
		{
			name: "lowest exposed port", c: docker.Container{Networks: nets, Ports: []docker.Port{{Port: 443}, {Port: 80}}},
			wantHost: "10.0.0.5", wantPort: "80",
		},
		{
			name: "no port is not an error", c: docker.Container{Networks: nets},
			wantHost: "10.0.0.5", wantPort: "",
		},
		{
			name: "bind ip used", c: docker.Container{Networks: nets, Ports: bound},
			cfg: config.DockerConfig{UseBindPortIP: true}, wantHost: "192.168.1.10", wantPort: "8080",
		},
		{
			name: "bind ip ignored when disabled", c: docker.Container{Networks: nets, Ports: bound},
			opts: Options{Network: "proxy"}, wantHost: "172.18.0.2", wantPort: "80",
		},
		{
			name: "unspecified bind ip falls back", c: docker.Container{Networks: nets, Ports: unspecified},
			cfg: config.DockerConfig{UseBindPortIP: true}, opts: Options{Network: "proxy"}, wantHost: "172.18.0.2", wantPort: "80",
		},
		{
			name: "named network", c: docker.Container{Networks: nets},
			opts: Options{Network: "back"}, wantHost: "10.0.0.5",
		},
		{
			name: "missing named network falls back to first by name", c: docker.Container{Networks: nets},
			opts: Options{Network: "nope"}, wantHost: "10.0.0.5",
		},
		{
			name: "host mode with node ip", c: docker.Container{NetworkMode: "host", NodeIP: "10.1.1.1", Networks: nets},
			wantHost: "10.1.1.1",
		},
		{
			name: "host mode without node ip", c: docker.Container{NetworkMode: "host"},
			wantHost: "127.0.0.1",
		},
		{
			name: "container mode uses connected networks", wantHost: "172.18.0.2",
			c: docker.Container{
				NetworkMode:       "container:abc",
				Networks:          map[string]docker.Network{"none": {Name: "none"}},
				ConnectedNetworks: nets,
			},
			opts: Options{Network: "proxy"},
		},
		{
			name: "no networks is an error", c: docker.Container{Name: "lonely"}, wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := ResolveAddress(tt.c, tt.opts, tt.cfg, tt.serverPort)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
			if host != tt.wantHost || port != tt.wantPort {
				t.Errorf("ResolveAddress() = %q:%q, want %q:%q", host, port, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestDefaultPort(t *testing.T) {
	tests := []struct {
		name       string
		ports      []docker.Port
		serverPort string
		want       string
	}{
		{name: "none", want: ""},
		{name: "server port", ports: []docker.Port{{Port: 80}}, serverPort: "1234", want: "1234"},
		{name: "lowest", ports: []docker.Port{{Port: 53}, {Port: 80}}, want: "53"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultPort(docker.Container{Ports: tt.ports}, tt.serverPort); got != tt.want {
				t.Errorf("DefaultPort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"my_app.web-1": "my-app-web-1",
		"--a  b--":     "a-b",
		"plain":        "plain",
		"":             "",
		"héllo wörld":  "héllo-wörld",
	}
	for in, want := range tests {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServiceName(t *testing.T) {
	tests := []struct {
		name string
		c    docker.Container
		want string
	}{
		{
			name: "compose labels",
			c: docker.Container{ServiceName: "shop-web-1", Labels: map[string]string{
				"com.docker.compose.project": "shop", "com.docker.compose.service": "web",
			}},
			want: "shop-web",
		},
		{
			name: "partial compose labels fall back",
			c:    docker.Container{ServiceName: "my.app", Labels: map[string]string{"com.docker.compose.project": "shop"}},
			want: "my-app",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ServiceName(tt.c); got != tt.want {
				t.Errorf("ServiceName() = %q, want %q", got, tt.want)
			}
		})
	}
}
