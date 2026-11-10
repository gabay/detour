// Package server serves the latest merged configuration to Traefik's HTTP provider.
package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"detour/internal/compiler"
	"detour/internal/dynamic"
)

// Snapshot is an immutable, pre-serialised copy of one successful configuration
// build. Gzip variants are compressed once here, never per request.
type Snapshot struct {
	UpdatedAt time.Time

	YAML, JSON         []byte
	YAMLGzip, JSONGzip []byte

	// YAMLETag and JSONETag are strong ETags over the uncompressed bodies.
	// Gzip variants derive their own ETag from these (see variant).
	YAMLETag, JSONETag string
}

// NewSnapshot serialises conf as YAML and JSON and gzips both once.
func NewSnapshot(conf *dynamic.Configuration, now time.Time) (*Snapshot, error) {
	yamlBody, err := compiler.Marshal(conf, compiler.FormatYAML)
	if err != nil {
		return nil, err
	}
	jsonBody, err := compiler.Marshal(conf, compiler.FormatJSON)
	if err != nil {
		return nil, err
	}
	yamlGz, err := gzipBytes(yamlBody)
	if err != nil {
		return nil, fmt.Errorf("gzip yaml: %w", err)
	}
	jsonGz, err := gzipBytes(jsonBody)
	if err != nil {
		return nil, fmt.Errorf("gzip json: %w", err)
	}
	return &Snapshot{
		UpdatedAt: now,
		YAML:      yamlBody,
		JSON:      jsonBody,
		YAMLGzip:  yamlGz,
		JSONGzip:  jsonGz,
		YAMLETag:  etagOf(yamlBody),
		JSONETag:  etagOf(jsonBody),
	}, nil
}

// variant returns the body, ETag and Content-Type for a format, optionally gzipped.
func (sn *Snapshot) variant(format compiler.Format, gzipped bool) (body []byte, etag, contentType string) {
	if format == compiler.FormatJSON {
		body, etag, contentType = sn.JSON, sn.JSONETag, contentTypeJSON
		if gzipped {
			body = sn.JSONGzip
		}
	} else {
		body, etag, contentType = sn.YAML, sn.YAMLETag, contentTypeYAML
		if gzipped {
			body = sn.YAMLGzip
		}
	}
	if gzipped {
		// The gzip bytes differ from the identity bytes, so their ETag must too.
		etag = etag[:len(etag)-1] + `-gzip"`
	}
	return body, etag, contentType
}

// Store holds the current Snapshot. The zero value is ready to use and
// reports no snapshot until Set is called.
type Store struct {
	p atomic.Pointer[Snapshot]
}

// Set publishes sn as the current snapshot.
func (s *Store) Set(sn *Snapshot) {
	s.p.Store(sn)
}

// Get returns the current snapshot, or nil if none has been published yet.
func (s *Store) Get() *Snapshot {
	return s.p.Load()
}

func etagOf(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
