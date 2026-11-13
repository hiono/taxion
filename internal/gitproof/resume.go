package gitproof

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// CheckResumeStart observes an untouched Coder worktree. It does not refresh
// the index, capture a snapshot, create objects, or run validation commands.
func CheckResumeStart(ctx context.Context, dispatch core.GraphDispatch) error {
	return checkSavedWorktree(ctx, dispatch, false)
}

// CheckResumeReview reads the immutable candidate used by preparation. Mutable
// source files are not the Reviewer input; preparation uses the saved Git objects.
func CheckResumeReview(ctx context.Context, dispatch core.GraphDispatch, source string) error {
	if dispatch.Recipient != "REVIEWER" || !fullSHA(dispatch.Instruction.BaselineSHA) {
		return fmt.Errorf("resume requires the saved Reviewer candidate")
	}
	remote, err := readOnlyGit(ctx, source, "remote", "get-url", "origin")
	if err != nil || VerifyRepositoryRemote(dispatch, remote) != nil {
		return fmt.Errorf("resume review source repository identity differs")
	}
	target, err := readOnlyGit(ctx, source, "rev-parse", "--verify", dispatch.Instruction.BaselineSHA+"^{commit}")
	if err != nil || target != dispatch.Instruction.BaselineSHA {
		return fmt.Errorf("resume review candidate commit is unavailable")
	}
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		refID := dispatch.RelatedID
		if refID == "" {
			refID = dispatch.ID
		}
		tree, err := readOnlyGit(ctx, source, "rev-parse", "--verify", snapshotRef(refID)+"^{tree}")
		if err != nil || tree != dispatch.Instruction.TargetTreeSHA {
			return fmt.Errorf("resume review snapshot differs from the issued target")
		}
	}
	if dispatch.ReviewWorktreeSource == "" {
		if _, err := os.Lstat(dispatch.Instruction.Worktree); !os.IsNotExist(err) {
			return fmt.Errorf("resume review destination exists without a managed preparation record")
		}
		return nil
	}
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		return verifyPreparedReviewWorktree(ctx, dispatch, source)
	}
	expectedSource, err := canonical(source)
	if err != nil {
		return err
	}
	actualSource, err := canonical(dispatch.ReviewWorktreeSource)
	if err != nil || actualSource != expectedSource {
		return fmt.Errorf("resume prepared review source differs")
	}
	sourceCommon, err := readOnlyGit(ctx, source, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	reviewCommon, err := readOnlyGit(ctx, dispatch.Instruction.Worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || reviewCommon != sourceCommon {
		return fmt.Errorf("resume prepared review belongs to another repository")
	}
	dispatch.Recipient = "CODER"
	return checkSavedWorktree(ctx, dispatch, true)
}

func readOnlyGit(ctx context.Context, directory string, args ...string) (string, error) {
	cmd := observationGitCommand(ctx, directory, "", args...)
	output := &limitedOutput{remaining: 64 * 1024}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("read-only Git observation %s failed: %w", args[0], err)
	}
	return strings.TrimSpace(output.String()), nil
}

func readOnlyGitWithIndex(ctx context.Context, directory, index string, args ...string) (string, error) {
	output, err := observationGitCommand(ctx, directory, index, args...).Output()
	if err != nil {
		return "", fmt.Errorf("read-only Git observation %s failed: %w", args[0], err)
	}
	return strings.TrimSpace(string(output)), nil
}

func observationGitCommand(ctx context.Context, directory, index string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{
		"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
		"-c", "core.untrackedCache=false", "-c", "core.filemode=true", "-c", "core.symlinks=true",
		"-c", "core.ignoreStat=false", "-c", "core.trustctime=true", "-c", "core.checkStat=default",
		"-C", directory,
	}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
	if index != "" {
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+index)
	}
	return cmd
}

func checkSavedWorktree(ctx context.Context, dispatch core.GraphDispatch, allowDetached bool) error {
	run := func(args ...string) (string, error) {
		return readOnlyGit(ctx, dispatch.Instruction.Worktree, args...)
	}
	instruction := dispatch.Instruction
	if dispatch.Recipient != "CODER" || !fullSHA(instruction.BaselineSHA) || !fullSHA(dispatch.ExpectedStartSHA()) {
		return fmt.Errorf("resume requires an exact Coder execution-start identity")
	}
	root, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	actual, err := canonical(root)
	if err != nil {
		return fmt.Errorf("resume worktree root is unavailable")
	}
	expected, err := canonical(instruction.Worktree)
	if err != nil || actual != expected {
		return fmt.Errorf("resume worktree is not the exact repository root")
	}
	remote, err := run("remote", "get-url", "origin")
	if err != nil || VerifyRepositoryRemote(dispatch, remote) != nil {
		return fmt.Errorf("resume endpoint identity differs from approved configuration")
	}
	var branch string
	if allowDetached {
		branch, err = run("rev-parse", "--abbrev-ref", "HEAD")
	} else {
		branch, err = run("symbolic-ref", "--quiet", "--short", "HEAD")
	}
	if err != nil || branch != instruction.Branch && !(allowDetached && branch == "HEAD") {
		return fmt.Errorf("resume branch differs from the saved contract")
	}
	head, err := run("rev-parse", "HEAD")
	if err != nil || head != dispatch.ExpectedStartSHA() {
		return fmt.Errorf("resume HEAD differs from the saved execution start")
	}
	baseline, err := run("rev-parse", "--verify", instruction.BaselineSHA+"^{commit}")
	if err != nil || baseline != instruction.BaselineSHA {
		return fmt.Errorf("resume baseline is unavailable")
	}
	if _, err := run("merge-base", "--is-ancestor", baseline, head); err != nil {
		return fmt.Errorf("resume baseline is not an ancestor of execution start")
	}
	entries, err := run("ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	var paths []string
	for _, entry := range strings.Split(entries, "\x00") {
		if entry != "" && !strings.HasPrefix(entry, "H ") {
			return fmt.Errorf("resume index flags can hide worktree changes")
		}
		if entry != "" {
			paths = append(paths, entry[2:])
		}
	}
	if err := checkReadOnlyFilters(ctx, instruction.Worktree, paths); err != nil {
		return err
	}
	tree := head
	if instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot && instruction.TargetTreeSHA != "" {
		tree = instruction.TargetTreeSHA
		if !fullSHA(tree) {
			return fmt.Errorf("resume target tree identity is invalid")
		}
	}
	if _, err := run("diff-index", "--cached", "--quiet", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", tree, "--"); err != nil {
		return fmt.Errorf("resume index differs from the approved start tree")
	}
	if _, err := run("diff-files", "--quiet", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", "--"); err != nil {
		return fmt.Errorf("resume worktree differs from its index")
	}
	untracked, err := run("ls-files", "--others", "--exclude-standard")
	if err != nil || untracked != "" {
		return fmt.Errorf("resume worktree has unrecorded files")
	}
	return nil
}

func checkReadOnlyFilters(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	attributes, err := readOnlyGitWithIndex(ctx, root, "", append([]string{"check-attr", "filter", "-z", "--"}, paths...)...)
	if err != nil {
		return err
	}
	fields := strings.Split(attributes, "\x00")
	for i := 1; i+1 < len(fields); i += 3 {
		if fields[i] == "filter" && fields[i+1] != "unset" && fields[i+1] != "unspecified" {
			return fmt.Errorf("tracked Git filters may execute commands during observation")
		}
	}
	return nil
}
