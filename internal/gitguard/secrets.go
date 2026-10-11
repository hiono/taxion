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
	"strings"
	"time"
)

const ScannerVersion = "1.9.0"

// CheckSecrets scans indexed blobs locally with their path metadata. Scanner configuration
// and credentials from the project or environment are never forwarded.
func CheckSecrets(ctx context.Context, directory, scanner string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	entries, err := readIndex(ctx, directory)
	if err != nil {
		return err
	}
	if err := checkPaths(entries); err != nil {
		return err
	}
	executable, err := exec.LookPath(scanner)
	if err != nil {
		return fmt.Errorf("required Betterleaks scanner is unavailable")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return fmt.Errorf("resolve Betterleaks executable: %w", err)
	}
	temporary, err := os.MkdirTemp("", "taxion-scanner-")
	if err != nil {
		return fmt.Errorf("prepare local scanner configuration: %w", err)
	}
	defer os.RemoveAll(temporary)
	configPath := filepath.Join(temporary, "config.toml")
	if err := os.WriteFile(configPath, []byte("[extend]\nuseDefault = true\n"), 0600); err != nil {
		return fmt.Errorf("write local scanner configuration: %w", err)
	}
	// HOME and the working directory are private and empty; explicit config and
	// disabled validation avoid project discovery and credential API requests.
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + temporary}
	version := exec.CommandContext(ctx, executable, "version")
	version.Dir, version.Env = temporary, environment
	output, err := version.Output()
	if ctx.Err() != nil {
		return fmt.Errorf("secret scan interrupted: %w", ctx.Err())
	}
	if err != nil || strings.TrimSpace(string(output)) != ScannerVersion {
		return fmt.Errorf("secret scan requires Betterleaks %s; scanner version could not be verified", ScannerVersion)
	}
	findings := 0
	for _, entry := range entries {
		// A gitlink stores a commit ID, not the submodule's file contents.
		if entry.mode == "160000" {
			continue
		}
		blob := exec.CommandContext(ctx, "git", "-C", directory, "cat-file", "blob", entry.object)
		blob.Env = append(gitEnvironment(), "GIT_NO_REPLACE_OBJECTS=1")
		input, err := blob.StdoutPipe()
		if err != nil {
			return fmt.Errorf("prepare indexed content stream: %w", err)
		}
		if err := blob.Start(); err != nil {
			input.Close()
			return fmt.Errorf("cannot read indexed content: %w", err)
		}
		scan := exec.CommandContext(ctx, executable, "stdin",
			"--set-attr", "path="+entry.name,
			"--config", configPath, "--gitleaks-ignore-path", temporary,
			"--ignore-gitleaks-allow", "--validation=false", "--no-banner", "--no-color",
			"--redact=100", "--log-level=error", "--report-format=json", "--report-path=-",
			"--exit-code=10")
		scan.Dir, scan.Env, scan.Stdin = temporary, environment, input
		var report bytes.Buffer
		scan.Stdout = &report
		scanErr := scan.Run()
		input.Close()
		blobErr := blob.Wait()
		if ctx.Err() != nil {
			return fmt.Errorf("secret scan interrupted: %w", ctx.Err())
		}
		if blobErr != nil {
			return fmt.Errorf("cannot read complete indexed content")
		}
		if scanErr != nil {
			var exit *exec.ExitError
			if !errors.As(scanErr, &exit) || exit.ExitCode() != 10 {
				return fmt.Errorf("secret scanner failed; commit content was not verified")
			}
		}
		var detected []json.RawMessage
		if err := json.Unmarshal(report.Bytes(), &detected); err != nil || detected == nil {
			return fmt.Errorf("secret scanner returned an invalid report")
		}
		if (scanErr != nil) != (len(detected) != 0) {
			return fmt.Errorf("secret scanner exit status disagrees with its report")
		}
		findings += len(detected)
	}
	if findings != 0 {
		return fmt.Errorf("commit index contains %d potential secret findings; review indexed content before committing", findings)
	}
	return nil
}
