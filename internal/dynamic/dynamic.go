// Package dynamic models the Traefik v3 dynamic configuration and decodes it
// from Traefik-style labels.
package dynamic

import (
	"reflect"

	"github.com/traefik/paerser/parser"
)

// Configuration is the Traefik v3 dynamic configuration.
type Configuration struct {
	HTTP *HTTPConfiguration `json:"http,omitempty" yaml:"http,omitempty"`
	TCP  *TCPConfiguration  `json:"tcp,omitempty" yaml:"tcp,omitempty"`
	UDP  *UDPConfiguration  `json:"udp,omitempty" yaml:"udp,omitempty"`
	TLS  *TLSConfiguration  `json:"tls,omitempty" yaml:"tls,omitempty"`
}

// DecodeLabels decodes Traefik container/service labels into a Configuration.
// Labels outside the traefik.http, traefik.tcp, traefik.udp and
// traefik.tls.stores.default namespaces (traefik.enable, traefik.docker.*, ...)
// are ignored. All sections of the returned Configuration are non-nil.
func DecodeLabels(labels map[string]string) (*Configuration, error) {
	conf := &Configuration{
		HTTP: &HTTPConfiguration{},
		TCP:  &TCPConfiguration{},
		UDP:  &UDPConfiguration{},
		TLS:  &TLSConfiguration{},
	}

	err := Decode(labels, conf, "traefik.http", "traefik.tcp", "traefik.udp", "traefik.tls.stores.default")
	if err != nil {
		return nil, err
	}

	return conf, nil
}

// Decode decodes labels into element, considering only the labels whose key starts
// with one of filters. It is a thin wrapper around paerser's parser.Decode.
func Decode(labels map[string]string, element any, filters ...string) error {
	return parser.Decode(labels, element, parser.DefaultRootName, filters...)
}

// PruneEmpty sets empty maps and sections (HTTP, TCP, UDP, TLS) to nil, so that
// an empty configuration marshals to {} instead of {"http": {}, ...}.
func (c *Configuration) PruneEmpty() {
	if c == nil {
		return
	}

	if h := c.HTTP; h != nil {
		h.Routers = pruneMap(h.Routers)
		h.Services = pruneMap(h.Services)
		h.Middlewares = pruneMap(h.Middlewares)
		h.ServersTransports = pruneMap(h.ServersTransports)
		if h.Routers == nil && h.Services == nil && h.Middlewares == nil && h.ServersTransports == nil {
			c.HTTP = nil
		}
	}

	if t := c.TCP; t != nil {
		t.Routers = pruneMap(t.Routers)
		t.Services = pruneMap(t.Services)
		t.Middlewares = pruneMap(t.Middlewares)
		t.ServersTransports = pruneMap(t.ServersTransports)
		if t.Routers == nil && t.Services == nil && t.Middlewares == nil && t.ServersTransports == nil {
			c.TCP = nil
		}
	}

	if u := c.UDP; u != nil {
		u.Routers = pruneMap(u.Routers)
		u.Services = pruneMap(u.Services)
		if u.Routers == nil && u.Services == nil {
			c.UDP = nil
		}
	}

	if t := c.TLS; t != nil {
		if len(t.Certificates) == 0 {
			t.Certificates = nil
		}
		t.Options = pruneMap(t.Options)
		t.Stores = pruneMap(t.Stores)
		if t.Certificates == nil && t.Options == nil && t.Stores == nil {
			c.TLS = nil
		}
	}
}

// Clone returns a deep copy of the configuration. Fields tagged json:"-" are copied too.
func (c *Configuration) Clone() *Configuration {
	if c == nil {
		return nil
	}

	return deepCopy(reflect.ValueOf(c)).Interface().(*Configuration)
}

func pruneMap[K comparable, V any](m map[K]V) map[K]V {
	if len(m) == 0 {
		return nil
	}
	return m
}

// deepCopy recursively copies pointers, structs, slices, maps and interfaces.
// It only handles exported fields, which is all the dynamic types use.
func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		p := reflect.New(v.Elem().Type())
		p.Elem().Set(deepCopy(v.Elem()))
		return p

	case reflect.Struct:
		s := reflect.New(v.Type()).Elem()
		s.Set(v)
		for i := range s.NumField() {
			if f := s.Field(i); f.CanSet() {
				f.Set(deepCopy(f))
			}
		}
		return s

	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		s := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			s.Index(i).Set(deepCopy(v.Index(i)))
		}
		return s

	case reflect.Map:
		if v.IsNil() {
			return v
		}
		m := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			m.SetMapIndex(iter.Key(), deepCopy(iter.Value()))
		}
		return m

	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		i := reflect.New(v.Type()).Elem()
		i.Set(deepCopy(v.Elem()))
		return i

	default:
		return v
	}
}
