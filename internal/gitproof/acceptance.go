package gitproof

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// VerifySavedSnapshotAcceptance observes existing objects, index and files only.
// A retained Coder may be detached; the current Task must retain its approved branch.
func VerifySavedSnapshotAcceptance(ctx context.Context, coder, reviewer core.GraphDispatch, task core.GraphTask) error {
	for _, d := range []core.GraphDispatch{coder, reviewer} {
		if d.Status != core.DispatchSucceeded || d.Instruction.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
			d.Result == nil || d.Result.GitEvidence == nil {
			return fmt.Errorf("saved acceptance requires successful snapshot results")
		}
		if err := d.ValidateResult(d.Status, d.Result); err != nil {
			return err
		}
	}
	tree := coder.Result.OutputTreeSHA
	if coder.Recipient != "CODER" || reviewer.Recipient != "REVIEWER" || reviewer.ID == coder.ID ||
		reviewer.Relationship != core.RelationshipIndependent || reviewer.RelatedID != coder.ID ||
		reviewer.Result.ReviewVerdict != "APPROVE" || !reviewer.Result.GitEvidence.WorktreeReleased ||
		task.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
		task.Repo != coder.Repository || reviewer.Repository != coder.Repository ||
		task.BaselineSHA != coder.Instruction.BaselineSHA || task.TargetSHA != coder.Result.OutputSHA ||
		reviewer.Result.OutputSHA != task.TargetSHA || reviewer.Result.OutputTreeSHA != tree ||
		task.TargetTreeSHA != tree || !fullSHA(tree) ||
		!slices.Equal(task.TargetChangedFiles, coder.Result.ChangedFiles) ||
		!slices.Equal(reviewer.Instruction.TargetChangedFiles, task.TargetChangedFiles) {
		return fmt.Errorf("saved acceptance target, independent review or changed paths differ")
	}
	if _, err := os.Lstat(reviewer.Instruction.Worktree); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("saved acceptance requires the released Reviewer path to remain absent")
	}
	if coder.ReviewWorktreeSource != "" {
		return fmt.Errorf("saved Coder has an unexpected review source")
	}
	sourceRoot, err := canonical(coder.Instruction.Worktree)
	if err != nil {
		return err
	}
	currentRoot, err := canonical(task.Worktree)
	if err != nil || currentRoot == sourceRoot {
		return fmt.Errorf("saved acceptance requires a distinct current Task worktree")
	}
	reviewSource, err := canonical(reviewer.ReviewWorktreeSource)
	if err != nil || reviewSource != currentRoot {
		return fmt.Errorf("released review source differs from the current Task worktree")
	}
	common := ""
	for _, root := range []string{sourceRoot, currentRoot} {
		value, err := readOnlyGit(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return err
		}
		value, err = canonical(value)
		if err != nil || common != "" && value != common {
			return fmt.Errorf("acceptance worktrees must share the repository object database")
		}
		common = value
		ref := snapshotRef(coder.ID)
		actual, err := readOnlyGit(ctx, root, "rev-parse", "--verify", ref+"^{tree}")
		if err != nil || actual != tree || coder.Result.GitEvidence.SnapshotRef != ref ||
			reviewer.Result.GitEvidence.SnapshotRef != ref {
			return fmt.Errorf("saved snapshot ref differs from the reviewed tree")
		}
	}
	registry, err := readOnlyGit(ctx, currentRoot, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(registry, "\x00") {
		if entry == "worktree "+filepath.Clean(reviewer.Instruction.Worktree) {
			return fmt.Errorf("released review remains registered")
		}
	}
	parents, err := readOnlyGit(ctx, currentRoot, "rev-list", "--parents", "-n", "1", task.TargetSHA)
	fields := strings.Fields(parents)
	if err != nil || len(fields) == 0 || fields[0] != task.TargetSHA ||
		!slices.Equal(fields[1:], coder.Result.GitEvidence.ParentSHAs) ||
		!slices.Equal(fields[1:], reviewer.Result.GitEvidence.ParentSHAs) {
		return fmt.Errorf("saved target commit parents differ")
	}
	paths, err := readOnlyGit(ctx, currentRoot, "diff", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--name-only", "-z", task.BaselineSHA, tree, "--")
	var changed []string
	if paths != "" {
		changed = strings.Split(strings.TrimSuffix(paths, "\x00"), "\x00")
	}
	if err != nil || !sameFiles(changed, task.TargetChangedFiles) {
		return fmt.Errorf("saved target tree changed paths differ")
	}
	retained := coder
	retained.Instruction.TargetTreeSHA = tree
	if err := checkSavedWorktree(ctx, retained, true); err != nil {
		return fmt.Errorf("retained Coder snapshot: %w", err)
	}
	current := coder
	current.Instruction.Worktree, current.Instruction.Branch = task.Worktree, task.Branch
	current.Instruction.TargetSHA, current.Instruction.TargetTreeSHA = task.TargetSHA, tree
	if err := checkSavedWorktree(ctx, current, false); err != nil {
		return fmt.Errorf("current Task snapshot: %w", err)
	}
	return CheckChangedPaths(core.GraphDispatch{Recipient: "CODER", Instruction: core.DispatchInstruction{
		Version: core.DispatchInstructionVersion, AuthorizedActions: task.Scope,
		ForbiddenActions: task.OutOfScope, FileAccess: core.CloneFileAccessPolicy(task.FileAccess),
		Worktree: task.Worktree, BaselineSHA: task.BaselineSHA,
	}}, task.TargetChangedFiles)
}
