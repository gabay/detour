package builder

import (
	"detour/internal/docker"
	"detour/internal/rule"
)

const (
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
)

// Normalize replaces every run of characters that are neither letters nor digits
// with a single "-" and trims leading and trailing separators.
func Normalize(name string) string {
	return rule.Normalize(name)
}

// ServiceName returns the router and service base name for c. Compose containers
// are named "<service>_<project>" so that they stay unique across projects.
func ServiceName(c docker.Container) string {
	project, service := c.Labels[composeProjectLabel], c.Labels[composeServiceLabel]
	if project != "" && service != "" {
		return Normalize(service + "_" + project)
	}
	return Normalize(c.ServiceName)
}
