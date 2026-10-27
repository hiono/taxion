package gitproof

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// ObserveArtifact reads the worktree using a private index. It may create Git
// objects, but does not update the real index, files, branches or retained refs.
func ObserveArtifact(task core.GraphTask, source string, selection []byte) (core.ArtifactTarget, error) {
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
	if err != nil || head != task.BaselineSHA || task.TargetSHA != "" && task.TargetSHA != head {
		return core.ArtifactTarget{}, fmt.Errorf("artifact input must retain the approved baseline commit")
	}
	branch, err := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != task.Branch {
		return core.ArtifactTarget{}, fmt.Errorf("artifact input branch differs from the approved Task")
	}
	tree, files, _, err := captureWorktreeTree(root, task.BaselineSHA, head)
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

func VerifyArtifact(task core.GraphTask, target core.ArtifactTarget, selection []byte) error {
	if err := target.Verify(); err != nil {
		return err
	}
	actual, err := ObserveArtifact(task, target.Source, selection)
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
	if len(task.ValidationCommands) == 0 {
		return nil, fmt.Errorf("artifact validation requires approved commands")
	}
	if err := verify(); err != nil {
		return nil, err
	}
	checks, err := executeValidationCommands(ctx, task.Worktree, target.TreeSHA,
		slices.Clone(task.ValidationCommands), false)
	if checkErr := verify(); checkErr != nil {
		return checks, errors.Join(err, fmt.Errorf("artifact validation changed its target: %w", checkErr))
	}
	return checks, err
}
