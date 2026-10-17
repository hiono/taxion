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

// ReadSetupFiles permits missing ignore rules only while preparing an explicit
// repair. Tracked files and symlinks remain forbidden; execution uses ReadFiles.
func ReadSetupFiles(userConfigDir, projectDir string) ([]Layer, error) {
	directory := filepath.Join(projectDir, ".taxion")
	for _, path := range []string{directory, filepath.Join(directory, "config.yaml"), filepath.Join(projectDir, ".env")} {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("setup refuses local configuration symlinks; repair manually")
		}
		tracked, err := setupTracked(projectDir, path)
		if err != nil {
			return nil, err
		}
		if tracked {
			return nil, fmt.Errorf("setup refuses Git-tracked local configuration; remove it from the index manually")
		}
	}
	var layers []Layer
	for _, entry := range []struct{ source, path string }{
		{"user", filepath.Join(userConfigDir, "taxion", "config.yaml")},
		{"project", filepath.Join(directory, "config.yaml")},
	} {
		if _, err := os.Lstat(entry.path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		if entry.source == "user" {
			if err := CheckGitPolicy(entry.path); err != nil {
				return nil, err
			}
		}
		input, err := os.ReadFile(entry.path)
		if err != nil {
			return nil, err
		}
		layers = append(layers, Layer{Source: entry.source, Input: input})
	}
	if _, err := ResolvePartial(layers); err != nil {
		return nil, err
	}
	return layers, nil
}

func setupTracked(root, path string) (bool, error) {
	if _, err := os.Lstat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	tracked, err := configGit(root, "--literal-pathspecs", "ls-files", "--cached", "-z", "--", filepath.ToSlash(relative))
	return len(tracked) != 0, err
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
