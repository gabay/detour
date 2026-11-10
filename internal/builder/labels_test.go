package builder

import (
	"testing"

	"detour/internal/config"
	"detour/internal/docker"
)

func TestExtractOptions(t *testing.T) {
	tests := []struct {
		name    string
		c       docker.Container
		cfg     config.DockerConfig
		want    Options
		wantErr bool
	}{
		{
			name: "defaults from config",
			c:    docker.Container{Source: docker.SourceContainer},
			cfg:  config.DockerConfig{ExposedByDefault: true, Network: "proxy"},
			want: Options{Enable: true, Network: "proxy"},
		},
		{
			name: "enable false overrides exposedByDefault",
			c:    docker.Container{Source: docker.SourceContainer, Labels: map[string]string{"traefik.enable": "false"}},
			cfg:  config.DockerConfig{ExposedByDefault: true},
			want: Options{Enable: false},
		},
		{
			name: "container network and allownonrunning",
			c: docker.Container{Source: docker.SourceContainer, Labels: map[string]string{
				"traefik.docker.network":         "backend",
				"traefik.docker.allownonrunning": "true",
			}},
			cfg:  config.DockerConfig{Network: "proxy"},
			want: Options{Enable: false, Network: "backend", AllowNonRunning: true},
		},
		{
			name: "container ignores swarm labels",
			c: docker.Container{Source: docker.SourceContainer, Labels: map[string]string{
				"traefik.swarm.network": "x",
				"traefik.swarm.lbswarm": "true",
			}},
			cfg:  config.DockerConfig{Network: "proxy"},
			want: Options{Network: "proxy"},
		},
		{
			name: "swarm lbswarm and network",
			c: docker.Container{Source: docker.SourceSwarm, Labels: map[string]string{
				"traefik.swarm.lbswarm": "true",
				"traefik.swarm.network": "overlay",
			}},
			cfg:  config.DockerConfig{Network: "proxy", ExposedByDefault: true},
			want: Options{Enable: true, Network: "overlay", LBSwarm: true},
		},
		{
			name: "swarm deprecated docker aliases",
			c: docker.Container{Source: docker.SourceSwarm, Labels: map[string]string{
				"traefik.docker.lbswarm": "true",
				"traefik.docker.network": "overlay",
			}},
			want: Options{Enable: false, Network: "overlay", LBSwarm: true},
		},
		{
			name: "swarm docker and swarm labels conflict",
			c: docker.Container{Source: docker.SourceSwarm, Labels: map[string]string{
				"traefik.docker.network": "a",
				"traefik.swarm.network":  "b",
			}},
			wantErr: true,
		},
		{
			name:    "invalid enable bool",
			c:       docker.Container{Source: docker.SourceContainer, Labels: map[string]string{"traefik.enable": "yes"}},
			wantErr: true,
		},
		{
			name:    "invalid swarm lbswarm bool",
			c:       docker.Container{Source: docker.SourceSwarm, Labels: map[string]string{"traefik.swarm.lbswarm": "maybe"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractOptions(tt.c, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExtractOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ExtractOptions() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
