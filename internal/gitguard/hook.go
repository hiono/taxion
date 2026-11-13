package gitguard

import (
	"bytes"
	"context"
	"encoding/json"
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

const scannerMarker = "# Taxion scanner: "

func secretHookScript(executable, scanner string) ([]byte, error) {
	script, err := hookScript(executable)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(scanner) || strings.ContainsAny(scanner, "\r\n\x00") {
		return nil, fmt.Errorf("hook requires an absolute scanner path without control separators")
	}
	metadata, err := json.Marshal(scanner)
	if err != nil {
		return nil, fmt.Errorf("encode hook scanner reference: %w", err)
	}
	quoted := "'" + strings.ReplaceAll(scanner, "'", "'\"'\"'") + "'"
	script = bytes.Replace(script, []byte("# Taxion managed pre-commit: prohibited paths only; no secret content scan.\n"),
		[]byte("# Taxion managed pre-commit: prohibited paths and local secret content scan.\n"+scannerMarker+string(metadata)+"\n"), 1)
	return bytes.Replace(script, []byte(" git check-index\n"), []byte(" git check-index --secrets --scanner "+quoted+"\n"), 1), nil
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
	installed, _, err := ManageConfiguredHook(ctx, directory, executable, operation, "")
	return installed, err
}

// ManageConfiguredHook installs a full hook when scanner is supplied. Status
// and uninstall recover its reference only from an exactly reconstructed script.
func ManageConfiguredHook(ctx context.Context, directory, executable, operation, scanner string) (bool, bool, error) {
	switch operation {
	case "install", "status", "uninstall":
	default:
		return false, false, fmt.Errorf("unknown hook operation")
	}
	path, err := hookPath(ctx, directory)
	if err != nil {
		return false, false, err
	}
	if operation != "install" {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, false, fmt.Errorf("inspect pre-commit hook: %w", err)
		}
		if err == nil && info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return false, false, fmt.Errorf("read pre-commit hook: %w", err)
			}
			for _, line := range strings.Split(string(content), "\n") {
				if strings.HasPrefix(line, scannerMarker) {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, scannerMarker)), &scanner); err != nil {
						return false, false, fmt.Errorf("invalid Taxion scanner reference; hook left unchanged")
					}
				}
			}
		}
	}
	script, err := hookScript(executable)
	if scanner != "" {
		script, err = secretHookScript(executable, scanner)
	}
	if err != nil {
		return false, false, err
	}
	installed, err := checkHook(path, script)
	if err != nil {
		return false, false, err
	}
	if operation == "uninstall" {
		if installed {
			if err := os.Remove(path); err != nil {
				return false, false, fmt.Errorf("remove Taxion pre-commit hook: %w", err)
			}
		}
		return false, false, nil
	}
	if operation == "status" && !installed {
		return false, false, nil
	}
	if err := checkHookExecutable(executable); err != nil {
		return false, false, err
	}
	if scanner != "" {
		if err := checkHookExecutable(scanner); err != nil {
			return false, false, fmt.Errorf("required hook scanner: %w", err)
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		temporary, err := os.MkdirTemp("", "taxion-scanner-version-")
		if err != nil {
			return false, false, fmt.Errorf("prepare hook scanner verification: %w", err)
		}
		defer os.RemoveAll(temporary)
		if err := verifyScanner(ctx, scanner, temporary, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + temporary}); err != nil {
			return false, false, err
		}
	}
	if operation == "status" || installed {
		return true, scanner != "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, false, fmt.Errorf("create hook directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".taxion-pre-commit-*")
	if err != nil {
		return false, false, fmt.Errorf("prepare Taxion pre-commit hook: %w", err)
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(script)
	syncErr := temp.Sync()
	modeErr := temp.Chmod(0755)
	closeErr := temp.Close()
	if err := errors.Join(writeErr, syncErr, modeErr, closeErr); err != nil {
		return false, false, fmt.Errorf("write Taxion pre-commit hook: %w", err)
	}
	// Link publishes a complete hook without overwriting one created meanwhile.
	if err := os.Link(temp.Name(), path); err != nil {
		return false, false, fmt.Errorf("install Taxion pre-commit hook without replacement: %w", err)
	}
	return true, scanner != "", nil
}
