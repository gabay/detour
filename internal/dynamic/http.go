package dynamic

import (
	"reflect"
	"slices"
)

// HTTPConfiguration contains all HTTP configuration.
type HTTPConfiguration struct {
	Routers           map[string]*Router           `json:"routers,omitempty" yaml:"routers,omitempty"`
	Services          map[string]*Service          `json:"services,omitempty" yaml:"services,omitempty"`
	Middlewares       map[string]*Middleware       `json:"middlewares,omitempty" yaml:"middlewares,omitempty"`
	ServersTransports map[string]*ServersTransport `json:"serversTransports,omitempty" yaml:"serversTransports,omitempty"`
}

// Router is a Traefik HTTP router.
type Router struct {
	EntryPoints []string         `json:"entryPoints,omitempty" yaml:"entryPoints,omitempty"`
	Middlewares []string         `json:"middlewares,omitempty" yaml:"middlewares,omitempty"`
	Service     string           `json:"service,omitempty" yaml:"service,omitempty"`
	Rule        string           `json:"rule,omitempty" yaml:"rule,omitempty"`
	RuleSyntax  string           `json:"ruleSyntax,omitempty" yaml:"ruleSyntax,omitempty"`
	Priority    int              `json:"priority,omitempty" yaml:"priority,omitempty"`
	TLS         *RouterTLSConfig `json:"tls,omitempty" yaml:"tls,omitempty" label:"allowEmpty"`
}

// RouterTLSConfig is the TLS configuration of an HTTP router.
type RouterTLSConfig struct {
	Options      string   `json:"options,omitempty" yaml:"options,omitempty"`
	CertResolver string   `json:"certResolver,omitempty" yaml:"certResolver,omitempty"`
	Domains      []Domain `json:"domains,omitempty" yaml:"domains,omitempty"`
}

// Domain is a main domain name with its SANs.
type Domain struct {
	Main string   `json:"main,omitempty" yaml:"main,omitempty"`
	SANs []string `json:"sans,omitempty" yaml:"sans,omitempty"`
}

// Service is a Traefik HTTP service. Exactly one of its kinds is normally set.
type Service struct {
	LoadBalancer *ServersLoadBalancer `json:"loadBalancer,omitempty" yaml:"loadBalancer,omitempty" label:"allowEmpty"`
	Weighted     *WeightedRoundRobin  `json:"weighted,omitempty" yaml:"weighted,omitempty"`
	Mirroring    *Mirroring           `json:"mirroring,omitempty" yaml:"mirroring,omitempty"`
	Failover     *Failover            `json:"failover,omitempty" yaml:"failover,omitempty"`
}

// ServersLoadBalancer is the load balancer of an HTTP service.
type ServersLoadBalancer struct {
	Sticky             *Sticky             `json:"sticky,omitempty" yaml:"sticky,omitempty" label:"allowEmpty"`
	Servers            []Server            `json:"servers,omitempty" yaml:"servers,omitempty" label:"allowEmpty" label-slice-as-struct:"server"`
	HealthCheck        *ServerHealthCheck  `json:"healthCheck,omitempty" yaml:"healthCheck,omitempty"`
	PassHostHeader     *bool               `json:"passHostHeader,omitempty" yaml:"passHostHeader,omitempty"`
	ResponseForwarding *ResponseForwarding `json:"responseForwarding,omitempty" yaml:"responseForwarding,omitempty"`
	ServersTransport   string              `json:"serversTransport,omitempty" yaml:"serversTransport,omitempty"`
}

// Server is a backend server of a load balancer.
//
// Scheme and Port are label-only inputs (for example traefik.http.services.x.loadbalancer.server.port).
// They are never serialized: a later compilation step turns them into URL.
type Server struct {
	URL          string `json:"url,omitempty" yaml:"url,omitempty"`
	Weight       *int   `json:"weight,omitempty" yaml:"weight,omitempty"`
	PreservePath bool   `json:"preservePath,omitempty" yaml:"preservePath,omitempty"`
	Scheme       string `json:"-" yaml:"-"`
	Port         string `json:"-" yaml:"-"`
}

// ResponseForwarding configures how responses are flushed to the client.
type ResponseForwarding struct {
	FlushInterval string `json:"flushInterval,omitempty" yaml:"flushInterval,omitempty"`
}

// Sticky configures sticky sessions.
type Sticky struct {
	Cookie *Cookie `json:"cookie,omitempty" yaml:"cookie,omitempty" label:"allowEmpty"`
}

// Cookie is the sticky session cookie.
type Cookie struct {
	Name     string  `json:"name,omitempty" yaml:"name,omitempty"`
	Secure   bool    `json:"secure,omitempty" yaml:"secure,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty" yaml:"httpOnly,omitempty"`
	SameSite string  `json:"sameSite,omitempty" yaml:"sameSite,omitempty"`
	MaxAge   int     `json:"maxAge,omitempty" yaml:"maxAge,omitempty"`
	Path     *string `json:"path,omitempty" yaml:"path,omitempty"`
}

// ServerHealthCheck configures active health checks of a load balancer.
type ServerHealthCheck struct {
	Scheme          string            `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Path            string            `json:"path,omitempty" yaml:"path,omitempty"`
	Port            int               `json:"port,omitempty" yaml:"port,omitempty"`
	Interval        string            `json:"interval,omitempty" yaml:"interval,omitempty"`
	Timeout         string            `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Hostname        string            `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	FollowRedirects *bool             `json:"followRedirects,omitempty" yaml:"followRedirects,omitempty"`
	Headers         map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Method          string            `json:"method,omitempty" yaml:"method,omitempty"`
	Status          int               `json:"status,omitempty" yaml:"status,omitempty"`
	Mode            string            `json:"mode,omitempty" yaml:"mode,omitempty"`
}

// WeightedRoundRobin balances traffic between other services by weight.
type WeightedRoundRobin struct {
	Services []WRRService `json:"services,omitempty" yaml:"services,omitempty"`
	Sticky   *Sticky      `json:"sticky,omitempty" yaml:"sticky,omitempty" label:"allowEmpty"`
}

// WRRService is a weighted reference to another service.
type WRRService struct {
	Name   string `json:"name,omitempty" yaml:"name,omitempty"`
	Weight *int   `json:"weight,omitempty" yaml:"weight,omitempty"`
}

// Mirroring duplicates traffic from a service to mirror services.
type Mirroring struct {
	Service     string          `json:"service,omitempty" yaml:"service,omitempty"`
	Mirrors     []MirrorService `json:"mirrors,omitempty" yaml:"mirrors,omitempty"`
	MaxBodySize *int64          `json:"maxBodySize,omitempty" yaml:"maxBodySize,omitempty"`
}

// MirrorService is a mirror target and the percentage of traffic it receives.
type MirrorService struct {
	Name    string `json:"name,omitempty" yaml:"name,omitempty"`
	Percent int    `json:"percent,omitempty" yaml:"percent,omitempty"`
}

// Failover routes to a fallback service when the main service is unhealthy.
type Failover struct {
	Service  string `json:"service,omitempty" yaml:"service,omitempty"`
	Fallback string `json:"fallback,omitempty" yaml:"fallback,omitempty"`
}

// ServersTransport configures outgoing connections to backend servers.
type ServersTransport struct {
	ServerName          string              `json:"serverName,omitempty" yaml:"serverName,omitempty"`
	InsecureSkipVerify  bool                `json:"insecureSkipVerify,omitempty" yaml:"insecureSkipVerify,omitempty"`
	RootCAs             []string            `json:"rootCAs,omitempty" yaml:"rootCAs,omitempty"`
	Certificates        []Certificate       `json:"certificates,omitempty" yaml:"certificates,omitempty"`
	MaxIdleConnsPerHost int                 `json:"maxIdleConnsPerHost,omitempty" yaml:"maxIdleConnsPerHost,omitempty"`
	ForwardingTimeouts  *ForwardingTimeouts `json:"forwardingTimeouts,omitempty" yaml:"forwardingTimeouts,omitempty"`
	DisableHTTP2        bool                `json:"disableHTTP2,omitempty" yaml:"disableHTTP2,omitempty"`
	PeerCertURI         string              `json:"peerCertURI,omitempty" yaml:"peerCertURI,omitempty"`
}

// ForwardingTimeouts holds the timeouts of a ServersTransport.
type ForwardingTimeouts struct {
	DialTimeout           string `json:"dialTimeout,omitempty" yaml:"dialTimeout,omitempty"`
	ResponseHeaderTimeout string `json:"responseHeaderTimeout,omitempty" yaml:"responseHeaderTimeout,omitempty"`
	IdleConnTimeout       string `json:"idleConnTimeout,omitempty" yaml:"idleConnTimeout,omitempty"`
	ReadIdleTimeout       string `json:"readIdleTimeout,omitempty" yaml:"readIdleTimeout,omitempty"`
	PingTimeout           string `json:"pingTimeout,omitempty" yaml:"pingTimeout,omitempty"`
}

// Merge appends the servers of other to lb when both load balancers are otherwise
// identical (DeepEqual apart from Servers). Servers already present (same URL, or
// deep-equal when the URL is empty) are not duplicated. It returns false and leaves
// lb untouched when the load balancers differ otherwise.
func (lb *ServersLoadBalancer) Merge(other *ServersLoadBalancer) bool {
	if lb == nil || other == nil {
		return lb == other
	}

	a, b := *lb, *other
	a.Servers, b.Servers = nil, nil
	if !reflect.DeepEqual(a, b) {
		return false
	}

	for _, s := range other.Servers {
		if !slices.ContainsFunc(lb.Servers, func(e Server) bool {
			return reflect.DeepEqual(e, s) || (s.URL != "" && e.URL == s.URL)
		}) {
			lb.Servers = append(lb.Servers, s)
		}
	}

	return true
}

// Merge merges the load balancer of other into s, see ServersLoadBalancer.Merge.
// When either side has no load balancer, or the non-load-balancer parts differ, it
// reports whether s and other are DeepEqual, without mutating s.
func (s *Service) Merge(other *Service) bool {
	if s == nil || other == nil || s.LoadBalancer == nil || other.LoadBalancer == nil {
		return reflect.DeepEqual(s, other)
	}

	a, b := *s, *other
	a.LoadBalancer, b.LoadBalancer = nil, nil
	if !reflect.DeepEqual(a, b) {
		return false
	}

	return s.LoadBalancer.Merge(other.LoadBalancer)
}
