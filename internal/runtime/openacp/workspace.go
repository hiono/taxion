package openacp

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/tools"
)

type fileSnapshot struct {
	content []byte
	mode    fs.FileMode
}

func copySkill(asset tools.Asset, destination string) error {
	if err := tools.CheckAsset(asset, true); err != nil {
		return err
	}
	err := filepath.WalkDir(asset.Path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(asset.Path, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("openacp: skill contains a non-regular file")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, content, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chmod(target, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	return tools.CheckAsset(tools.Asset{Path: destination, Hash: asset.Hash}, true)
}

func approvedPaths(config Config) ([]string, error) {
	switch config.FileAccess.Level {
	case core.FileAccessExact:
		return slices.Clone(config.FileAccess.Paths), nil
	case core.FileAccessCommitted:
		if config.TrackedFiles == nil {
			return nil, fmt.Errorf("openacp: committed file access requires an immutable tracked-path snapshot")
		}
		return slices.Clone(config.TrackedFiles), nil
	case core.FileAccessAll:
		var paths []string
		err := filepath.WalkDir(config.WorkingDirectory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(config.WorkingDirectory, path)
			if err != nil || rel == "." {
				return err
			}
			if rel == ".git" {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() {
				paths = append(paths, filepath.ToSlash(rel))
			}
			return nil
		})
		return paths, err
	default:
		return nil, fmt.Errorf("openacp: explicit fileAccess is required")
	}
}

func copyWorkspace(config Config, destination string) (map[string]fileSnapshot, error) {
	if config.Role != "CODER" && config.Role != "REVIEWER" {
		return map[string]fileSnapshot{}, nil
	}
	paths, err := approvedPaths(config)
	if err != nil {
		return nil, err
	}
	source, err := os.OpenRoot(config.WorkingDirectory)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	snapshot := map[string]fileSnapshot{}
	for _, path := range paths {
		if err := (core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{path}}).Validate(); err != nil {
			return nil, err
		}
		// Even an in-root symlink is rejected: its later target must not widen
		// the approved snapshot or cause export through a different host path.
		if err := noSymlinks(source, path); err != nil {
			return nil, err
		}
		info, err := source.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("openacp: approved path %s is not a regular file", path)
		}
		content, err := source.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(destination, path)), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(destination, path), content, info.Mode().Perm()); err != nil {
			return nil, err
		}
		snapshot[path] = fileSnapshot{content: content, mode: info.Mode().Perm()}
	}
	return snapshot, nil
}

func noSymlinks(root *os.Root, path string) error {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := range parts {
		name := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("openacp: symlink is outside the supported file boundary: %s", name)
		}
	}
	return nil
}

// Export only regular, policy-authorized files after checking the entire change
// set for concurrent host edits. Git metadata never enters the isolated copy.
func exportWorkspace(config Config, directory string, before map[string]fileSnapshot) error {
	source, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenRoot(config.WorkingDirectory)
	if err != nil {
		return err
	}
	defer target.Close()
	paths := map[string]bool{}
	for path := range before {
		paths[path] = true
	}
	if err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("openacp: agent created a non-regular file")
		}
		paths[filepath.ToSlash(rel)] = true
		return nil
	}); err != nil {
		return err
	}
	type change struct {
		path    string
		content []byte
		mode    fs.FileMode
		remove  bool
	}
	var changes []change
	for path := range paths {
		old, existed := before[path]
		content, err := source.ReadFile(path)
		removed := os.IsNotExist(err)
		if err != nil && !removed {
			return err
		}
		mode := fs.FileMode(0644)
		if !removed {
			info, err := source.Stat(path)
			if err != nil {
				return err
			}
			mode = info.Mode().Perm()
		}
		if existed && !removed && bytes.Equal(content, old.content) && mode == old.mode {
			continue
		}
		if config.Role != "CODER" {
			return fmt.Errorf("openacp: read-only role modified its isolated input")
		}
		if err := (core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{path}}).Validate(); err != nil {
			return err
		}
		if config.FileAccess.Level == core.FileAccessExact && !slices.Contains(config.FileAccess.Paths, path) ||
			config.FileAccess.Level == core.FileAccessCommitted && !slices.Contains(config.TrackedFiles, path) {
			return fmt.Errorf("openacp: agent changed an unapproved path: %s", path)
		}
		if err := noSymlinks(target, path); err != nil {
			return err
		}
		current, err := target.ReadFile(path)
		if existed {
			info, statErr := target.Stat(path)
			if err != nil || statErr != nil || !bytes.Equal(current, old.content) || info.Mode().Perm() != old.mode {
				return fmt.Errorf("openacp: host file changed during execution: %s", path)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("openacp: new output conflicts with a host file: %s", path)
		}
		changes = append(changes, change{path: path, content: content, mode: mode, remove: removed})
	}
	slices.SortFunc(changes, func(a, b change) int { return strings.Compare(a.path, b.path) })
	for _, item := range changes {
		if item.remove {
			if err := target.Remove(item.path); err != nil {
				return fmt.Errorf("openacp: export deletion: %w", err)
			}
			continue
		}
		if err := target.MkdirAll(filepath.Dir(item.path), 0755); err != nil {
			return err
		}
		if err := target.WriteFile(item.path, item.content, item.mode); err != nil {
			return fmt.Errorf("openacp: export output (partial changes may remain): %w", err)
		}
	}
	return nil
}
