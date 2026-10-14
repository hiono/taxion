package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReadFiles searches only the supplied project directory. Missing default
// files are optional; all other read failures are errors.
func ReadFiles(userConfigDir, projectDir string) ([]Layer, error) {
	return readFiles(userConfigDir, projectDir, true)
}

// ReadProposalFiles permits local proposals before configuration exists.
// Present files still use the normal schema and Git-policy checks.
func ReadProposalFiles(userConfigDir, projectDir string) ([]Layer, error) {
	return readFiles(userConfigDir, projectDir, false)
}

func readFiles(userConfigDir, projectDir string, required bool) ([]Layer, error) {
	_, projectErr := os.Lstat(filepath.Join(projectDir, ".taxion", "config.yaml"))
	if required || projectErr == nil || !errors.Is(projectErr, os.ErrNotExist) {
		if err := checkProjectGitPolicy(projectDir); err != nil {
			return nil, err
		}
	}
	var layers []Layer
	for _, entry := range []struct {
		source, path string
	}{
		{"user", filepath.Join(userConfigDir, "taxion", "config.yaml")},
		{"project", filepath.Join(projectDir, ".taxion", "config.yaml")},
	} {
		path := entry.path
		_, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("inspect %s configuration: %w", entry.source, err)
		}
		if err := CheckGitPolicy(path); err != nil {
			return nil, fmt.Errorf("%s configuration: %w", entry.source, err)
		}
		input, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s configuration: %w", entry.source, err)
		}
		layers = append(layers, Layer{Source: entry.source, Input: input})
	}
	if required && len(layers) == 0 {
		return nil, fmt.Errorf("no configuration files found; create Global taxion/config.yaml or Project .taxion/config.yaml")
	}
	return layers, nil
}
