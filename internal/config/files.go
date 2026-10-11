package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Files struct {
	User       string
	Project    string
	Invocation string
}

// ReadFiles searches only the supplied project directory. Missing default
// files are optional; explicit files and all other read failures are errors.
func ReadFiles(userConfigDir, projectDir string, files Files) ([]Layer, error) {
	var layers []Layer
	for _, entry := range []struct {
		source, explicit, fallback string
	}{
		{"user", files.User, filepath.Join(userConfigDir, "taxion", "config.yaml")},
		{"project", files.Project, filepath.Join(projectDir, ".taxion", "config.yaml")},
		{"invocation", files.Invocation, ""},
	} {
		path := entry.explicit
		if path == "" {
			path = entry.fallback
		}
		if path == "" {
			continue
		}
		_, err := os.Lstat(path)
		if err != nil {
			if entry.explicit == "" && errors.Is(err, os.ErrNotExist) {
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
	if len(layers) == 0 {
		return nil, fmt.Errorf("no configuration files found; provide --file or create user/project config.yaml")
	}
	return layers, nil
}
