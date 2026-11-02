package gitproof

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// ObserveArtifact reads the worktree using a private index. It may create Git
// objects, but does not update the real index, files, branches or retained refs.
func ObserveArtifact(task core.GraphTask, source string, selection []byte) (core.ArtifactTarget, error) {
	return observeArtifact(task, source, selection, false)
}

// ObserveInput records identity under an explicit observation contract, not the
// contract that originally produced the input.
func ObserveInput(task core.GraphTask, source string, selection []byte) (core.ArtifactTarget, error) {
	return observeArtifact(task, source, selection, true)
}

func observeArtifact(task core.GraphTask, source string, selection []byte, precheck bool) (core.ArtifactTarget, error) {
	if task.OutputMode != core.OutputModeWorktreeTreeSnapshot || strings.TrimSpace(source) == "" ||
		source != strings.TrimSpace(source) || source != core.Redact(source) {
		return core.ArtifactTarget{}, fmt.Errorf("artifact input requires snapshot mode and an explicit source")
	}
	root, err := worktreeRoot(task.Worktree)
	if err != nil {
		return core.ArtifactTarget{}, err
	}
	remote, err := git(root, "remote", "get-url", "origin")
	if err != nil {
		return core.ArtifactTarget{}, err
	}
	contract := core.GraphDispatch{Repository: task.Repo, Recipient: "CODER",
		Configuration: &core.DispatchConfiguration{Selection: selection},
		Instruction: core.DispatchInstruction{Version: core.DispatchInstructionVersion,
			Worktree: root, BaselineSHA: task.BaselineSHA,
			AuthorizedActions: task.Scope, ForbiddenActions: task.OutOfScope, FileAccess: task.FileAccess}}
	if err := VerifyRepositoryRemote(contract, remote); err != nil {
		return core.ArtifactTarget{}, err
	}
	head, err := git(root, "rev-parse", "HEAD")
	target := task.TargetSHA
	if target == "" {
		target = task.BaselineSHA
	}
	if err != nil || head != target {
		return core.ArtifactTarget{}, fmt.Errorf("artifact input must retain the approved target commit")
	}
	if _, err := git(root, "merge-base", "--is-ancestor", task.BaselineSHA, head); err != nil {
		return core.ArtifactTarget{}, fmt.Errorf("artifact input baseline must be an ancestor of its target")
	}
	branch, err := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != task.Branch {
		return core.ArtifactTarget{}, fmt.Errorf("artifact input branch differs from the approved Task")
	}
	var paths []string
	if precheck {
		paths, err = candidatePaths(root, task.BaselineSHA)
		if err != nil {
			return core.ArtifactTarget{}, err
		}
		if err := CheckChangedPaths(contract, paths); err != nil {
			return core.ArtifactTarget{}, err
		}
		for _, path := range paths {
			if core.ProhibitedArtifactPath(path) {
				return core.ArtifactTarget{}, fmt.Errorf("input observation cannot capture dotenv or controller management paths")
			}
		}
		if len(paths) != 0 {
			args := append([]string{"-C", root, "-c", "core.fsmonitor=false", "check-attr", "-z", "filter", "--"}, paths...)
			output, err := exec.Command("git", args...).Output()
			if err != nil {
				return core.ArtifactTarget{}, fmt.Errorf("input observation cannot verify Git attributes")
			}
			attributes := strings.Split(string(output), "\x00")
			for i := 2; i < len(attributes); i += 3 {
				if attributes[i] != "unspecified" && attributes[i] != "unset" {
					return core.ArtifactTarget{}, fmt.Errorf("input observation cannot execute Git clean/process filters")
				}
			}
		}
	}
	var tree string
	var files []string
	if precheck {
		tree, files, _, err = captureWorktreePaths(root, task.BaselineSHA, task.BaselineSHA, paths)
	} else {
		tree, files, _, err = captureWorktreeTree(root, task.BaselineSHA, head)
	}
	if err != nil {
		return core.ArtifactTarget{}, err
	}

	if task.TargetTreeSHA != "" && (tree != task.TargetTreeSHA || !sameFiles(files, task.TargetChangedFiles)) {
		return core.ArtifactTarget{}, fmt.Errorf("artifact input differs from its declared tree or changed paths")
	}
	if err := CheckChangedPaths(contract, files); err != nil {
		return core.ArtifactTarget{}, err
	}
	a := core.ArtifactTarget{Version: 1, Repository: task.Repo, BaselineSHA: task.BaselineSHA,
		CommitSHA: head, TreeSHA: tree, ChangedFiles: files, Source: source}
	hash, err := core.CanonicalSHA256(a)
	if err != nil {
		return core.ArtifactTarget{}, err
	}
	a.Hash = "sha256:" + hash
	return a, a.Verify()
}

func candidatePaths(root, baseline string) ([]string, error) {
	// List paths before adding their contents to a private index.
	tracked, err := exec.Command("git", "-C", root, "-c", "core.fsmonitor=false", "diff", "--no-ext-diff", "--no-textconv", "--name-only", "--no-renames", "-z", baseline, "--").Output()
	if err != nil {
		return nil, err
	}
	untracked, err := exec.Command("git", "-C", root, "-c", "core.fsmonitor=false", "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, names := range [][]byte{tracked, untracked} {
		for _, path := range strings.Split(string(names), "\x00") {
			if path != "" && !slices.Contains(files, path) {
				files = append(files, path)
			}
		}
	}
	slices.Sort(files)
	return files, nil
}

func RetainInput(task core.GraphTask, target core.ArtifactTarget, id string, selection []byte) error {
	if err := VerifyInput(task, target, selection); err != nil {
		return err
	}
	return pinSnapshot(task.Worktree, id, target.TreeSHA)
}

func VerifyInputLocation(source, successor, id, tree string, distinct bool) error {
	sourceRoot, err := worktreeRoot(source)
	if err != nil {
		return err
	}
	targetRoot, err := worktreeRoot(successor)
	if err != nil {
		return err
	}
	if distinct && sourceRoot == targetRoot {
		return fmt.Errorf("implementation successor requires a distinct worktree to preserve the observed input")
	}
	sourceGit, err := commonDirectory(sourceRoot)
	if err != nil {
		return err
	}
	targetGit, err := commonDirectory(targetRoot)
	if err != nil || sourceGit != targetGit {
		return fmt.Errorf("observed successor must share the retained source repository object database")
	}
	return verifySnapshotRef(sourceRoot, id, tree)
}

func VerifyArtifact(task core.GraphTask, target core.ArtifactTarget, selection []byte) error {
	return verifyArtifact(task, target, selection, false)
}

func VerifyInput(task core.GraphTask, target core.ArtifactTarget, selection []byte) error {
	return verifyArtifact(task, target, selection, true)
}

func verifyArtifact(task core.GraphTask, target core.ArtifactTarget, selection []byte, precheck bool) error {
	if err := target.Verify(); err != nil {
		return err
	}
	actual, err := observeArtifact(task, target.Source, selection, precheck)
	if err != nil {
		return err
	}
	if actual.Hash != target.Hash {
		return fmt.Errorf("input artifact changed after approval")
	}
	return nil
}

func RetainArtifact(task core.GraphTask, target core.ArtifactTarget, id string, selection []byte) error {
	if err := VerifyArtifact(task, target, selection); err != nil {
		return err
	}
	return pinSnapshot(task.Worktree, id, target.TreeSHA)
}

func ValidateArtifact(ctx context.Context, task core.GraphTask, target core.ArtifactTarget,
	selection []byte) ([]core.DispatchValidation, error) {
	verify := func() error { return VerifyArtifact(task, target, selection) }
	if err := core.CheckReviewValidationCommands(task.Decisions, task.ValidationCommands); err != nil {
		return nil, err
	}
	if err := verify(); err != nil {
		return nil, err
	}
	mode, _ := core.ReviewValidationPolicy(task.Decisions)
	checks, err := executeValidationCommands(ctx, task.Worktree, target.TreeSHA,
		slices.Clone(task.ValidationCommands), mode == "observe")
	if checkErr := verify(); checkErr != nil {
		return checks, errors.Join(err, fmt.Errorf("artifact validation changed its target: %w", checkErr))
	}
	return checks, err
}
