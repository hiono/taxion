package portable

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

type repository struct {
	ID     string            `json:"id"`
	Origin string            `json:"origin"`
	Pack   string            `json:"pack"`
	Roots  []string          `json:"roots"`
	Refs   map[string]string `json:"refs"`
	Paths  []location        `json:"paths"`
}

type location struct {
	Original string `json:"original"`
	Head     string `json:"head"`
	Tree     string `json:"tree"`
	Branch   string `json:"branch,omitempty"`
	Released bool   `json:"released,omitempty"`
}

func git(ctx context.Context, root string, input io.Reader, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null",
		"-c", "core.autocrlf=false"}, args...)...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1")
	command.Stdin = input
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		// Git stderr may contain source credentials or private file contents.
		return nil, fmt.Errorf("portable Git %s failed: %w", args[0], err)
	}
	return output, nil
}

func sha(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 20
}

func collectRepositories(ctx context.Context, state store.Workspace, add func([]byte) (string, error)) ([]repository, error) {
	repos := map[string]*repository{}
	sources := map[string][]string{}
	locate := func(repo, path, head, tree string) error {
		if path == "" || head == "" {
			return nil
		}
		if !filepath.IsAbs(path) || !sha(head) || tree != "" && !sha(tree) {
			return fmt.Errorf("nonportable Git path or object identity")
		}
		if repos[repo] == nil {
			repos[repo] = &repository{ID: repo, Refs: map[string]string{}}
		}
		r := repos[repo]
		for _, object := range []string{head, tree} {
			if object != "" && !slices.Contains(r.Roots, object) {
				r.Roots = append(r.Roots, object)
			}
		}
		original := path
		if state.Records.Restore != nil {
			for _, mapping := range state.Records.Restore.Paths {
				if mapping.Original == path {
					path = mapping.Local
				}
			}
		}
		sources[repo] = append(sources[repo], path)
		for i := range r.Paths {
			if r.Paths[i].Original == original {
				r.Paths[i] = location{Original: original, Head: head, Tree: tree}
				return nil
			}
		}
		r.Paths = append(r.Paths, location{Original: original, Head: head, Tree: tree})
		return nil
	}
	task := func(t core.GraphTask) error {
		if err := locate(t.Repo, t.Worktree, t.BaselineSHA, ""); err != nil {
			return err
		}
		return locate(t.Repo, t.Worktree, first(t.TargetSHA, t.BaselineSHA), t.TargetTreeSHA)
	}
	for _, event := range state.Graph.History {
		if err := task(event.Task); err != nil {
			return nil, err
		}
	}
	for _, t := range state.Graph.Tasks {
		if err := task(t); err != nil {
			return nil, err
		}
	}
	for _, event := range state.Graph.DispatchHistory {
		if event.Result == nil {
			continue
		}
		d, ok := state.Graph.GraphDispatch(event.DispatchID)
		if !ok {
			return nil, fmt.Errorf("historical result has no Dispatch")
		}
		if evidence := event.Result.GitEvidence; evidence != nil {
			if err := locate(d.Repository, evidence.Worktree, evidence.HeadSHA, evidence.TreeSHA); err != nil {
				return nil, err
			}
		}
		if evidence := event.Result.PartialGitEvidence; evidence != nil {
			if err := locate(d.Repository, evidence.Worktree, evidence.HeadSHA, evidence.TreeSHA); err != nil {
				return nil, err
			}
		}
	}
	for _, d := range state.Graph.Dispatches {
		if err := locate(d.Repository, d.Instruction.Worktree, d.Instruction.BaselineSHA, ""); err != nil {
			return nil, err
		}
		if err := locate(d.Repository, d.Instruction.Worktree, d.ExpectedStartSHA(), d.Instruction.TargetTreeSHA); err != nil {
			return nil, err
		}
		result := d.Result
		if result == nil {
			continue
		}
		if result.GitEvidence != nil {
			e := result.GitEvidence
			if err := locate(d.Repository, e.Worktree, e.HeadSHA, first(result.OutputTreeSHA, e.TreeSHA)); err != nil {
				return nil, err
			}
		}
		if result.PartialGitEvidence != nil {
			e := result.PartialGitEvidence
			if err := locate(d.Repository, e.Worktree, e.HeadSHA, e.TreeSHA); err != nil {
				return nil, err
			}
		}
		tree := result.OutputTreeSHA
		if result.PartialGitEvidence != nil {
			tree = result.PartialGitEvidence.TreeSHA
		}
		if tree != "" && d.Recipient == "CODER" {
			digest := sha256.Sum256([]byte(d.ID))
			repos[d.Repository].Refs["refs/taxion/snapshots/"+hex.EncodeToString(digest[:])] = tree
		}
		if d.ReviewWorktreeSource != "" {
			sources[d.Repository] = append(sources[d.Repository], state.Records.RestoredDispatch(d).ReviewWorktreeSource)
		}
	}
	var result []repository
	ids := mapKeys(repos)
	slices.Sort(ids)
	for _, id := range ids {
		r := repos[id]
		for i := range r.Paths {
			for _, task := range state.Graph.Tasks {
				if task.Worktree == r.Paths[i].Original && task.Owner == "CODER" {
					r.Paths[i].Branch = task.Branch
				}
			}
			for _, d := range state.Graph.Dispatches {
				if d.Instruction.Worktree != r.Paths[i].Original {
					continue
				}
				if d.Recipient == "CODER" {
					r.Paths[i].Branch = d.Instruction.Branch
				}
				if d.Result != nil && d.Result.GitEvidence != nil {
					r.Paths[i].Released = d.Result.GitEvidence.WorktreeReleased
				}
			}
		}
		var source string
		for _, candidate := range sources[id] {
			if _, err := os.Stat(candidate); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return nil, err
			}
			remote, err := git(ctx, candidate, nil, "remote", "get-url", "origin")
			if err != nil {
				return nil, err
			}
			for _, d := range state.Graph.Dispatches {
				if d.Repository == id {
					if err := gitproof.VerifyRepositoryRemote(d, strings.TrimSpace(string(remote))); err != nil {
						return nil, err
					}
				}
			}
			origin, err := safeOrigin(strings.TrimSpace(string(remote)), id)
			if err != nil {
				return nil, err
			}
			r.Origin, source = origin, candidate
			break
		}
		if source == "" {
			return nil, fmt.Errorf("portable snapshot requires an available source for repository %s", id)
		}
		for _, location := range r.Paths {
			path := location.Original
			if state.Records.Restore != nil {
				for _, mapping := range state.Records.Restore.Paths {
					if mapping.Original == path {
						path = mapping.Local
						break
					}
				}
			}
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return nil, err
			}
			if err := gitproof.CheckPortableWorktree(path, location.Head, location.Tree); err != nil {
				return nil, err
			}
		}
		slices.Sort(r.Roots)
		objects, err := git(ctx, source, strings.NewReader(strings.Join(r.Roots, "\n")+"\n"),
			"rev-list", "--objects", "--no-object-names", "--stdin")
		if err != nil {
			return nil, err
		}
		content, err := git(ctx, source, bytes.NewReader(objects), "cat-file", "--batch")
		if err != nil {
			return nil, err
		}
		if err := checkPortableObjects(content); err != nil {
			return nil, err
		}
		pack, err := git(ctx, source, strings.NewReader(strings.Join(r.Roots, "\n")+"\n"), "pack-objects", "--stdout", "--revs")
		if err != nil {
			return nil, err
		}
		r.Pack, err = add(pack)
		if err != nil {
			return nil, err
		}
		result = append(result, *r)
	}
	return result, nil
}

func checkPortableObjects(data []byte) error {
	if err := checkCredentials(data); err != nil {
		return err
	}
	if bytes.Contains(data, []byte("version https://git-lfs.github.com/spec/v1\n")) {
		return fmt.Errorf("portable snapshot requires Git LFS objects, which are not supported")
	}
	for len(data) != 0 {
		header, rest, ok := bytes.Cut(data, []byte{'\n'})
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 {
			return fmt.Errorf("incomplete portable Git object header")
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 || size >= len(rest) || rest[size] != '\n' {
			return fmt.Errorf("incomplete portable Git object content")
		}
		if fields[1] == "tree" {
			for tree := rest[:size]; len(tree) != 0; {
				entry, objects, ok := bytes.Cut(tree, []byte{0})
				if !ok || len(objects) < 20 {
					return fmt.Errorf("invalid portable Git tree")
				}
				mode, name, ok := strings.Cut(string(entry), " ")
				if !ok {
					return fmt.Errorf("invalid portable Git tree entry")
				}
				if mode == "160000" {
					return fmt.Errorf("portable snapshot cannot include unresolved submodule content")
				}
				if name == ".taxion" || name == ".env" || strings.HasPrefix(name, ".env.") &&
					name != ".env.example" && name != ".env.sample" && name != ".env.template" {
					return fmt.Errorf("portable Git objects contain prohibited local state or dotenv paths")
				}
				tree = objects[20:]
			}
		}
		data = rest[size+1:]
	}
	return nil
}

func checkCredentials(data []byte) error {
	for _, rule := range memory.DefaultRules {
		if (rule.Name == "key" || rule.Name == "token") && rule.Re.Match(data) {
			return fmt.Errorf("portable snapshot rejected credential-like content; review source locally (matched values are not printed)")
		}
	}
	return nil
}

func mapKeys[V any](m map[string]V) []string {
	var keys []string
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func safeOrigin(remote, repository string) (string, error) {
	if gitproof.RepositoryIdentity(remote) != repository {
		return "", fmt.Errorf("portable repository origin differs")
	}
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		return "https://" + parsed.Host + "/" + repository + ".git", nil
	}
	if colon := strings.IndexByte(remote, ':'); colon >= 0 {
		host := remote[:colon]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		if host != "" && !strings.ContainsAny(host, "/\\ \r\n") {
			return "https://" + host + "/" + repository + ".git", nil
		}
	}
	return "", fmt.Errorf("portable snapshot requires a network repository identity, not a local origin")
}
