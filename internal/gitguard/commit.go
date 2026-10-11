package gitguard

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
)

// Commit publishes only the scanned tree using a compare-and-swap branch update.
// It holds the real index lock, ignores ambient Git redirection, and never stages
// working files or invokes project hooks. The caller owns Dispatch authorization.
func Commit(ctx context.Context, dispatch core.GraphDispatch, message, scanner string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if dispatch.Recipient != "CODER" || dispatch.Instruction.CommitMessagePolicy != core.CommitMessagePolicy ||
		dispatch.ExecutionStartSHA == "" {
		return "", fmt.Errorf("managed commit requires a Coder execution start and supported commit policy")
	}
	if core.ValidateCommitMessage(message) != nil {
		return "", fmt.Errorf("managed commit message violates the Conventional/Contextual policy")
	}
	var environment []string
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			environment = append(environment, variable)
		}
	}
	environment = append(environment, "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1")
	directory := dispatch.Instruction.Worktree
	git := func(input string, args ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
		command.Env = environment
		if input != "" {
			command.Stdin = strings.NewReader(input)
		}
		output, err := command.Output()
		if ctx.Err() != nil {
			return "", fmt.Errorf("managed Git operation interrupted: %w", ctx.Err())
		}
		if err != nil {
			return "", fmt.Errorf("managed Git operation %s failed: %w", args[0], err)
		}
		return strings.TrimSuffix(string(output), "\n"), nil
	}
	root, err := git("", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	requested, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve managed worktree: %w", err)
	}
	requested, err = filepath.EvalSymlinks(requested)
	if err != nil || requested != root {
		return "", fmt.Errorf("managed commit worktree must be the Git root")
	}
	remote, err := git("", "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	// Use the same legacy repository identity contract as Git evidence.
	if !strings.EqualFold(gitproof.RepositoryIdentity(remote), dispatch.Repository) {
		return "", fmt.Errorf("managed commit origin does not match Dispatch repository")
	}
	branch, err := git("", "symbolic-ref", "--quiet", "HEAD")
	if err != nil || branch != "refs/heads/"+dispatch.Instruction.Branch {
		return "", fmt.Errorf("managed commit branch does not match Dispatch branch")
	}
	head, err := git("", "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	for _, start := range []string{dispatch.Instruction.BaselineSHA, dispatch.ExecutionStartSHA} {
		if _, err := hex.DecodeString(start); err != nil || (len(start) != 40 && len(start) != 64) {
			return "", fmt.Errorf("managed commit requires full baseline and execution-start SHAs")
		}
		if _, err := git("", "merge-base", "--is-ancestor", start, head); err != nil {
			return "", fmt.Errorf("managed commit HEAD does not descend from the Dispatch start")
		}
	}
	commits, err := git("", "rev-list", dispatch.ExecutionStartSHA+".."+head)
	if err != nil {
		return "", err
	}
	for _, sha := range strings.Fields(commits) {
		existingMessage, err := git("", "show", "-s", "--format=%B", sha)
		if err != nil || core.ValidateCommitMessage(existingMessage) != nil {
			return "", fmt.Errorf("existing authored commit violates the Dispatch commit policy")
		}
	}
	for _, state := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		path, err := git("", "rev-parse", "--path-format=absolute", "--git-path", state)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(path); err == nil {
			return "", fmt.Errorf("managed commit requires no merge, rebase, or sequencer operation in progress")
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect managed Git operation state: %w", err)
		}
	}
	indexPath, err := git("", "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	lock, err := os.OpenFile(indexPath+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("lock managed commit index: %w", err)
	}
	defer os.Remove(lock.Name())
	if err := lock.Close(); err != nil {
		return "", fmt.Errorf("close managed index lock: %w", err)
	}
	index, err := os.ReadFile(indexPath)
	if err != nil {
		return "", fmt.Errorf("read managed commit index: %w", err)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(indexPath), "taxion-commit-")
	if err != nil {
		return "", fmt.Errorf("prepare managed commit snapshot: %w", err)
	}
	defer os.RemoveAll(temporary)
	snapshot := filepath.Join(temporary, "index")
	if err := os.WriteFile(snapshot, index, 0600); err != nil {
		return "", fmt.Errorf("copy managed commit snapshot: %w", err)
	}
	environment = append(environment, "GIT_INDEX_FILE="+snapshot)
	// Split indexes resolve sharedindex files relative to their own directory.
	// Copy the referenced shared index next to the private index before conversion.
	shared, err := git("", "rev-parse", "--shared-index-path")
	if err != nil {
		return "", err
	}
	if shared != "" {
		if !filepath.IsAbs(shared) {
			shared = filepath.Join(directory, shared)
		}
		content, err := os.ReadFile(shared)
		if err != nil {
			return "", fmt.Errorf("read shared index snapshot: %w", err)
		}
		if err := os.WriteFile(filepath.Join(temporary, filepath.Base(shared)), content, 0600); err != nil {
			return "", fmt.Errorf("copy shared index snapshot: %w", err)
		}
	}
	if _, err := git("", "update-index", "--no-split-index"); err != nil {
		return "", err
	}
	if err := checkSecrets(ctx, directory, scanner, environment, message); err != nil {
		return "", err
	}
	tree, err := git("", "write-tree")
	if err != nil {
		return "", err
	}
	currentTree, err := git("", "rev-parse", head+"^{tree}")
	if err != nil {
		return "", err
	}
	if tree == currentTree {
		return "", fmt.Errorf("managed commit requires staged changes")
	}
	for _, from := range []string{head, dispatch.Instruction.BaselineSHA} {
		files, err := git("", "diff", "--no-renames", "--name-only", "-z", from, tree)
		if err != nil {
			return "", err
		}
		if files != "" {
			if err := gitproof.CheckChangedPaths(dispatch, strings.Split(strings.TrimSuffix(files, "\x00"), "\x00")); err != nil {
				return "", fmt.Errorf("managed commit contains a path outside the authorized scope")
			}
		}
	}
	sign, err := git("", "config", "--type=bool", "--default=false", "--get", "commit.gpgsign")
	if err != nil {
		return "", err
	}
	arguments := []string{"commit-tree", tree, "-p", head}
	if sign == "true" {
		arguments = append(arguments, "-S")
	}
	sha, err := git(message, arguments...)
	if err != nil {
		return "", err
	}
	currentBranch, err := git("", "symbolic-ref", "--quiet", "HEAD")
	if err != nil || currentBranch != branch {
		return "", fmt.Errorf("managed commit branch changed before publication")
	}
	publication := exec.CommandContext(ctx, "git", "-C", directory, "update-ref", "-m", "taxion managed commit", branch, sha, head)
	publication.Env = environment
	if err := publication.Run(); err != nil {
		return "", fmt.Errorf("managed commit branch update failed; publication of candidate %s was not confirmed; inspect the branch before retrying", sha)
	}
	return sha, nil
}
