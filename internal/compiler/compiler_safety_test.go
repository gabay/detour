package compiler

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestCompileLogsWarnings(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := New(parse(t, `{"http":{"routers":{"web":{"service":"a"}}}}`))
	_, conflicts := c.Compile(parse(t, `{"http":{"routers":{"web":{"service":"b"},
		"r2":{"service":"missing","middlewares":["nomw","auth@file"]},
		"r3":{"service":"api@internal"}}}}`))

	out := buf.String()
	tests := []struct {
		name string
		want int
	}{
		{"dynamic entry dropped: static configuration takes precedence", 1},
		{"http router references unknown service", 1},
		{"http router references unknown middleware", 1},
	}
	for _, tt := range tests {
		if n := strings.Count(out, tt.name); n != tt.want {
			t.Errorf("%q logged %d times, want %d\n%s", tt.name, n, tt.want, out)
		}
	}
	if strings.Contains(out, "api@internal") || strings.Contains(out, "auth@file") {
		t.Errorf("provider-qualified names must not be checked:\n%s", out)
	}
	if len(conflicts) != 1 {
		t.Errorf("conflicts = %v", conflicts)
	}
}

func TestCompileIsRepeatableAndDoesNotMutate(t *testing.T) {
	const static = `{"http":{"routers":{"a":{"service":"s"}}},"tls":{"certificates":[{"certFile":"a.crt","keyFile":"a.key"}]}}`
	const discovered = `{"http":{"routers":{"a":{"service":"x"},"b":{"service":"s","middlewares":["m"]}},"middlewares":{"m":{}}},"tls":{"certificates":[{"certFile":"b.crt","keyFile":"b.key"}]}}`

	st, disc := parse(t, static), parse(t, discovered)
	wantStatic, wantDisc := st.Clone(), disc.Clone()
	c := New(st)

	first, _ := c.Compile(disc)
	first.HTTP.Routers["a"].Service = "mutated"
	first.TLS.Certificates[0].CertFile = "mutated"
	second, _ := c.Compile(disc)

	if got := compact(t, second); got != compact(t, parse(t, `{"http":{"routers":{"a":{"service":"s"},"b":{"service":"s","middlewares":["m"]}},"middlewares":{"m":{}}},"tls":{"certificates":[{"certFile":"a.crt","keyFile":"a.key"},{"certFile":"b.crt","keyFile":"b.key"}]}}`)) {
		t.Errorf("second compile affected by mutation of first result: %s", got)
	}
	if !reflect.DeepEqual(c.static, wantStatic) {
		t.Error("compiler static configuration was mutated")
	}
	if !reflect.DeepEqual(disc, wantDisc) {
		t.Error("discovered input was mutated")
	}
	if !reflect.DeepEqual(st, wantStatic) {
		t.Error("caller's static input was mutated")
	}
}

func TestCompileConcurrent(t *testing.T) {
	c := New(parse(t, `{"http":{"routers":{"a":{"service":"s"}}}}`))
	disc := parse(t, `{"http":{"routers":{"a":{"service":"x"},"b":{"service":"s"}}}}`)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				c.Compile(disc)
			}
		}()
	}
	wg.Wait()
}
