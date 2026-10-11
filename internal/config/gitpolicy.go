package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CheckGitPolicy checks both the selected path and its resolved target, so an
// external symlink cannot hide a tracked or unignored worktree file.
func CheckGitPolicy(path string) error {
	selected, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve configuration path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(selected)
	if err != nil {
		return fmt.Errorf("resolve configuration symlinks: %w", err)
	}
	for _, candidate := range []string{selected, resolved} {
		if err := checkGitPath(candidate); err != nil {
			return err
		}
		if selected == resolved {
			break
		}
	}
	return nil
}

func checkGitPath(path string) error {
	directory := filepath.Dir(path)
	for {
		_, err := os.Lstat(filepath.Join(directory, ".git"))
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect configuration Git boundary: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}
		directory = parent
	}
	rootText, err := configGit(directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("identify configuration worktree: %w", err)
	}
	root := strings.TrimSuffix(string(rootText), "\n")
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("configuration path lies outside its detected Git worktree")
	}
	relative = filepath.ToSlash(relative)
	tracked, err := configGit(root, "--literal-pathspecs", "ls-files", "--cached", "-z", "--", relative)
	if err != nil {
		return fmt.Errorf("inspect configuration tracking: %w", err)
	}
	if len(tracked) != 0 {
		return fmt.Errorf("configuration file is Git-tracked; remove it from the index and add an ignore rule before loading")
	}
	if _, err := configGit(root, "check-ignore", "-q", "--", relative); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return fmt.Errorf("configuration file requires a Git ignore rule before loading")
		}
		return fmt.Errorf("inspect configuration ignore rules: %w", err)
	}
	return nil
}

func configGit(directory string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			command.Env = append(command.Env, variable)
		}
	}
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("configuration Git check timed out: %w", ctx.Err())
	}
	return output, err
}
