package gitproof

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// CommitTree resolves an independently pinned full commit, never a shell expression.
func CommitTree(worktree, commit string) (string, error) {
	if !fullSHA(commit) {
		return "", fmt.Errorf("packet target requires a full commit SHA")
	}
	return git(worktree, "rev-parse", "--verify", commit+"^{tree}")
}

func Verify(dispatch core.GraphDispatch, result *core.DispatchResult) error {
	if result == nil {
		return fmt.Errorf("Git proof requires a dispatch result")
	}
	result.GitEvidence = nil
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		return verifySnapshot(dispatch, result)
	}
	if result.DispatchID != dispatch.ID || result.TaskID != dispatch.TaskID ||
		result.Recipient != dispatch.Recipient || result.Repository != dispatch.Repository ||
		result.Status != core.DispatchSucceeded || result.StartedSHA != dispatch.ExpectedStartSHA() {
		return fmt.Errorf("Git verification result does not match the dispatch identity and baseline")
	}
	if strings.TrimSpace(dispatch.Instruction.Worktree) == "" {
		return fmt.Errorf("Git verification requires a worktree")
	}
	if !fullSHA(dispatch.Instruction.BaselineSHA) || !fullSHA(result.OutputSHA) ||
		(dispatch.Instruction.TargetSHA != "" && !fullSHA(dispatch.Instruction.TargetSHA)) {
		return fmt.Errorf("Git verification requires full commit SHAs")
	}
	root, err := git(dispatch.Instruction.Worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root, err = canonical(root)
	if err != nil {
		return fmt.Errorf("resolve Git worktree: %w", err)
	}
	requestedRoot, err := canonical(dispatch.Instruction.Worktree)
	if err != nil || requestedRoot != root {
		return fmt.Errorf("dispatch worktree must identify the Git root %s", root)
	}
	remote, err := git(root, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("read origin remote: %w", err)
	}
	if err := VerifyRepositoryRemote(dispatch, remote); err != nil {
		return fmt.Errorf("Git origin %q does not match repository %q", repositoryIdentity(remote), dispatch.Repository)
	}

	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	baseline, err := git(root, "rev-parse", dispatch.Instruction.BaselineSHA+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve dispatch baseline: %w", err)
	}
	if baseline != dispatch.Instruction.BaselineSHA {
		return fmt.Errorf("dispatch baseline %s is not a full commit SHA", dispatch.Instruction.BaselineSHA)
	}
	if dispatch.Recipient == "REVIEWER" && dispatch.Instruction.TargetSHA != "" && head != dispatch.Instruction.TargetSHA {
		return fmt.Errorf("Git HEAD %s does not match target SHA %s", head, dispatch.Instruction.TargetSHA)
	}
	if dispatch.Recipient == "REVIEWER" {
		if head != baseline || result.OutputSHA != baseline {
			return fmt.Errorf("Reviewer must inspect the dispatch baseline SHA %s", baseline)
		}
	} else if _, err := git(root, "merge-base", "--is-ancestor", baseline, head); err != nil {
		return fmt.Errorf("Git HEAD %s does not descend from baseline %s: %w", head, baseline, err)
	}
	branch, err := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		var exit *exec.ExitError
		if dispatch.Recipient != "REVIEWER" || !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("read worktree branch: %w", err)
		}
		branch = "(detached)"
	}
	if branch != "(detached)" && branch != dispatch.Instruction.Branch {
		return fmt.Errorf("Git branch %q does not match dispatch branch %q", branch, dispatch.Instruction.Branch)
	}
	if head != result.OutputSHA {
		return fmt.Errorf("Git HEAD %s does not match reported output SHA %s", head, result.OutputSHA)
	}
	if dispatch.Recipient == "CODER" && dispatch.Instruction.CommitMessagePolicy != "" {
		if dispatch.Instruction.CommitMessagePolicy != core.CommitMessagePolicy {
			return fmt.Errorf("unsupported commit message policy %q", dispatch.Instruction.CommitMessagePolicy)
		}
		start := dispatch.ExpectedStartSHA()
		if _, err := git(root, "merge-base", "--is-ancestor", start, head); err != nil {
			return fmt.Errorf("output must descend from execution start SHA %s: %w", start, err)
		}
		commits, err := git(root, "rev-list", start+".."+head)
		if err != nil {
			return fmt.Errorf("read authored commits: %w", err)
		}
		for _, sha := range strings.Fields(commits) {
			message, err := git(root, "show", "-s", "--format=%B", sha)
			if err != nil {
				return fmt.Errorf("read commit %s message: %w", sha, err)
			}
			if err := core.ValidateCommitMessage(message); err != nil {
				return fmt.Errorf("commit %s violates %s: %w", sha, core.CommitMessagePolicy, err)
			}
		}
	}
	files, err := changedFiles(root, baseline, head)
	if err != nil {
		return err
	}
	if err := verifyChangeManifest(dispatch, result, files); err != nil {
		return err
	}
	if err := CheckChangedPaths(dispatch, files); err != nil {
		return err
	}
	status, err := git(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("Git worktree is dirty")
	}
	for i, validation := range result.Validations {
		if validation.Worktree == "" || validation.SHA != head {
			return fmt.Errorf("validation %q has incomplete or mismatched worktree provenance", validation.Command)
		}
		validationRoot, err := git(validation.Worktree, "rev-parse", "--show-toplevel")
		if err != nil {
			return fmt.Errorf("resolve validation worktree: %w", err)
		}
		validationRoot, err = canonical(validationRoot)
		if err != nil {
			return fmt.Errorf("resolve validation worktree: %w", err)
		}
		validationSHA, err := git(validationRoot, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("read validation SHA: %w", err)
		}
		if validationRoot != root || validationSHA != head {
			return fmt.Errorf("validation %q provenance does not match worktree %s at SHA %s", validation.Command, root, head)
		}
		result.Validations[i].Worktree = root
	}
	parentLine, err := git(root, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return err
	}
	parentFields := strings.Fields(parentLine)
	if len(parentFields) == 0 || parentFields[0] != head {
		return fmt.Errorf("read parents for Git HEAD %s", head)
	}
	evidence := &core.DispatchGitEvidence{
		Worktree: root, Repository: dispatch.Repository, Branch: branch,
		BaselineSHA: baseline, HeadSHA: head, ParentSHAs: slices.Clone(parentFields[1:]),
		ChangedFiles: files, WorktreeClean: true, VerifiedAt: time.Now().UTC(),
	}
	result.GitEvidence = evidence
	return nil
}

func ObserveStart(dispatch core.GraphDispatch) (string, error) {
	start := dispatch.ExpectedStartSHA()
	if !fullSHA(start) {
		return "", fmt.Errorf("execution start requires a full SHA")
	}

	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		return ObserveSnapshotStart(dispatch)
	}
	files, err := changedFiles(dispatch.Instruction.Worktree, dispatch.Instruction.BaselineSHA, start)
	if err != nil {
		return "", err
	}
	check := &core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
		Repository: dispatch.Repository, Status: core.DispatchSucceeded,
		StartedSHA: start, OutputSHA: start, ChangedFiles: files,
	}
	if err := Verify(dispatch, check); err != nil {
		return "", fmt.Errorf("execution start Git verification: %w", err)
	}
	return check.GitEvidence.HeadSHA, nil
}

// ObserveRequestBaseline reads a clean, attached worktree using the same
// repository and Git checks as execution, without creating a Dispatch.
func ObserveRequestBaseline(worktree, repository string, selection []byte) (root, branch, sha string, err error) {
	root, err = git(worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	root, err = canonical(root)
	if err != nil {
		return
	}
	sha, err = git(root, "rev-parse", "HEAD")
	if err != nil {
		return
	}
	branch, err = git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		err = fmt.Errorf("request requires an attached worktree branch: %w", err)
		return
	}
	dispatch := core.GraphDispatch{
		Repository: repository, Recipient: "CODER",
		Configuration: &core.DispatchConfiguration{Selection: selection},
		Instruction:   core.DispatchInstruction{Worktree: root, Branch: branch, BaselineSHA: sha},
	}
	err = Verify(dispatch, &core.DispatchResult{
		Repository: repository, Recipient: "CODER", Status: core.DispatchSucceeded,
		StartedSHA: sha, OutputSHA: sha,
	})
	return
}

// ExecuteValidations runs only the commands authorized by the immutable
// Dispatch, then verifies that execution preserved the reported Git state.
func ExecuteValidations(ctx context.Context, dispatch core.GraphDispatch, result *core.DispatchResult) error {
	if result == nil {
		return fmt.Errorf("controller validation requires a result")
	}
	if result.ChangeManifest != nil {
		return fmt.Errorf("change manifest is generated by the controller")
	}
	for index := range result.Validations {
		result.Validations[index].StartedAt = time.Time{}
		result.Validations[index].FinishedAt = time.Time{}
		result.Validations[index].Output = ""
	}
	if len(dispatch.Instruction.ValidationCommands) == 0 {
		return fmt.Errorf("controller validation requires authorized commands")
	}
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot &&
		dispatch.Recipient == "CODER" {
		if err := CaptureSnapshot(dispatch, result); err != nil {
			return err
		}
	}
	if err := Verify(dispatch, result); err != nil {
		return err
	}
	root := result.GitEvidence.Worktree
	result.GitEvidence = nil
	var err error
	result.Validations, err = executeValidationCommands(ctx, root, validationSHA(dispatch, result),
		dispatch.Instruction.ValidationCommands, dispatch.Recipient == "REVIEWER" && result.ReviewVerdict != "APPROVE")
	if err != nil {
		return err
	}
	return Verify(dispatch, result)
}

func executeValidationCommands(ctx context.Context, root, sha string, commands []string, allowFailure bool) ([]core.DispatchValidation, error) {
	checks := []core.DispatchValidation{}
	for _, authorized := range commands {
		if err := ctx.Err(); err != nil {
			return checks, err
		}
		started := time.Now().UTC()
		command := exec.CommandContext(ctx, "sh", "-c", authorized)
		command.Dir = root
		if err := configureValidationProcess(command); err != nil {
			return checks, err
		}
		command.WaitDelay = 5 * time.Second
		output, err := command.CombinedOutput()
		validation := core.DispatchValidation{
			Command: authorized, Worktree: root, SHA: sha,
			Output: string(output), StartedAt: started, FinishedAt: time.Now().UTC(),
		}
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				validation.ExitCode = -1
				checks = append(checks, validation)
				return checks, fmt.Errorf("execute validation %q: %w", authorized, err)
			}
			validation.ExitCode = exit.ExitCode()
		}
		checks = append(checks, validation)
		if ctx.Err() != nil {
			return checks, fmt.Errorf("validation %q: %w", authorized, ctx.Err())
		}
		if validation.ExitCode != 0 && !allowFailure {
			return checks, fmt.Errorf("controller validation %q failed with exit code %d: %s", authorized, validation.ExitCode, output)
		}
	}
	return checks, nil
}

func validationSHA(dispatch core.GraphDispatch, result *core.DispatchResult) string {
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		return result.OutputTreeSHA
	}
	return result.OutputSHA
}

func fullSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func git(directory string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(output)), nil
}

func canonical(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func repositoryIdentity(remote string) string {
	value := remote
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		value = parsed.Path
	} else if colon := strings.IndexByte(remote, ':'); colon >= 0 {
		value = remote[colon+1:]
	}
	value = strings.TrimSuffix(strings.Trim(strings.TrimSpace(value), "/"), ".git")
	parts := strings.Split(value, "/")
	if len(parts) < 2 {
		return ""
	}
	return value
}

// RepositoryIdentity preserves the complete namespace used by Git proof.
func RepositoryIdentity(remote string) string {
	return repositoryIdentity(remote)
}

func changedFiles(directory, baseline, head string) ([]string, error) {
	command := exec.Command("git", "-C", directory, "diff", "--no-renames", "--name-only", "-z", baseline, head)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read Git changed files: %w", err)
	}
	if len(output) == 0 {
		return []string{}, nil
	}
	files := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	for i := range files {
		files[i] = filepath.ToSlash(files[i])
	}
	return files, nil
}

func sameFiles(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	return slices.Equal(slices.Sorted(slices.Values(left)), slices.Sorted(slices.Values(right)))
}

func withinAny(path string, scopes []string) bool {
	for _, scope := range scopes {
		scope = filepath.ToSlash(filepath.Clean(scope))
		if matched, _ := filepath.Match(scope, path); matched {
			return true
		}
		if !strings.ContainsAny(scope, "*?[") &&
			(path == scope || strings.HasPrefix(path, strings.TrimSuffix(scope, "/")+"/")) {
			return true
		}
	}
	return false
}

// CheckChangedPaths applies the same Dispatch scope to pre-commit trees and
// final Git evidence. Callers obtain the paths from Git, not agent reports.
func CheckChangedPaths(dispatch core.GraphDispatch, files []string) error {
	var committed map[string]struct{}
	if policy := dispatch.Instruction.FileAccess; policy != nil && policy.Level == core.FileAccessCommitted && len(files) > 0 {
		paths, err := TrackedPathsAt(dispatch.Instruction.Worktree, dispatch.ExpectedStartSHA())
		if err != nil {
			return fmt.Errorf("check committed-file access: %w", err)
		}
		committed = make(map[string]struct{}, len(paths))
		for _, path := range paths {
			committed[path] = struct{}{}
		}
	}
	for _, file := range files {
		if dispatch.Recipient == "REVIEWER" || !withinAny(file, dispatch.Instruction.AuthorizedActions) ||
			withinAny(file, dispatch.Instruction.ForbiddenActions) {
			return fmt.Errorf("Git change %q is outside the authorized scope", file)
		}
		if policy := dispatch.Instruction.FileAccess; policy != nil {
			switch policy.Level {
			case core.FileAccessExact:
				if !slices.Contains(policy.Paths, filepath.ToSlash(file)) {
					return fmt.Errorf("Git change %q is outside the exact file-access paths", file)
				}
			case core.FileAccessCommitted:
				if _, ok := committed[filepath.ToSlash(file)]; !ok {
					return fmt.Errorf("Git change %q was not tracked at the Dispatch start SHA", file)
				}
			case core.FileAccessAll:
			default:
				return fmt.Errorf("unknown file access level %q", policy.Level)
			}
		}
	}
	return nil
}
