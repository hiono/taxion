package gitproof

import (
	"fmt"

	"github.com/hiono/taxion/internal/core"
)

// VerifyApprovedInputSuccessor compares input snapshots without manufacturing
// a Coder output or a partial-result receipt.
func VerifyApprovedInputSuccessor(source core.GraphDispatch, task core.GraphTask) error {
	if source.Recipient != "CODER" || source.Instruction.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
		source.Instruction.TargetTreeSHA == "" || task.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
		task.Repo != source.Repository || task.BaselineSHA != source.Instruction.BaselineSHA ||
		task.TargetSHA != source.Instruction.TargetSHA || task.TargetTreeSHA != source.Instruction.TargetTreeSHA ||
		!sameFiles(task.TargetChangedFiles, source.Instruction.TargetChangedFiles) {
		return fmt.Errorf("successor must preserve the exact previously approved input snapshot")
	}
	sourceRoot, err := worktreeRoot(source.Instruction.Worktree)
	if err != nil {
		return err
	}
	root, err := worktreeRoot(task.Worktree)
	if err != nil {
		return err
	}
	sourceGit, err := commonDirectory(sourceRoot)
	if err != nil {
		return err
	}
	targetGit, err := commonDirectory(root)
	if err != nil || sourceGit != targetGit || sourceRoot == root {
		return fmt.Errorf("carried input requires a distinct worktree in the same repository")
	}
	if _, err := ObserveSnapshotStart(source); err != nil {
		return fmt.Errorf("original approved input changed: %w", err)
	}
	candidate := source
	candidate.Instruction.Worktree, candidate.Instruction.Branch = task.Worktree, task.Branch
	candidate.Instruction.AuthorizedActions, candidate.Instruction.ForbiddenActions = task.Scope, task.OutOfScope
	candidate.Instruction.FileAccess = core.CloneFileAccessPolicy(task.FileAccess)
	if _, err := ObserveSnapshotStart(candidate); err != nil {
		return fmt.Errorf("successor approved input differs: %w", err)
	}
	return nil
}
