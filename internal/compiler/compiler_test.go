package compiler

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"detour/internal/dynamic"
)

func parse(t *testing.T, s string) *dynamic.Configuration {
	t.Helper()
	if s == "" {
		return nil
	}
	var c dynamic.Configuration
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return &c
}

func compact(t *testing.T, c *dynamic.Configuration) string {
	t.Helper()
	b, err := Marshal(c, FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestCompile(t *testing.T) {
	tests := []struct {
		name       string
		static     string
		discovered string
		want       string
		conflicts  []Conflict
	}{
		{
			name: "nil static and nil discovered",
			want: `{}`,
		},
		{
			name:   "static only",
			static: `{"http":{"routers":{"a":{"service":"s"}}}}`,

			want: `{"http":{"routers":{"a":{"service":"s"}}}}`,
		},
		{
			name:       "discovered only",
			discovered: `{"http":{"routers":{"b":{"service":"s"}}}}`,
			want:       `{"http":{"routers":{"b":{"service":"s"}}}}`,
		},
		{
			name:       "disjoint merge across sections",
			static:     `{"http":{"routers":{"a":{"service":"s"}}},"tcp":{"services":{"t1":{}}}}`,
			discovered: `{"http":{"services":{"s":{}}},"udp":{"routers":{"u":{"service":"us"}}}}`,
			want:       `{"http":{"routers":{"a":{"service":"s"}},"services":{"s":{}}},"tcp":{"services":{"t1":{}}},"udp":{"routers":{"u":{"service":"us"}}}}`,
		},
		{
			name:       "router conflict keeps static",
			static:     `{"http":{"routers":{"web":{"service":"a"}}}}`,
			discovered: `{"http":{"routers":{"web":{"service":"b"},"api":{"service":"c"}}}}`,
			want:       `{"http":{"routers":{"api":{"service":"c"},"web":{"service":"a"}}}}`,
			conflicts:  []Conflict{{"http router", "web"}},
		},
		{
			name:       "all same-name conflicts sorted by kind and name",
			static:     `{"http":{"services":{"s":{}},"middlewares":{"m":{}},"serversTransports":{"t":{}}},"tcp":{"routers":{"r":{}},"services":{"s":{}},"middlewares":{"m":{}},"serversTransports":{"t":{}}},"udp":{"services":{"s":{}}},"tls":{"options":{"o":{}},"stores":{"st":{}}}}`,
			discovered: `{"http":{"services":{"s":{"loadBalancer":{"serversTransport":"x"}}},"middlewares":{"m":{}},"serversTransports":{"t":{}}},"tcp":{"routers":{"r":{}},"services":{"s":{}},"middlewares":{"m":{}},"serversTransports":{"t":{}}},"udp":{"services":{"s":{}}},"tls":{"options":{"o":{}},"stores":{"st":{}}}}`,
			want:       `{"http":{"services":{"s":{}},"middlewares":{"m":{}},"serversTransports":{"t":{}}},"tcp":{"routers":{"r":{}},"services":{"s":{}},"middlewares":{"m":{}},"serversTransports":{"t":{}}},"udp":{"services":{"s":{}}},"tls":{"options":{"o":{}},"stores":{"st":{}}}}`,
			conflicts: []Conflict{
				{"http middleware", "m"},
				{"http serversTransport", "t"},
				{"http service", "s"},
				{"tcp middleware", "m"},
				{"tcp router", "r"},
				{"tcp serversTransport", "t"},
				{"tcp service", "s"},
				{"tls option", "o"},
				{"tls store", "st"},
				{"udp service", "s"},
			},
		},
		{
			name:       "same name in different kinds does not conflict",
			static:     `{"http":{"routers":{"x":{"service":"x"}}}}`,
			discovered: `{"http":{"services":{"x":{}}}}`,
			want:       `{"http":{"routers":{"x":{"service":"x"}},"services":{"x":{}}}}`,
		},
		{
			name:       "certificates are deduplicated by files, static first",
			static:     `{"tls":{"certificates":[{"certFile":"a.crt","keyFile":"a.key","stores":["default"]}]}}`,
			discovered: `{"tls":{"certificates":[{"certFile":"a.crt","keyFile":"a.key","stores":["other"]},{"certFile":"b.crt","keyFile":"b.key"}]}}`,
			want:       `{"tls":{"certificates":[{"certFile":"a.crt","keyFile":"a.key","stores":["default"]},{"certFile":"b.crt","keyFile":"b.key"}]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(parse(t, tt.static))
			got, conflicts := c.Compile(parse(t, tt.discovered))
			if s := compact(t, got); s != tt.want {
				t.Errorf("config:\n got %s\nwant %s", s, tt.want)
			}
			if !reflect.DeepEqual(conflicts, tt.conflicts) {
				t.Errorf("conflicts: got %v want %v", conflicts, tt.conflicts)
			}
		})
	}
}
