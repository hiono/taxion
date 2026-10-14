package gitproof

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func TrackedPathsAt(worktree, commit string) ([]string, error) {
	if !fullSHA(commit) {
		return nil, fmt.Errorf("file access snapshot requires a full commit SHA")
	}
	root, err := git(worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	root, err = canonical(root)
	if err != nil {
		return nil, fmt.Errorf("resolve file access worktree: %w", err)
	}
	requestedRoot, err := canonical(worktree)
	if err != nil || requestedRoot != root {
		return nil, fmt.Errorf("file access worktree must identify its Git root")
	}
	resolved, err := git(root, "rev-parse", commit+"^{commit}")
	if err != nil || resolved != commit {
		return nil, fmt.Errorf("file access snapshot requires the exact commit %s", commit)
	}
	command := exec.Command("git", "-C", root, "ls-tree", "-r", "-z", "--name-only", commit)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read committed paths at %s: %w: %s", commit, err, strings.TrimSpace(stderr.String()))
	}
	if len(output) == 0 {
		return []string{}, nil
	}
	paths := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	for i := range paths {
		paths[i] = filepath.ToSlash(paths[i])
	}
	return paths, nil
}
