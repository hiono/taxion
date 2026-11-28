package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxWorkspaceDiffBytes = 8 * 1024 * 1024

type workspaceFile struct {
	mode  string
	hash  [32]byte
	data  []byte
	path  string
	large bool
}

type workspaceBaseline struct {
	directory string
	files     map[string]workspaceFile
	ignored   map[string]bool
	index     map[string]indexEntry
	snapshot  string
}

type indexEntry struct {
	mode string
	oid  string
}

func workspacePaths(ctx context.Context, directory string) (map[string]bool, map[string]bool, map[string]indexEntry, error) {
	tracked, err := workspaceGit(ctx, directory, false, "ls-files", "--cached", "--stage", "-z", "--", ".")
	if err != nil {
		return nil, nil, nil, err
	}
	paths := make(map[string]bool)
	index := make(map[string]indexEntry)
	for _, entry := range splitGitPaths(tracked) {
		meta, path, ok := strings.Cut(entry, "\t")
		if !ok {
			return nil, nil, nil, errors.New("invalid Git index entry")
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 || fields[2] != "0" {
			return nil, nil, nil, errors.New("unmerged or invalid Git index entry")
		}
		index[path] = indexEntry{mode: fields[0], oid: fields[1]}
		paths[path] = fields[0] == "160000"
	}
	others, err := workspaceGit(ctx, directory, false, "ls-files", "--others", "--exclude-standard", "-z", "--", ".")
	if err != nil {
		return nil, nil, nil, err
	}
	for _, path := range splitGitPaths(others) {
		paths[path] = false
	}
	all, err := workspaceGit(ctx, directory, false, "ls-files", "--others", "-z", "--", ".")
	if err != nil {
		return nil, nil, nil, err
	}
	ignored := make(map[string]bool)
	for _, path := range splitGitPaths(all) {
		if _, ok := paths[path]; !ok {
			ignored[path] = true
		}
	}
	return paths, ignored, index, nil
}

func captureWorkspaceBaseline(ctx context.Context, directory string) (*workspaceBaseline, error) {
	inside, err := workspaceGit(ctx, directory, false, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	if strings.TrimSpace(inside) != "true" {
		return nil, nil
	}
	paths, ignored, index, err := workspacePaths(ctx, directory)
	if err != nil {
		return nil, fmt.Errorf("list workspace files: %w", err)
	}
	snapshot, err := os.MkdirTemp("", "taxion-workspace-")
	if err != nil {
		return nil, err
	}
	files, err := snapshotWorkspace(ctx, directory, paths, snapshot)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(snapshot))
	}
	return &workspaceBaseline{directory: directory, files: files, ignored: ignored, index: index, snapshot: snapshot}, nil
}

func (b *workspaceBaseline) close() error {
	if b == nil {
		return nil
	}
	return os.RemoveAll(b.snapshot)
}

func snapshotWorkspace(ctx context.Context, directory string, paths map[string]bool, snapshot string) (map[string]workspaceFile, error) {
	root, err := openWorkspaceRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := make(map[string]workspaceFile)
	for path, submodule := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, exists, err := readWorkspaceFile(ctx, root, path, submodule)
		if err != nil {
			return nil, fmt.Errorf("snapshot %q: %w", path, err)
		}
		if exists {
			if len(f.data) > 0 {
				copy, err := os.CreateTemp(snapshot, "file-")
				if err != nil {
					return nil, err
				}
				f.path = copy.Name()
				_, writeErr := copy.Write(f.data)
				closeErr := copy.Close()
				if err := errors.Join(writeErr, closeErr); err != nil {
					return nil, err
				}
				f.data = nil
			}
			files[path] = f
		}
	}
	return files, nil
}

func (b *workspaceBaseline) diff(ctx context.Context) (result string, resultErr error) {
	if b == nil {
		return "Git workspace diff unavailable; actual file changes were not inspected.", nil
	}
	paths, ignored, index, err := workspacePaths(ctx, b.directory)
	if err != nil {
		return "", fmt.Errorf("list workspace files: %w", err)
	}
	for path, f := range b.files {
		if !ignored[path] {
			if _, ok := paths[path]; !ok {
				paths[path] = f.mode == "160000"
			}
		}
	}
	for path, entry := range b.index {
		if !ignored[path] {
			if _, ok := paths[path]; !ok {
				paths[path] = entry.mode == "160000"
			}
		}
	}
	for path := range b.ignored {
		delete(paths, path)
	}
	snapshot, err := os.MkdirTemp("", "taxion-workspace-")
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(snapshot)) }()
	current, err := snapshotWorkspace(ctx, b.directory, paths, snapshot)
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(paths))
	for path := range paths {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	var output strings.Builder
	output.WriteString("Git workspace changes relative to the pre-run workspace. Ignored untracked files and paths ignored at the start (even if later staged), changes outside the working directory, binary contents, and submodule contents are not included. The workspace may change during or after inspection.\n\n")
	for _, path := range keys {
		old, before := b.files[path]
		now, after := current[path]
		if before && after && old.mode == now.mode && old.hash == now.hash {
			continue
		}
		if !before && !after {
			continue
		}
		if old.large || now.large {
			return "", fmt.Errorf("changed file %q exceeds the input size limit", path)
		}
		text, err := workspaceFileDiff(ctx, path, old, now, before, after)
		if err != nil {
			return "", fmt.Errorf("diff %q: %w", path, err)
		}
		if output.Len()+len(text) > maxWorkspaceDiffBytes {
			return "", errors.New("workspace diff exceeds the input size limit")
		}
		output.WriteString(text)
	}
	indexKeys := append([]string(nil), keys...)
	for path := range b.index {
		if ignored[path] && !b.ignored[path] {
			indexKeys = append(indexKeys, path)
		}
	}
	sort.Strings(indexKeys)
	for _, path := range indexKeys {
		oldIndex, hadIndex := b.index[path]
		newIndex, hasIndex := index[path]
		if hadIndex == hasIndex && oldIndex == newIndex {
			continue
		}
		var text strings.Builder
		action := "changed"
		if !hadIndex {
			action = "added"
		} else if !hasIndex {
			action = "removed"
		}
		fmt.Fprintf(&text, "Index entry %s: %s\n", action, strconv.Quote(path))
		if hadIndex && hasIndex && oldIndex.mode != newIndex.mode {
			fmt.Fprintf(&text, "old mode %s\nnew mode %s\n", oldIndex.mode, newIndex.mode)
		}
		if oldIndex.mode == "160000" || newIndex.mode == "160000" {
			if oldIndex.mode == "160000" {
				fmt.Fprintf(&text, "-Subproject commit %s\n", oldIndex.oid)
			}
			if newIndex.mode == "160000" {
				fmt.Fprintf(&text, "+Subproject commit %s\n", newIndex.oid)
			}
		}
		if hasIndex && newIndex.mode != "160000" && (!hadIndex || oldIndex.oid != newIndex.oid) {
			content, err := workspaceGit(ctx, b.directory, false, "cat-file", "blob", newIndex.oid)
			if err != nil {
				return "", fmt.Errorf("read staged file %q: %w", path, err)
			}
			staged, err := snapshotData(strings.NewReader(content), newIndex.mode)
			if err != nil {
				return "", err
			}
			old, before := b.files[path]
			if hadIndex {
				before = oldIndex.mode != "160000"
				old = workspaceFile{}
				if before {
					content, err := workspaceGit(ctx, b.directory, false, "cat-file", "blob", oldIndex.oid)
					if err != nil {
						return "", fmt.Errorf("read pre-run index file %q: %w", path, err)
					}
					old, err = snapshotData(strings.NewReader(content), oldIndex.mode)
					if err != nil {
						return "", err
					}
				}
			}
			if old.large {
				return "", fmt.Errorf("changed file %q exceeds the input size limit", path)
			}
			if !before || old.mode != staged.mode || old.hash != staged.hash {
				if hadIndex && b.files[path].large {
					return "", fmt.Errorf("pre-run worktree file %q exceeds the input size limit", path)
				}
				stagedDiff, err := workspaceFileDiff(ctx, path, old, staged, before, true)
				if err != nil {
					return "", fmt.Errorf("diff staged file %q: %w", path, err)
				}
				if hadIndex && b.files[path].hash != old.hash {
					stagedDiff, err = redactPreRunWorktreeAdditions(stagedDiff, b.files[path])
					if err != nil {
						return "", err
					}
				}
				text.WriteString("Staged content relative to the pre-run index (or pre-run untracked file):\n")
				text.WriteString(stagedDiff)
			}
		}
		if output.Len()+text.Len() > maxWorkspaceDiffBytes {
			return "", errors.New("workspace diff exceeds the input size limit")
		}
		output.WriteString(text.String())
	}
	return output.String(), nil
}

func redactPreRunWorktreeAdditions(diff string, old workspaceFile) (string, error) {
	if old.mode == "160000" {
		return diff, nil
	}
	data, err := old.content()
	if err != nil {
		return "", err
	}
	lines := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		lines[line] = true
	}
	var result strings.Builder
	inHunk := false
	for _, line := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
		} else if inHunk && strings.HasPrefix(line, "+") && lines[strings.TrimSuffix(line[1:], "\n")] {
			result.WriteString("+[pre-run worktree content omitted]\n")
			continue
		}
		result.WriteString(line)
	}
	return result.String(), nil
}

func workspaceFileDiff(ctx context.Context, path string, old, now workspaceFile, before, after bool) (string, error) {
	oldData, err := old.content()
	if err != nil {
		return "", err
	}
	nowData, err := now.content()
	if err != nil {
		return "", err
	}
	name := strconv.Quote(path)
	var header strings.Builder
	fmt.Fprintf(&header, "diff --git %s %s\n", strconv.Quote("a/"+path), strconv.Quote("b/"+path))
	if !before {
		fmt.Fprintf(&header, "new file mode %s\n", now.mode)
	} else if !after {
		fmt.Fprintf(&header, "deleted file mode %s\n", old.mode)
	} else if old.mode != now.mode {
		fmt.Fprintf(&header, "old mode %s\nnew mode %s\n", old.mode, now.mode)
	}
	if old.mode == "160000" || now.mode == "160000" {
		if before {
			fmt.Fprintf(&header, "-Subproject commit %s\n", oldData)
		}
		if after {
			fmt.Fprintf(&header, "+Subproject commit %s\n", nowData)
		}
		return header.String(), nil
	}
	if bytes.IndexByte(oldData, 0) >= 0 || bytes.IndexByte(nowData, 0) >= 0 || !utf8.Valid(oldData) || !utf8.Valid(nowData) {
		fmt.Fprintf(&header, "Binary file %s changed (contents omitted)\n", name)
		return header.String(), nil
	}
	body, err := diffWorkspaceBytes(ctx, oldData, nowData, before, after)
	if err != nil {
		return "", err
	}
	if body == "" {
		return header.String(), nil
	}
	lines := strings.SplitAfter(body, "\n")
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "), strings.HasPrefix(line, "new file mode "), strings.HasPrefix(line, "deleted file mode "):
		case strings.HasPrefix(line, "--- "):
			if before {
				fmt.Fprintf(&header, "--- %s\n", strconv.Quote("a/"+path))
			} else {
				header.WriteString("--- /dev/null\n")
			}
		case strings.HasPrefix(line, "+++ "):
			if after {
				fmt.Fprintf(&header, "+++ %s\n", strconv.Quote("b/"+path))
			} else {
				header.WriteString("+++ /dev/null\n")
			}
		case strings.HasPrefix(line, "Binary files "):
			fmt.Fprintf(&header, "Binary file %s changed (contents omitted)\n", name)
		default:
			header.WriteString(line)
		}
	}
	return header.String(), nil
}

func (f workspaceFile) content() ([]byte, error) {
	if f.path != "" {
		return os.ReadFile(f.path)
	}
	return f.data, nil
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
		if strings.HasPrefix(name, "GIT_") && name != "GIT_CONFIG_GLOBAL" && name != "GIT_CONFIG_SYSTEM" && name != "GIT_CONFIG_NOSYSTEM" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
}

func workspaceGit(ctx context.Context, directory string, allowDiffExit bool, args ...string) (string, error) {
	args = append([]string{"-c", "core.fsmonitor=false"}, args...)
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	command.Env = append(workspaceGitEnvironment(), "GIT_ALLOW_PROTOCOL=")
	var output, stderr limitedGitOutput
	command.Stdout, command.Stderr = &output, &stderr
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

func snapshotData(r io.Reader, mode string) (workspaceFile, error) {
	h := sha256.New()
	var data bytes.Buffer
	limited := &io.LimitedReader{R: r, N: maxWorkspaceDiffBytes + 1}
	if _, err := io.Copy(io.MultiWriter(h, &data), limited); err != nil {
		return workspaceFile{}, err
	}
	large := data.Len() > maxWorkspaceDiffBytes
	content := data.Bytes()
	if large {
		if _, err := io.Copy(h, r); err != nil {
			return workspaceFile{}, err
		}
		content = nil
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return workspaceFile{mode: mode, hash: sum, data: content, large: large}, nil
}
