package gitproof

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// ReviewDiff reads immutable Git objects, never the mutable worktree or external diff helpers.
func ReviewDiff(source, baseline, target string) (string, error) {
	if !fullSHA(baseline) || !fullSHA(target) {
		return "", fmt.Errorf("review diff requires full baseline and target SHA")
	}
	return git(source, "-c", "core.quotePath=true", "diff", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--no-color", baseline, target, "--")
}

// ReverifyReleasedReview checks the immutable target through its source repository
// and confirms that the controller's released worktree remains absent.
func ReverifyReleasedReview(dispatch core.GraphDispatch, result *core.DispatchResult) error {
	if result == nil || result.GitEvidence == nil || !result.GitEvidence.WorktreeReleased ||
		dispatch.ReviewWorktreeSource == "" {
		return fmt.Errorf("released review requires saved evidence and source worktree")
	}
	if err := dispatch.ValidateResult(dispatch.Status, result); err != nil {
		return err
	}
	source := dispatch.ReviewWorktreeSource
	if _, err := os.Lstat(dispatch.Instruction.Worktree); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("released review path must remain absent: %v", err)
	}
	registry, err := git(source, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(registry, "\x00") {
		if entry == "worktree "+dispatch.Instruction.Worktree {
			return fmt.Errorf("released review remains registered")
		}
	}
	remote, err := git(source, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("review source repository is unavailable")
	}
	if err := VerifyRepositoryRemote(dispatch, remote); err != nil {
		return fmt.Errorf("review source repository identity changed")
	}
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		refID := dispatch.RelatedID
		if refID == "" {
			refID = dispatch.ID
		}
		if result.GitEvidence.SnapshotRef != snapshotRef(refID) ||
			result.GitEvidence.TreeSHA != dispatch.Instruction.TargetTreeSHA {
			return fmt.Errorf("released review snapshot evidence differs from its target")
		}
		return verifySnapshotRef(source, refID, dispatch.Instruction.TargetTreeSHA)
	}
	head, err := git(source, "rev-parse", result.OutputSHA+"^{commit}")
	if err != nil || head != result.OutputSHA || head != dispatch.Instruction.TargetSHA {
		return fmt.Errorf("review target commit is unavailable or changed")
	}
	parents, err := git(source, "rev-list", "--parents", "-n", "1", head)
	fields := strings.Fields(parents)
	if err != nil || len(fields) == 0 || fields[0] != head ||
		!slices.Equal(fields[1:], result.GitEvidence.ParentSHAs) {
		return fmt.Errorf("review target parents differ from saved evidence")
	}
	return nil
}

func CreateReviewWorktree(source string, dispatch core.GraphDispatch) error {
	if dispatch.Recipient != "REVIEWER" || !fullSHA(dispatch.Instruction.BaselineSHA) ||
		strings.TrimSpace(source) == "" || !filepath.IsAbs(dispatch.Instruction.Worktree) {
		return fmt.Errorf("review worktree requires a Reviewer dispatch and full target SHA")
	}
	if _, err := os.Lstat(dispatch.Instruction.Worktree); err == nil {
		return fmt.Errorf("review worktree path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect review worktree path: %w", err)
	}
	remote, err := git(source, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if err := VerifyRepositoryRemote(dispatch, remote); err != nil {
		return fmt.Errorf("review source repository does not match the dispatch")
	}
	_, err = git(source, "worktree", "add", "--detach", dispatch.Instruction.Worktree, dispatch.Instruction.BaselineSHA)
	if err != nil {
		return err
	}
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		if err := applyTreeSnapshot(source, dispatch); err != nil {
			return errors.Join(err, removeCreatedWorktree(source, dispatch.Instruction.Worktree))
		}
	}
	return nil
}

// ReleaseReviewWorktree removes a controller-created worktree without --force.
// The clean state, target SHA, and shared repository must still match at release.
func ReleaseReviewWorktree(source string, dispatch core.GraphDispatch, result *core.DispatchResult) error {
	if dispatch.Recipient != "REVIEWER" || result == nil {
		return fmt.Errorf("review worktree release requires a Reviewer result")
	}
	if err := Verify(dispatch, result); err != nil {
		return err
	}
	sourceGit, err := commonDirectory(source)
	if err != nil {
		return err
	}
	reviewGit, err := commonDirectory(dispatch.Instruction.Worktree)
	if err != nil {
		return err
	}
	if sourceGit != reviewGit {
		return fmt.Errorf("review worktree belongs to another Git repository")
	}
	removeArgs := []string{"worktree", "remove"}
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		removeArgs = append(removeArgs, "--force")
	}
	removeArgs = append(removeArgs, dispatch.Instruction.Worktree)
	if _, err := git(source, removeArgs...); err != nil {
		result.GitEvidence = nil
		return fmt.Errorf("release review worktree: %w", err)
	}
	if _, err := os.Lstat(dispatch.Instruction.Worktree); !errors.Is(err, os.ErrNotExist) {
		result.GitEvidence = nil
		return fmt.Errorf("review worktree path remains after release: %v", err)
	}
	registry, err := git(source, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		result.GitEvidence = nil
		return fmt.Errorf("confirm review worktree release: %w", err)
	}
	absolute, err := filepath.Abs(dispatch.Instruction.Worktree)
	if err != nil {
		result.GitEvidence = nil
		return err
	}
	for _, entry := range strings.Split(registry, "\x00") {
		if entry == "worktree "+absolute {
			result.GitEvidence = nil
			return fmt.Errorf("review worktree remains in the Git registry")
		}
	}
	result.GitEvidence.WorktreeReleased = true
	return nil
}

func commonDirectory(worktree string) (string, error) {
	directory, err := git(worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return canonical(directory)
}

// VerifyPreparedReviewWorktree preserves the real index and files. Snapshot
// preparation leaves HEAD and the index at baseline, with the target in files.
func VerifyPreparedReviewWorktree(dispatch core.GraphDispatch, source string) error {
	return VerifyPreparedReviewWorktreeContext(context.Background(), dispatch, source)
}

func VerifyPreparedReviewWorktreeContext(ctx context.Context, dispatch core.GraphDispatch, source string) error {
	err := verifyPreparedReviewWorktree(ctx, dispatch, source)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func verifyPreparedReviewWorktree(ctx context.Context, dispatch core.GraphDispatch, source string) error {
	if dispatch.Recipient != "REVIEWER" || dispatch.ReviewWorktreeSource == "" ||
		dispatch.Instruction.OutputMode != core.OutputModeWorktreeTreeSnapshot {
		return fmt.Errorf("prepared review requires a managed snapshot Reviewer")
	}
	actualSource, err := canonical(dispatch.ReviewWorktreeSource)
	if err != nil {
		return err
	}
	expectedSource, err := canonical(source)
	if err != nil || actualSource != expectedSource {
		return fmt.Errorf("prepared review source differs from the approved candidate")
	}
	root, err := readOnlyGit(ctx, dispatch.Instruction.Worktree, "rev-parse", "--show-toplevel")
	if err != nil || root != filepath.Clean(dispatch.Instruction.Worktree) || root == actualSource {
		return fmt.Errorf("prepared review must retain its exact separate worktree path")
	}
	sourceGit, err := readOnlyGit(ctx, actualSource, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	reviewGit, err := readOnlyGit(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || sourceGit != reviewGit {
		return fmt.Errorf("prepared review belongs to another Git repository")
	}
	registry, err := readOnlyGit(ctx, actualSource, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	registered := false
	for _, entry := range strings.Split(registry, "\x00") {
		registered = registered || entry == "worktree "+root
	}
	if !registered {
		return fmt.Errorf("prepared review is not registered in the candidate repository")
	}
	remote, err := readOnlyGit(ctx, root, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if err := VerifyRepositoryRemote(dispatch, remote); err != nil {
		return fmt.Errorf("prepared review repository identity changed")
	}
	if _, err := readOnlyGit(ctx, root, "symbolic-ref", "--quiet", "HEAD"); err == nil {
		return fmt.Errorf("prepared review must retain detached HEAD")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("prepared review HEAD cannot be verified")
		}
	}
	if _, err := readOnlyGit(ctx, root, "diff", "--cached", "--quiet", "--no-ext-diff",
		"--no-textconv", "--ignore-submodules=none", dispatch.Instruction.BaselineSHA, "--"); err != nil {
		return fmt.Errorf("prepared review index differs from its original baseline")
	}
	if err := checkPortableWorktree(ctx, root, dispatch.Instruction.BaselineSHA, dispatch.Instruction.TargetTreeSHA); err != nil {
		return fmt.Errorf("prepared review target changed: %w", err)
	}
	ignored, err := readOnlyGit(ctx, root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	if ignored != "" {
		return fmt.Errorf("prepared review contains ignored files; preserve and resolve before reconciliation")
	}
	refID := dispatch.RelatedID
	if refID == "" {
		refID = dispatch.ID
	}
	tree, err := readOnlyGit(ctx, actualSource, "rev-parse", "--verify", snapshotRef(refID)+"^{tree}")
	if err != nil || tree != dispatch.Instruction.TargetTreeSHA {
		return fmt.Errorf("prepared review snapshot ref changed")
	}
	return nil
}
