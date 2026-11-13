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
		info, err := os.Stat(candidate)
		if err != nil {
			return fmt.Errorf("inspect local configuration path: %w", err)
		}
		if err := checkGitPath(candidate, info.IsDir()); err != nil {
			return err
		}
		if selected == resolved {
			break
		}
	}
	return nil
}

func checkProjectGitPolicy(projectDir string) error {
	directory, err := filepath.Abs(filepath.Join(projectDir, ".taxion"))
	if err != nil {
		return fmt.Errorf("resolve local Taxion directory: %w", err)
	}
	if err := checkGitPath(directory, true); err != nil {
		return fmt.Errorf("local .taxion directory: %w", err)
	}
	for _, path := range []string{directory, filepath.Join(projectDir, ".env")} {
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if path != directory {
				absolute, err := filepath.Abs(path)
				if err != nil {
					return fmt.Errorf("resolve local dotenv path: %w", err)
				}
				if err := checkGitPath(absolute, false); err != nil {
					return fmt.Errorf("local dotenv Git policy: %w", err)
				}
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect local configuration path: %w", err)
		}
		if err := CheckGitPolicy(path); err != nil {
			return fmt.Errorf("local configuration Git policy: %w", err)
		}
	}
	info, err := os.Stat(directory)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("local .taxion path must be a directory")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect local Taxion directory: %w", err)
	}
	return nil
}

func checkGitPath(path string, isDirectory bool) error {
	directory := filepath.Dir(path)
	if isDirectory {
		directory = path
	}
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
	// Git cannot inspect a path beyond a symlink. Check the indexed link here;
	// CheckGitPolicy separately checks the fully resolved target.
	parts := strings.Split(relative, "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("inspect configuration Git path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			relative = strings.Join(parts[:i+1], "/")
			isDirectory = false
			break
		}
	}
	tracked, err := configGit(root, "--literal-pathspecs", "ls-files", "--cached", "-z", "--", relative)
	if err != nil {
		return fmt.Errorf("inspect configuration tracking: %w", err)
	}
	if len(tracked) != 0 {
		return fmt.Errorf("local configuration path or descendant is Git-tracked; remove it from the index and add an ignore rule before loading")
	}
	ignorePath := relative
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if isDirectory {
			ignorePath += "/"
		} else {
			return nil
		}
	} else if err != nil {
		return fmt.Errorf("inspect local configuration path: %w", err)
	}
	if _, err := configGit(root, "check-ignore", "-q", "--", ignorePath); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return fmt.Errorf("local configuration path requires a Git ignore rule for the entire path before loading")
		}
		return fmt.Errorf("inspect configuration ignore rules: %w", err)
	}
	if isDirectory {
		unignored, err := configGit(root, "--literal-pathspecs", "ls-files", "--others", "--exclude-standard", "-z", "--", relative)
		if err != nil {
			return fmt.Errorf("inspect local configuration ignore exceptions: %w", err)
		}
		if len(unignored) != 0 {
			return fmt.Errorf("local configuration directory contains an unignored path; remove ignore exceptions before loading")
		}
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
