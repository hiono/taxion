package gitproof

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

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
	if !strings.EqualFold(repositoryIdentity(remote), dispatch.Repository) {
		return fmt.Errorf("review source repository does not match the dispatch")
	}
	_, err = git(source, "worktree", "add", "--detach", dispatch.Instruction.Worktree, dispatch.Instruction.BaselineSHA)
	return err
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
	if _, err := git(source, "worktree", "remove", dispatch.Instruction.Worktree); err != nil {
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
