package dynamic

import (
	"reflect"
	"testing"
)

func TestServersLoadBalancerMerge(t *testing.T) {
	tests := []struct {
		name     string
		dst      *ServersLoadBalancer
		other    *ServersLoadBalancer
		want     bool
		wantURLs []string
	}{
		{
			name:     "appends new servers and dedupes by URL",
			dst:      &ServersLoadBalancer{Servers: []Server{{URL: "http://a"}}},
			other:    &ServersLoadBalancer{Servers: []Server{{URL: "http://a"}, {URL: "http://b"}, {URL: "http://b"}}},
			want:     true,
			wantURLs: []string{"http://a", "http://b"},
		},
		{
			name:     "differing options refuse the merge",
			dst:      &ServersLoadBalancer{Servers: []Server{{URL: "http://a"}}, ServersTransport: "x"},
			other:    &ServersLoadBalancer{Servers: []Server{{URL: "http://b"}}, ServersTransport: "y"},
			want:     false,
			wantURLs: []string{"http://a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneOf(tt.dst)
			if got := tt.dst.Merge(tt.other); got != tt.want {
				t.Fatalf("Merge() = %v, want %v", got, tt.want)
			}
			if !tt.want {
				if !reflect.DeepEqual(tt.dst, before) {
					t.Errorf("dst mutated on refused merge: %+v", tt.dst)
				}
				return
			}
			var urls []string
			for _, s := range tt.dst.Servers {
				urls = append(urls, s.URL)
			}
			if !reflect.DeepEqual(urls, tt.wantURLs) {
				t.Errorf("urls = %v, want %v", urls, tt.wantURLs)
			}
		})
	}
}

func cloneOf[T any](p *T) *T {
	return deepCopy(reflect.ValueOf(p)).Interface().(*T)
}

func TestServiceMerge(t *testing.T) {
	lb := func(urls ...string) *ServersLoadBalancer {
		lb := &ServersLoadBalancer{}
		for _, u := range urls {
			lb.Servers = append(lb.Servers, Server{URL: u})
		}
		return lb
	}

	tests := []struct {
		name  string
		dst   *Service
		other *Service
		want  bool
		urls  []string
	}{
		{
			name:  "same options merge servers",
			dst:   &Service{LoadBalancer: lb("http://a")},
			other: &Service{LoadBalancer: lb("http://a", "http://b")},
			want:  true,
			urls:  []string{"http://a", "http://b"},
		},
		{
			name:  "missing load balancer compares DeepEqual",
			dst:   &Service{Weighted: &WeightedRoundRobin{}},
			other: &Service{LoadBalancer: lb("http://b")},
			want:  false,
		},
		{
			name:  "equal services without load balancer",
			dst:   &Service{},
			other: &Service{},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dst.Merge(tt.other); got != tt.want {
				t.Fatalf("Merge() = %v, want %v", got, tt.want)
			}
			if tt.urls != nil {
				var urls []string
				for _, s := range tt.dst.LoadBalancer.Servers {
					urls = append(urls, s.URL)
				}
				if !reflect.DeepEqual(urls, tt.urls) {
					t.Errorf("urls = %v, want %v", urls, tt.urls)
				}
			}
		})
	}
}

func TestTCPAndUDPMerge(t *testing.T) {
	tcpDst := &TCPService{LoadBalancer: &TCPServersLoadBalancer{Servers: []TCPServer{{Address: "10.0.0.1:1"}}}}
	tcpOther := &TCPService{LoadBalancer: &TCPServersLoadBalancer{Servers: []TCPServer{{Address: "10.0.0.1:1"}, {Address: "10.0.0.2:1"}}}}
	if !tcpDst.Merge(tcpOther) || len(tcpDst.LoadBalancer.Servers) != 2 {
		t.Errorf("TCP merge: servers = %+v", tcpDst.LoadBalancer.Servers)
	}

	tcpBad := &TCPService{LoadBalancer: &TCPServersLoadBalancer{TerminationDelay: new(int)}}
	before := cloneOf(tcpDst)
	if tcpDst.Merge(tcpBad) || !reflect.DeepEqual(tcpDst, before) {
		t.Errorf("TCP merge with differing options should be refused without mutation")
	}

	udpDst := &UDPService{LoadBalancer: &UDPServersLoadBalancer{Servers: []UDPServer{{Address: "10.0.0.1:53"}}}}
	udpOther := &UDPService{LoadBalancer: &UDPServersLoadBalancer{Servers: []UDPServer{{Address: "10.0.0.1:53"}, {Address: "10.0.0.2:53"}}}}
	if !udpDst.Merge(udpOther) || len(udpDst.LoadBalancer.Servers) != 2 {
		t.Errorf("UDP merge: servers = %+v", udpDst.LoadBalancer.Servers)
	}
}
