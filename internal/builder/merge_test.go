package builder

import (
	"encoding/json"
	"reflect"
	"testing"

	"detour/internal/dynamic"

	"gopkg.in/yaml.v3"
)

func httpConf(routers map[string]*dynamic.Router, services map[string]*dynamic.Service) *dynamic.Configuration {
	return &dynamic.Configuration{HTTP: &dynamic.HTTPConfiguration{Routers: routers, Services: services}}
}

func lb(urls ...string) *dynamic.Service {
	svc := &dynamic.Service{LoadBalancer: &dynamic.ServersLoadBalancer{}}
	svc.LoadBalancer.SetDefaults()
	for _, u := range urls {
		svc.LoadBalancer.Servers = append(svc.LoadBalancer.Servers, dynamic.Server{URL: u})
	}
	return svc
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name      string
		in        []NamedConfiguration
		wantJSON  string
		conflicts [][]string // origins of each reported conflict
	}{
		{
			name: "services append servers",
			in: []NamedConfiguration{
				{Name: "b", Configuration: httpConf(nil, map[string]*dynamic.Service{"svc": lb("http://b:80")})},
				{Name: "a", Configuration: httpConf(nil, map[string]*dynamic.Service{"svc": lb("http://a:80")})},
			},
			wantJSON: `{"http":{"services":{"svc":{"loadBalancer":{"servers":[{"url":"http://a:80"},{"url":"http://b:80"}],"passHostHeader":true}}}}}`,
		},
		{
			name: "identical routers kept without conflict",
			in: []NamedConfiguration{
				{Name: "a", Configuration: httpConf(map[string]*dynamic.Router{"r": {Rule: "Host(`x`)", Service: "s"}}, nil)},
				{Name: "b", Configuration: httpConf(map[string]*dynamic.Router{"r": {Rule: "Host(`x`)", Service: "s"}}, nil)},
			},
			wantJSON: `{"http":{"routers":{"r":{"rule":"Host(` + "`x`" + `)","service":"s"}}}}`,
		},
		{
			name: "different routers conflict and are all removed",
			in: []NamedConfiguration{
				{Name: "a", Configuration: httpConf(map[string]*dynamic.Router{"r": {Rule: "Host(`x`)"}}, nil)},
				{Name: "b", Configuration: httpConf(map[string]*dynamic.Router{"r": {Rule: "Host(`y`)"}}, nil)},
				{Name: "c", Configuration: httpConf(map[string]*dynamic.Router{"keep": {Rule: "Host(`z`)"}}, nil)},
			},
			wantJSON:  `{"http":{"routers":{"keep":{"rule":"Host(` + "`z`" + `)"}}}}`,
			conflicts: [][]string{{"a", "b"}},
		},
		{
			name: "TLS certificates with the same files merge their stores",
			in: []NamedConfiguration{
				{Name: "a", Configuration: &dynamic.Configuration{TLS: &dynamic.TLSConfiguration{Certificates: []*dynamic.CertAndStores{
					{CertFile: "c.pem", KeyFile: "k.pem", Stores: []string{"s1"}}}}}},
				{Name: "b", Configuration: &dynamic.Configuration{TLS: &dynamic.TLSConfiguration{Certificates: []*dynamic.CertAndStores{
					{CertFile: "c.pem", KeyFile: "k.pem", Stores: []string{"s2"}},
					{CertFile: "d.pem", KeyFile: "e.pem"}}}}},
			},
			wantJSON: `{"tls":{"certificates":[{"certFile":"c.pem","keyFile":"k.pem","stores":["s1","s2"]},{"certFile":"d.pem","keyFile":"e.pem"}]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotConflicts [][]string
			got := Merge(tt.in, func(kind, name string, origins []string) {
				gotConflicts = append(gotConflicts, origins)
			})
			data, _ := json.Marshal(got)
			if g, w := string(data), canonical(t, tt.wantJSON); g != w {
				t.Fatalf("got\n%s\nwant\n%s", g, w)
			}
			if !reflect.DeepEqual(gotConflicts, tt.conflicts) {
				t.Fatalf("conflicts %v, want %v", gotConflicts, tt.conflicts)
			}
		})
	}
}

func TestMergeDeterministic(t *testing.T) {
	in := []NamedConfiguration{
		{Name: "a", Configuration: httpConf(map[string]*dynamic.Router{"r": {Rule: "Host(`a`)"}}, map[string]*dynamic.Service{"s": lb("http://a")})},
		{Name: "b", Configuration: httpConf(map[string]*dynamic.Router{"q": {Rule: "Host(`b`)"}}, map[string]*dynamic.Service{"s": lb("http://b")})},
		{Name: "c", Configuration: httpConf(map[string]*dynamic.Router{"r": {Rule: "Host(`c`)"}}, nil)},
	}
	shuffled := []NamedConfiguration{in[2], in[0], in[1]}

	first, err := yaml.Marshal(Merge(in, nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := yaml.Marshal(Merge(shuffled, nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("output depends on input order:\n%s\nvs\n%s", first, second)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	src := httpConf(nil, map[string]*dynamic.Service{"s": lb("http://a")})
	Merge([]NamedConfiguration{{Name: "a", Configuration: src}, {Name: "b", Configuration: httpConf(nil, map[string]*dynamic.Service{"s": lb("http://b")})}}, nil)
	if n := len(src.HTTP.Services["s"].LoadBalancer.Servers); n != 1 {
		t.Fatalf("input mutated: %d servers", n)
	}
}
