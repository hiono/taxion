package gitguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
)

// Stage publishes a scanned private index, never a recursive or force add.
func Stage(ctx context.Context, dispatch core.GraphDispatch, paths []string, scanner string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("managed stage requires explicit repository-relative file paths")
	}
	return managedIndex(ctx, dispatch, "", scanner, paths)
}

func validateStagePaths(directory string, dispatch core.GraphDispatch, paths []string) error {
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve managed staging root: %w", err)
	}
	var entries []indexEntry
	for index, path := range paths {
		if path == "" || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path ||
			path == "." || strings.HasPrefix(path, "../") || strings.ContainsAny(path, "\x00\\") ||
			slices.Contains(paths[:index], path) {
			return fmt.Errorf("managed stage requires unique canonical repository-relative file paths")
		}
		for _, component := range strings.Split(path, "/") {
			if strings.EqualFold(component, ".git") {
				return fmt.Errorf("managed stage cannot select Git metadata")
			}
		}
		parentPath := filepath.Join(root, filepath.Dir(path))
		// A tracked deletion may remove every parent directory from the worktree.
		for {
			if _, err := os.Lstat(parentPath); err == nil {
				break
			} else if !os.IsNotExist(err) || parentPath == root {
				return fmt.Errorf("managed stage cannot inspect a selected file's parent")
			}
			parentPath = filepath.Dir(parentPath)
		}
		parent, err := filepath.EvalSymlinks(parentPath)
		if err != nil {
			return fmt.Errorf("managed stage cannot resolve a selected file's parent")
		}
		relative, err := filepath.Rel(root, parent)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("managed stage cannot follow parent symlinks outside the worktree")
		}
		info, err := os.Lstat(filepath.Join(root, path))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("managed stage cannot inspect a selected file")
		}
		if err == nil && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("managed stage accepts files or tracked deletions, not directories or special files")
		}
		if err == nil {
			entries = append(entries, indexEntry{name: path})
		}
	}
	if err := checkPaths(entries); err != nil {
		return err
	}
	if err := gitproof.CheckChangedPaths(dispatch, paths); err != nil {
		return fmt.Errorf("managed stage selected a path outside the authorized scope")
	}
	return nil
}
