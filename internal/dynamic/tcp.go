package dynamic

import (
	"reflect"
	"slices"
)

// TCPConfiguration contains all TCP configuration.
type TCPConfiguration struct {
	Routers           map[string]*TCPRouter           `json:"routers,omitempty" yaml:"routers,omitempty"`
	Services          map[string]*TCPService          `json:"services,omitempty" yaml:"services,omitempty"`
	Middlewares       map[string]*TCPMiddleware       `json:"middlewares,omitempty" yaml:"middlewares,omitempty"`
	ServersTransports map[string]*TCPServersTransport `json:"serversTransports,omitempty" yaml:"serversTransports,omitempty"`
}

// TCPRouter is a Traefik TCP router.
type TCPRouter struct {
	EntryPoints []string            `json:"entryPoints,omitempty" yaml:"entryPoints,omitempty"`
	Middlewares []string            `json:"middlewares,omitempty" yaml:"middlewares,omitempty"`
	Service     string              `json:"service,omitempty" yaml:"service,omitempty"`
	Rule        string              `json:"rule,omitempty" yaml:"rule,omitempty"`
	Priority    int                 `json:"priority,omitempty" yaml:"priority,omitempty"`
	TLS         *RouterTCPTLSConfig `json:"tls,omitempty" yaml:"tls,omitempty" label:"allowEmpty"`
}

// RouterTCPTLSConfig is the TLS configuration of a TCP router.
type RouterTCPTLSConfig struct {
	Passthrough  bool     `json:"passthrough,omitempty" yaml:"passthrough,omitempty"`
	Options      string   `json:"options,omitempty" yaml:"options,omitempty"`
	CertResolver string   `json:"certResolver,omitempty" yaml:"certResolver,omitempty"`
	Domains      []Domain `json:"domains,omitempty" yaml:"domains,omitempty"`
}

// TCPService is a Traefik TCP service.
type TCPService struct {
	LoadBalancer *TCPServersLoadBalancer `json:"loadBalancer,omitempty" yaml:"loadBalancer,omitempty" label:"allowEmpty"`
	Weighted     *TCPWeightedRoundRobin  `json:"weighted,omitempty" yaml:"weighted,omitempty"`
}

// TCPServersLoadBalancer is the load balancer of a TCP service.
type TCPServersLoadBalancer struct {
	Servers          []TCPServer    `json:"servers,omitempty" yaml:"servers,omitempty" label:"allowEmpty" label-slice-as-struct:"server"`
	ServersTransport string         `json:"serversTransport,omitempty" yaml:"serversTransport,omitempty"`
	ProxyProtocol    *ProxyProtocol `json:"proxyProtocol,omitempty" yaml:"proxyProtocol,omitempty"`
	TerminationDelay *int           `json:"terminationDelay,omitempty" yaml:"terminationDelay,omitempty"`
}

// TCPServer is a backend server of a TCP load balancer.
//
// Port is a label-only input; a later compilation step folds it into Address.
type TCPServer struct {
	Address string `json:"address,omitempty" yaml:"address,omitempty"`
	Port    string `json:"-" yaml:"-"`
	TLS     bool   `json:"tls,omitempty" yaml:"tls,omitempty"`
}

// ProxyProtocol enables the PROXY protocol towards a backend.
type ProxyProtocol struct {
	Version int `json:"version,omitempty" yaml:"version,omitempty"`
}

// TCPWeightedRoundRobin balances TCP traffic between other services by weight.
type TCPWeightedRoundRobin struct {
	Services []TCPWRRService `json:"services,omitempty" yaml:"services,omitempty"`
}

// TCPWRRService is a weighted reference to another TCP service.
type TCPWRRService struct {
	Name   string `json:"name,omitempty" yaml:"name,omitempty"`
	Weight *int   `json:"weight,omitempty" yaml:"weight,omitempty"`
}

// TCPMiddleware is a Traefik TCP middleware.
type TCPMiddleware struct {
	IPAllowList  *TCPIPAllowList  `json:"ipAllowList,omitempty" yaml:"ipAllowList,omitempty"`
	InFlightConn *TCPInFlightConn `json:"inFlightConn,omitempty" yaml:"inFlightConn,omitempty"`
}

// TCPIPAllowList restricts TCP access to a set of client IP ranges.
type TCPIPAllowList struct {
	SourceRange []string `json:"sourceRange,omitempty" yaml:"sourceRange,omitempty"`
}

// TCPInFlightConn limits the number of concurrent TCP connections.
type TCPInFlightConn struct {
	Amount int64 `json:"amount,omitempty" yaml:"amount,omitempty"`
}

// TCPServersTransport configures outgoing TCP connections to backend servers.
type TCPServersTransport struct {
	DialKeepAlive    string                  `json:"dialKeepAlive,omitempty" yaml:"dialKeepAlive,omitempty"`
	DialTimeout      string                  `json:"dialTimeout,omitempty" yaml:"dialTimeout,omitempty"`
	TerminationDelay *int                    `json:"terminationDelay,omitempty" yaml:"terminationDelay,omitempty"`
	TLS              *TCPServersTransportTLS `json:"tls,omitempty" yaml:"tls,omitempty"`
}

// TCPServersTransportTLS holds the TLS settings of a TCP ServersTransport.
type TCPServersTransportTLS struct {
	ServerName         string        `json:"serverName,omitempty" yaml:"serverName,omitempty"`
	InsecureSkipVerify bool          `json:"insecureSkipVerify,omitempty" yaml:"insecureSkipVerify,omitempty"`
	RootCAs            []string      `json:"rootCAs,omitempty" yaml:"rootCAs,omitempty"`
	Certificates       []Certificate `json:"certificates,omitempty" yaml:"certificates,omitempty"`
	PeerCertURI        string        `json:"peerCertURI,omitempty" yaml:"peerCertURI,omitempty"`
}

// Merge appends the servers of other to lb when both load balancers are otherwise
// identical. Servers already present (same address, or deep-equal when the address
// is empty) are not duplicated. It returns false and leaves lb untouched otherwise.
func (lb *TCPServersLoadBalancer) Merge(other *TCPServersLoadBalancer) bool {
	if lb == nil || other == nil {
		return lb == other
	}

	a, b := *lb, *other
	a.Servers, b.Servers = nil, nil
	if !reflect.DeepEqual(a, b) {
		return false
	}

	for _, s := range other.Servers {
		if !slices.ContainsFunc(lb.Servers, func(e TCPServer) bool {
			return reflect.DeepEqual(e, s) || (s.Address != "" && e.Address == s.Address)
		}) {
			lb.Servers = append(lb.Servers, s)
		}
	}

	return true
}

// Merge merges the load balancer of other into s, see TCPServersLoadBalancer.Merge.
// When either side has no load balancer, or the non-load-balancer parts differ, it
// reports whether s and other are DeepEqual, without mutating s.
func (s *TCPService) Merge(other *TCPService) bool {
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
