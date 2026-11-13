package config

import "fmt"

type Layer struct {
	Source string
	Input  []byte
}

type Resolution struct {
	Config  Config            `json:"config"`
	Sources map[string]string `json:"sources"`
}

// Resolve applies layers from lowest to highest priority. It resolves candidate
// configuration only; approval and credential verification remain separate gates.
func Resolve(layers []Layer) (Resolution, error) {
	return resolve(layers, true)
}

// ResolvePartial validates supplied fields without inventing missing references.
func ResolvePartial(layers []Layer) (Resolution, error) {
	return resolve(layers, false)
}

// ResolveBudgetGuidance can prepare a local proposal before all execution
// identity fields exist. Every supplied layer is still schema-validated.
func ResolveBudgetGuidance(layers []Layer) (string, error) {
	if len(layers) == 0 {
		return "", nil
	}
	resolved, err := resolve(layers, false)
	return resolved.Config.Execution.BudgetGuidance, err
}

func resolve(layers []Layer, required bool) (Resolution, error) {
	result := Resolution{Sources: map[string]string{}}
	if !required {
		result.Config.Version = 1
	}
	target := result.Config.fields()
	seen := map[string]bool{}
	for index, layer := range layers {
		if layer.Source == "" || seen[layer.Source] {
			return Resolution{}, fmt.Errorf("configuration layers require unique nonempty source identifiers")
		}
		seen[layer.Source] = true
		value, err := decode(layer.Input)
		if err != nil {
			return Resolution{}, fmt.Errorf("configuration layer %d: %w", index+1, err)
		}
		result.Config.Version = value.Version
		result.Sources["version"] = layer.Source
		for name, field := range value.fields() {
			if *field != "" {
				*target[name] = *field
				result.Sources[name] = layer.Source
			}
		}
	}
	if err := result.Config.validate(required); err != nil {
		return Resolution{}, err
	}
	return result, nil
}
