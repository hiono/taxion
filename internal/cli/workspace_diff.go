package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const maxWorkspaceDiffBytes = 8 * 1024 * 1024

type workspaceBaseline struct {
	directory string
	commit    string
	untracked map[string]bool
}

func captureWorkspaceBaseline(ctx context.Context, directory string) (*workspaceBaseline, error) {
	commit, err := workspaceGit(ctx, directory, false, "rev-parse", "--verify", "HEAD")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	files, err := workspaceGit(ctx, directory, false, "ls-files", "-z", "--others", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("list existing untracked files: %w", err)
	}
	untracked := make(map[string]bool)
	for _, file := range splitGitPaths(files) {
		untracked[file] = true
	}
	return &workspaceBaseline{directory: directory, commit: strings.TrimSpace(commit), untracked: untracked}, nil
}

func (b *workspaceBaseline) diff(ctx context.Context) (string, error) {
	if b == nil {
		return "Git workspace diff unavailable; actual file changes were not inspected.", nil
	}
	changed, err := workspaceGit(ctx, b.directory, false, "diff", "--name-only", "-z", "--no-ext-diff", "--no-textconv", "--no-renames", "--relative", b.commit, "--", ".")
	if err != nil {
		return "", fmt.Errorf("list tracked changes: %w", err)
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--submodule=short", "--relative", b.commit, "--"}
	baseArgs := len(args)
	for _, file := range splitGitPaths(changed) {
		if !b.untracked[file] {
			args = append(args, ":(literal)"+file)
		}
	}
	var diff strings.Builder
	if len(args) > baseArgs {
		tracked, err := workspaceGit(ctx, b.directory, false, args...)
		if err != nil {
			return "", fmt.Errorf("read tracked changes: %w", err)
		}
		diff.WriteString(tracked)
	}
	files, err := workspaceGit(ctx, b.directory, false, "ls-files", "-z", "--others", "--exclude-standard", "--", ".")
	if err != nil {
		return "", fmt.Errorf("list new files: %w", err)
	}
	for _, file := range splitGitPaths(files) {
		if b.untracked[file] {
			continue
		}
		content, err := workspaceGit(ctx, b.directory, true, "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--", os.DevNull, file)
		if err != nil {
			return "", fmt.Errorf("read new file %q: %w", file, err)
		}
		if diff.Len()+len(content) > maxWorkspaceDiffBytes {
			return "", errors.New("workspace diff exceeds the input size limit")
		}
		diff.WriteString(content)
	}
	return "Git changes from the starting commit (may include pre-existing tracked changes):\n" +
		"Pre-existing untracked paths, ignored untracked files, changes outside the working directory, binary contents, and submodule contents are not included. " +
		"The workspace may change during or after inspection.\n\n" + diff.String(), nil
}

func splitGitPaths(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(value, "\x00"), "\x00")
}

type limitedGitOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *limitedGitOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > maxWorkspaceDiffBytes {
		b.exceeded = true
		return 0, errors.New("Git output exceeds the input size limit")
	}
	return b.buffer.Write(p)
}

func workspaceGitEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "GIT_") && name != "GIT_CONFIG_GLOBAL" &&
			name != "GIT_CONFIG_SYSTEM" && name != "GIT_CONFIG_NOSYSTEM" {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func workspaceGit(ctx context.Context, directory string, allowDiffExit bool, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	command.Env = workspaceGitEnvironment()
	var output limitedGitOutput
	var stderr limitedGitOutput
	command.Stdout = &output
	command.Stderr = &stderr
	err := command.Run()
	if output.exceeded || stderr.exceeded {
		return "", errors.New("Git output exceeds the input size limit")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exit *exec.ExitError
	if err != nil && !(allowDiffExit && errors.As(err, &exit) && exit.ExitCode() == 1 && stderr.buffer.Len() == 0 && output.buffer.Len() > 0) {
		return "", err
	}
	return output.buffer.String(), nil
}
