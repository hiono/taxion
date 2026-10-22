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
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{
			"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
			"-c", "core.untrackedCache=false", "-C", dispatch.Instruction.Worktree,
		}, args...)...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GIT_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
		output := &limitedOutput{remaining: 64 * 1024}
		cmd.Stdout = output
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("resume Git observation failed")
		}
		return strings.TrimSpace(output.String()), nil
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
	branch, err := run("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != instruction.Branch {
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
	tree := head
	if instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot && instruction.TargetTreeSHA != "" {
		tree = instruction.TargetTreeSHA
		if !fullSHA(tree) {
			return fmt.Errorf("resume target tree identity is invalid")
		}
	}
	if _, err := run("diff-index", "--quiet", "--no-ext-diff", "--no-textconv", "--ignore-submodules=none", tree, "--"); err != nil {
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
