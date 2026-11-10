package builder

import (
	"strings"
	"testing"

	"detour/internal/config"
	"detour/internal/docker"
)

func TestKeep(t *testing.T) {
	tests := []struct {
		name       string
		c          docker.Container
		opts       Options
		cfg        config.DockerConfig
		wantKeep   bool
		wantReason string
	}{
		{name: "running and enabled", c: docker.Container{Status: "running"}, opts: Options{Enable: true}, wantKeep: true},
		{name: "disabled", c: docker.Container{Status: "running"}, wantReason: "disabled"},
		{
			name:       "constraint mismatch",
			c:          docker.Container{Status: "running", Labels: map[string]string{"env": "dev"}},
			opts:       Options{Enable: true},
			cfg:        config.DockerConfig{Constraints: `Label("env", "prod")`},
			wantReason: "constraints not matched",
		},
		{
			name:       "constraint error",
			c:          docker.Container{Status: "running"},
			opts:       Options{Enable: true},
			cfg:        config.DockerConfig{Constraints: `Label(`},
			wantReason: "constraint error",
		},
		{name: "exited", c: docker.Container{Status: "exited"}, opts: Options{Enable: true}, wantReason: "not running"},
		{name: "exited but allowed", c: docker.Container{Status: "exited"}, opts: Options{Enable: true, AllowNonRunning: true}, wantKeep: true},
		{name: "unhealthy", c: docker.Container{Status: "running", Health: "unhealthy"}, opts: Options{Enable: true}, wantReason: "unhealthy"},
		{name: "starting health dropped", c: docker.Container{Status: "running", Health: "starting"}, opts: Options{Enable: true}, wantReason: "unhealthy"},
		{
			name:     "unhealthy allowed by config",
			c:        docker.Container{Status: "running", Health: "unhealthy"},
			opts:     Options{Enable: true},
			cfg:      config.DockerConfig{AllowEmptyServices: true},
			wantKeep: true,
		},
		{name: "swarm entry without status", c: docker.Container{Source: docker.SourceSwarm}, opts: Options{Enable: true}, wantKeep: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keep, reason := Keep(tt.c, tt.opts, tt.cfg)
			if keep != tt.wantKeep {
				t.Fatalf("Keep() = %v (%q), want %v", keep, reason, tt.wantKeep)
			}
			if !strings.HasPrefix(reason, tt.wantReason) {
				t.Errorf("reason = %q, want prefix %q", reason, tt.wantReason)
			}
		})
	}
}
