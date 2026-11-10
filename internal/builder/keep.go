package builder

import (
	"fmt"

	"detour/internal/config"
	"detour/internal/constraints"
	"detour/internal/docker"
)

// Keep decides whether c becomes a backend. When it returns false, the reason
// explains why, for logging.
func Keep(c docker.Container, opts Options, cfg config.DockerConfig) (bool, string) {
	if !opts.Enable {
		return false, "disabled"
	}
	matched, err := constraints.Match(c.Labels, cfg.Constraints)
	if err != nil {
		return false, fmt.Sprintf("constraint error: %v", err)
	}
	if !matched {
		return false, "constraints not matched"
	}
	if opts.AllowNonRunning {
		return true, ""
	}
	if c.Status != "" && c.Status != "running" {
		return false, "not running"
	}
	if !cfg.AllowEmptyServices && c.Health != "" && c.Health != "healthy" {
		return false, "unhealthy"
	}
	return true, ""
}
