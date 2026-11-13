package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Values exposes identifiers, never credentials, for setup and provenance.
func (c Config) Values() map[string]string {
	values := map[string]string{}
	for name, field := range c.fields() {
		if *field != "" {
			values[name] = *field
		}
	}
	return values
}

// ValuesLayer uses the same schema as files and environment overrides.
func ValuesLayer(source string, values map[string]string) (Layer, error) {
	var c Config
	fields := c.fields()
	document := map[string]any{"version": 1}
	for name, value := range values {
		if _, ok := fields[name]; !ok || value == "" {
			return Layer{}, fmt.Errorf("setup requires known, nonempty configuration fields")
		}
		section, key, _ := strings.Cut(name, ".")
		nested, ok := document[section].(map[string]string)
		if !ok {
			nested = map[string]string{}
			document[section] = nested
		}
		nested[key] = value
	}
	input, err := yaml.Marshal(document)
	if err != nil {
		return Layer{}, err
	}
	if _, err := decode(input); err != nil {
		return Layer{}, err
	}
	return Layer{Source: source, Input: input}, nil
}
