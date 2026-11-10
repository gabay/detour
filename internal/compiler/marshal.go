package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"

	"detour/internal/dynamic"
)

// Format is an output serialisation format for a dynamic configuration.
type Format int

const (
	FormatYAML Format = iota
	FormatJSON
)

// Marshal serialises conf in the given format. The output is deterministic:
// map keys are sorted by both encoders. Output ends with a newline.
func Marshal(conf *dynamic.Configuration, format Format) ([]byte, error) {
	if conf == nil {
		conf = &dynamic.Configuration{}
	}
	switch format {
	case FormatYAML:
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(conf); err != nil {
			return nil, fmt.Errorf("encode yaml: %w", err)
		}
		if err := enc.Close(); err != nil {
			return nil, fmt.Errorf("encode yaml: %w", err)
		}
		return buf.Bytes(), nil
	case FormatJSON:
		b, err := json.Marshal(conf)
		if err != nil {
			return nil, fmt.Errorf("encode json: %w", err)
		}
		return append(b, '\n'), nil
	default:
		return nil, fmt.Errorf("unknown format %d", format)
	}
}
