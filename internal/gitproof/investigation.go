package gitproof

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/hiono/taxion/internal/core"
)

type InvestigatedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type limitedOutput struct {
	strings.Builder
	remaining int
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, fmt.Errorf("Git investigation exceeds its byte ceiling")
	}
	w.remaining -= len(p)
	return w.Builder.Write(p)
}

// ReadCommittedFiles never reads the mutable worktree or runs user commands,
// filters, textconv, hooks or external diff drivers.
func ReadCommittedFiles(ctx context.Context, worktree, repository, sha string, scope, forbidden, paths []string, maxBytes int) ([]InvestigatedFile, error) {
	if !fullSHA(sha) || len(paths) == 0 || len(paths) > 16 || maxBytes <= 0 || maxBytes > 64*1024 {
		return nil, fmt.Errorf("Git investigation requires a full SHA, 1-16 paths and a byte ceiling up to 64 KiB")
	}
	for _, path := range paths {
		if path == "" || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path ||
			path == ".." || strings.HasPrefix(path, "../") || strings.ContainsAny(path, "\\\x00") {
			return nil, fmt.Errorf("Git investigation requires canonical repository-relative file paths")
		}
	}
	dispatch := core.GraphDispatch{Recipient: "CODER", Instruction: core.DispatchInstruction{
		AuthorizedActions: scope, ForbiddenActions: forbidden,
	}}
	if err := CheckChangedPaths(dispatch, paths); err != nil {
		return nil, err
	}
	run := func(limit int, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "-C", worktree}, args...)...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GIT_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0")
		output := &limitedOutput{remaining: limit}
		cmd.Stdout = output
		// Provider/repository details are not included in error messages.
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("Git investigation command failed: %w", err)
		}
		return output.String(), nil
	}
	root, err := run(4096, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	actual, err := canonical(strings.TrimSpace(root))
	if err != nil {
		return nil, err
	}
	requested, err := canonical(worktree)
	if err != nil || actual != requested {
		return nil, fmt.Errorf("Git investigation worktree must be the repository root")
	}
	remote, err := run(4096, "remote", "get-url", "origin")
	if err != nil || !strings.EqualFold(repositoryIdentity(strings.TrimSpace(remote)), repository) {
		return nil, fmt.Errorf("Git investigation origin differs from the approved repository")
	}
	resolved, err := run(128, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil || strings.TrimSpace(resolved) != sha {
		return nil, fmt.Errorf("Git investigation commit differs from the selected SHA")
	}
	files := make([]InvestigatedFile, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			return nil, fmt.Errorf("Git investigation paths must be unique")
		}
		seen[path] = true
		kind, err := run(32, "cat-file", "-t", sha+":"+path)
		if err != nil || strings.TrimSpace(kind) != "blob" {
			return nil, fmt.Errorf("Git investigation path must identify a committed file")
		}
		content, err := run(maxBytes, "cat-file", "blob", sha+":"+path)
		if err != nil {
			return nil, err
		}
		if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
			return nil, fmt.Errorf("Git investigation supports only UTF-8 text files without NUL bytes")
		}
		maxBytes -= len(content)
		files = append(files, InvestigatedFile{Path: path, Content: content})
	}
	return files, nil
}
