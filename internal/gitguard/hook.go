package gitguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func hookPath(ctx context.Context, directory string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("Taxion hook installation currently requires Unix executable permissions")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Resolve the directory only: Git's absolute path conversion can follow an
	// existing pre-commit symlink, hiding it from the no-replacement check.
	command := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	output, err := command.Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("hook path inspection interrupted: %w", ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("cannot locate effective Git pre-commit hook: %w", err)
	}
	path := strings.TrimSuffix(string(output), "\n")
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("Git returned a nonabsolute hook path")
	}
	return filepath.Join(path, "pre-commit"), nil
}

func hookScript(executable string) ([]byte, error) {
	if !filepath.IsAbs(executable) || strings.ContainsAny(executable, "\r\n\x00") {
		return nil, fmt.Errorf("hook requires an absolute executable path without control separators")
	}
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "'"
	return []byte("#!/bin/sh\n# Taxion managed pre-commit: prohibited paths only; no secret content scan.\nexec " + quoted + " git check-index\n"), nil
}

func checkHook(path string, script []byte) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect pre-commit hook: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("existing pre-commit hook is not a regular file; left unchanged")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read pre-commit hook: %w", err)
	}
	if !bytes.Equal(content, script) {
		return false, fmt.Errorf("existing pre-commit hook differs from this Taxion installation; left unchanged; integrate taxion git check-index manually")
	}
	if info.Mode().Perm()&0111 == 0 {
		return false, fmt.Errorf("Taxion pre-commit hook is not executable; restore its executable permission")
	}
	return true, nil
}

func checkHookExecutable(executable string) error {
	info, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("hook executable is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("hook executable must be an executable regular file")
	}
	return nil
}

// ManageHook changes only an exact hook for the supplied executable; existing
// custom, edited, or symlink hooks are never replaced or removed.
func ManageHook(ctx context.Context, directory, executable, operation string) (bool, error) {
	switch operation {
	case "install", "status", "uninstall":
	default:
		return false, fmt.Errorf("unknown hook operation")
	}
	path, err := hookPath(ctx, directory)
	if err != nil {
		return false, err
	}
	script, err := hookScript(executable)
	if err != nil {
		return false, err
	}
	installed, err := checkHook(path, script)
	if err != nil {
		return false, err
	}
	if operation == "uninstall" {
		if installed {
			if err := os.Remove(path); err != nil {
				return false, fmt.Errorf("remove Taxion pre-commit hook: %w", err)
			}
		}
		return false, nil
	}
	if operation == "status" && !installed {
		return false, nil
	}
	if err := checkHookExecutable(executable); err != nil {
		return false, err
	}
	if operation == "status" || installed {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("create hook directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".taxion-pre-commit-*")
	if err != nil {
		return false, fmt.Errorf("prepare Taxion pre-commit hook: %w", err)
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(script)
	syncErr := temp.Sync()
	modeErr := temp.Chmod(0755)
	closeErr := temp.Close()
	if err := errors.Join(writeErr, syncErr, modeErr, closeErr); err != nil {
		return false, fmt.Errorf("write Taxion pre-commit hook: %w", err)
	}
	// Link publishes a complete hook without overwriting one created meanwhile.
	if err := os.Link(temp.Name(), path); err != nil {
		return false, fmt.Errorf("install Taxion pre-commit hook without replacement: %w", err)
	}
	return true, nil
}
