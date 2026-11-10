package dynamic

import (
	"reflect"
	"slices"
)

// UDPConfiguration contains all UDP configuration.
type UDPConfiguration struct {
	Routers  map[string]*UDPRouter  `json:"routers,omitempty" yaml:"routers,omitempty"`
	Services map[string]*UDPService `json:"services,omitempty" yaml:"services,omitempty"`
}

// UDPRouter is a Traefik UDP router.
type UDPRouter struct {
	EntryPoints []string `json:"entryPoints,omitempty" yaml:"entryPoints,omitempty"`
	Service     string   `json:"service,omitempty" yaml:"service,omitempty"`
}

// UDPService is a Traefik UDP service.
type UDPService struct {
	LoadBalancer *UDPServersLoadBalancer `json:"loadBalancer,omitempty" yaml:"loadBalancer,omitempty" label:"allowEmpty"`
	Weighted     *UDPWeightedRoundRobin  `json:"weighted,omitempty" yaml:"weighted,omitempty"`
}

// UDPServersLoadBalancer is the load balancer of a UDP service.
type UDPServersLoadBalancer struct {
	Servers []UDPServer `json:"servers,omitempty" yaml:"servers,omitempty" label:"allowEmpty" label-slice-as-struct:"server"`
}

// UDPServer is a backend server of a UDP load balancer.
//
// Port is a label-only input; a later compilation step folds it into Address.
type UDPServer struct {
	Address string `json:"address,omitempty" yaml:"address,omitempty"`
	Port    string `json:"-" yaml:"-"`
}

// UDPWeightedRoundRobin balances UDP traffic between other services by weight.
type UDPWeightedRoundRobin struct {
	Services []UDPWRRService `json:"services,omitempty" yaml:"services,omitempty"`
}

// UDPWRRService is a weighted reference to another UDP service.
type UDPWRRService struct {
	Name   string `json:"name,omitempty" yaml:"name,omitempty"`
	Weight *int   `json:"weight,omitempty" yaml:"weight,omitempty"`
}

// Merge appends the servers of other to lb when both load balancers are otherwise
// identical. Servers already present (same address, or deep-equal when the address
// is empty) are not duplicated. It returns false and leaves lb untouched otherwise.
func (lb *UDPServersLoadBalancer) Merge(other *UDPServersLoadBalancer) bool {
	if lb == nil || other == nil {
		return lb == other
	}

	a, b := *lb, *other
	a.Servers, b.Servers = nil, nil
	if !reflect.DeepEqual(a, b) {
		return false
	}

	for _, s := range other.Servers {
		if !slices.ContainsFunc(lb.Servers, func(e UDPServer) bool {
			return reflect.DeepEqual(e, s) || (s.Address != "" && e.Address == s.Address)
		}) {
			lb.Servers = append(lb.Servers, s)
		}
	}

	return true
}

// Merge merges the load balancer of other into s, see UDPServersLoadBalancer.Merge.
// When either side has no load balancer, or the non-load-balancer parts differ, it
// reports whether s and other are DeepEqual, without mutating s.
func (s *UDPService) Merge(other *UDPService) bool {
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
