package compiler

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"detour/internal/dynamic"
)

func TestMarshalDeterministic(t *testing.T) {
	c := parse(t, `{"http":{"routers":{"z":{"service":"s","middlewares":["m1","m2"],"rule":"PathPrefix(/a)"},"a":{"service":"s"}},
		"services":{"s":{"loadBalancer":{"servers":[{"url":"http://a"},{"url":"http://b"}]}}},
		"middlewares":{"m1":{"headers":{"customResponseHeaders":{"b":"1","a":"2"}}},"m2":{"stripPrefix":{"prefixes":["/x"]}}}},
		"tcp":{"routers":{"t":{"service":"ts","rule":"HostSNI(*)"}}},
		"tls":{"options":{"o":{"minVersion":"VersionTLS12"}},"stores":{"default":{}}}}`)
	for _, f := range []Format{FormatYAML, FormatJSON} {
		first, err := Marshal(c, f)
		if err != nil {
			t.Fatal(err)
		}
		for range 20 {
			got, err := Marshal(c, f)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, first) {
				t.Fatalf("format %d: output not deterministic:\n%s\nvs\n%s", f, first, got)
			}
		}
	}
}

func TestMarshalYAMLRoundTrip(t *testing.T) {
	in := parse(t, `{"http":{"routers":{"r":{"entryPoints":["web"],"service":"s","rule":"Path(/a)","priority":3,"tls":{"certResolver":"le"}}},
		"services":{"s":{"loadBalancer":{"passHostHeader":false,"servers":[{"url":"http://a","weight":2}]}}}},
		"tls":{"certificates":[{"certFile":"c.crt","keyFile":"c.key","stores":["default"]}]}}`)
	in.PruneEmpty()

	out, err := Marshal(in, FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	var back dynamic.Configuration
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal %s: %v", out, err)
	}
	back.PruneEmpty()
	if !reflect.DeepEqual(&back, in) {
		t.Errorf("round trip mismatch:\n%s", out)
	}
}

func TestMarshalOmitsLabelOnlyFields(t *testing.T) {
	c := &dynamic.Configuration{HTTP: &dynamic.HTTPConfiguration{
		Services: map[string]*dynamic.Service{"s": {LoadBalancer: &dynamic.ServersLoadBalancer{
			Servers: []dynamic.Server{{URL: "http://a", Scheme: "https", Port: "8443"}},
		}}},
	}}
	for _, f := range []Format{FormatYAML, FormatJSON} {
		out, err := Marshal(c, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"https", "8443", "scheme", "port"} {
			if strings.Contains(string(out), bad) {
				t.Errorf("format %d: output contains label-only %q:\n%s", f, bad, out)
			}
		}
	}
}

func TestMarshalEmpty(t *testing.T) {
	tests := []struct {
		name   string
		conf   *dynamic.Configuration
		format Format
		want   string
	}{
		{"yaml empty", &dynamic.Configuration{}, FormatYAML, "{}\n"},
		{"json empty", &dynamic.Configuration{}, FormatJSON, "{}\n"},
		{"yaml nil", nil, FormatYAML, "{}\n"},
		{"json nil", nil, FormatJSON, "{}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(tt.conf, tt.format)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestMarshalUnknownFormat(t *testing.T) {
	if _, err := Marshal(&dynamic.Configuration{}, Format(99)); err == nil {
		t.Error("expected error for unknown format")
	}
}
