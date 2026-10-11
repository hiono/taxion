package gitproof

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func TestVerifyBindsChangesToRepositoryWorktreeAndSHA(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "git@github.com:owner/repo.git")
	baseline := commitFile(t, root, "README.md", "baseline")
	outputSHA := commitFile(t, root, "docs/guide.md", "update")
	dispatch := core.GraphDispatch{
		ID: "dispatch", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Instruction: core.DispatchInstruction{
			Branch: "work/task", Worktree: root, BaselineSHA: baseline,
			AuthorizedActions: []string{"docs"},
		},
	}
	result := &core.DispatchResult{
		DispatchID: "dispatch", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Status: core.DispatchSucceeded, StartedSHA: baseline, OutputSHA: outputSHA,
		ChangedFiles: []string{"docs/guide.md"},
		Validations: []core.DispatchValidation{{
			Command: "go test ./...", ExitCode: 0, Worktree: root, SHA: outputSHA,
		}},
		Evidence: []string{"test-report"}, NextTransition: core.WaitingForEvidence,
	}
	if err := Verify(dispatch, result); err != nil {
		t.Fatal(err)
	}
	if result.GitEvidence == nil || result.GitEvidence.BaselineSHA != baseline ||
		result.GitEvidence.HeadSHA != outputSHA || len(result.GitEvidence.ParentSHAs) != 1 ||
		result.GitEvidence.ParentSHAs[0] != baseline || result.GitEvidence.Worktree != root {
		t.Fatalf("Git evidence did not capture repository facts: %+v", result.GitEvidence)
	}
	if err := dispatch.ValidateResult(core.DispatchSucceeded, result); err != nil {
		t.Fatalf("verified result rejected by dispatch contract: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*core.GraphDispatch, *core.DispatchResult)
	}{
		{"repository", func(d *core.GraphDispatch, r *core.DispatchResult) {
			d.Repository = "owner/other"
			r.Repository = d.Repository
		}},
		{"output SHA", func(_ *core.GraphDispatch, r *core.DispatchResult) { r.OutputSHA = baseline }},
		{"invalid SHA", func(_ *core.GraphDispatch, r *core.DispatchResult) { r.OutputSHA = "not-a-sha" }},
		{"start SHA", func(_ *core.GraphDispatch, r *core.DispatchResult) { r.StartedSHA = outputSHA }},
		{"branch", func(d *core.GraphDispatch, _ *core.DispatchResult) { d.Instruction.Branch = "other" }},
		{"changed files", func(_ *core.GraphDispatch, r *core.DispatchResult) { r.ChangedFiles = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changedDispatch, changedResult := dispatch, *result
			test.mutate(&changedDispatch, &changedResult)
			if err := Verify(changedDispatch, &changedResult); err == nil || changedResult.GitEvidence != nil {
				t.Fatalf("accepted mismatched %s", test.name)
			}
		})
	}

	result.Validations[0].SHA = baseline
	if err := Verify(dispatch, result); err == nil || result.GitEvidence != nil {
		t.Fatal("accepted validation run at another SHA")
	}
	result.Validations[0].SHA = outputSHA

	otherSHA := commitFile(t, root, "src/unscoped.go", "outside scope")
	result.OutputSHA = otherSHA
	result.ChangedFiles = []string{"docs/guide.md", "src/unscoped.go"}
	result.Validations[0].SHA = otherSHA
	if err := Verify(dispatch, result); err == nil {
		t.Fatal("accepted a change outside the authorized scope")
	}
}

func TestVerifyRequiresReviewerToLeaveTargetClean(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "init", "-b", "review/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	baseline := commitFile(t, root, "README.md", "review target")
	dispatch := core.GraphDispatch{
		ID: "review", TaskID: "task", Recipient: "REVIEWER", Repository: "owner/repo",
		Instruction: core.DispatchInstruction{Branch: "review/task", Worktree: root, BaselineSHA: baseline},
	}
	worktreeChanged := false
	result := &core.DispatchResult{
		DispatchID: "review", TaskID: "task", Recipient: "REVIEWER", Repository: "owner/repo",
		Status: core.DispatchSucceeded, StartedSHA: baseline, OutputSHA: baseline,
		ChangedFiles: []string{},
		Validations: []core.DispatchValidation{{
			Command: "review target", ExitCode: 0, Worktree: root, SHA: baseline,
		}},
		Evidence: []string{"review-report"}, ReviewVerdict: "APPROVE",
		Acceptance: []core.DispatchAcceptance{{Criterion: "reviewed", Status: "PASS", Evidence: []string{"review-report"}}},
		Findings:   []core.DispatchFinding{}, WorktreeChanged: &worktreeChanged,
		NextTransition: core.WaitingForEvidence,
	}
	dispatch.Instruction.Acceptance = []string{"reviewed"}
	if err := Verify(dispatch, result); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.ValidateResult(core.DispatchSucceeded, result); err != nil {
		t.Fatalf("clean Reviewer result rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dispatch, result); err == nil {
		t.Fatal("accepted a dirty Reviewer worktree")
	}
}

func TestControllerExecutesOnlyAuthorizedValidation(t *testing.T) {
	root := t.TempDir()
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	baseline := commitFile(t, root, "README.md", "baseline")
	parent := commitFile(t, root, "docs/guide.md", "first correction")
	dispatch := core.GraphDispatch{
		ID: "coder", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Instruction: core.DispatchInstruction{
			Version: core.DispatchInstructionVersion, Worktree: root, Branch: "work/task",
			BaselineSHA: baseline, TargetSHA: parent, AuthorizedActions: []string{"docs"},
			ValidationCommands: []string{"printf 'controller observed'; test -f docs/guide.md"},
		},
	}
	start, err := ObserveStart(dispatch)
	if err != nil || start != parent {
		t.Fatalf("observed correction start = %s, %v", start, err)
	}
	dispatch.ExecutionStartSHA = start
	head := commitFile(t, root, "docs/guide.md", "second correction")
	if _, err := ObserveStart(dispatch); err == nil {
		t.Fatal("accepted starting execution at another HEAD")
	}
	newResult := func() *core.DispatchResult {
		return &core.DispatchResult{
			DispatchID: "coder", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
			Status: core.DispatchSucceeded, StartedSHA: parent, OutputSHA: head,
			ChangedFiles: []string{"docs/guide.md"}, Evidence: []string{"report"},
			NextTransition: core.WaitingForEvidence,
			Validations: []core.DispatchValidation{{
				Command: "touch unapproved", Worktree: root, SHA: head,
			}},
		}
	}
	result := newResult()
	if err := ExecuteValidations(context.Background(), dispatch, result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "unapproved")); !os.IsNotExist(err) {
		t.Fatal("agent-supplied validation command executed")
	}
	validation := result.Validations[0]
	if validation.Command != dispatch.Instruction.ValidationCommands[0] ||
		validation.Output != "controller observed" || validation.StartedAt.IsZero() ||
		validation.FinishedAt.Before(validation.StartedAt) {
		t.Fatalf("missing observed execution: %+v", validation)
	}
	if result.GitEvidence.ParentSHAs[0] != parent || result.GitEvidence.BaselineSHA != baseline {
		t.Fatal("approved baseline was conflated with the actual commit parent")
	}
	if err := dispatch.ValidateResult(core.DispatchSucceeded, result); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		command string
		timeout time.Duration
	}{
		{"failed exit", "exit 7", time.Second},
		{"dirty worktree", "touch generated.txt", time.Second},
		{"timeout", "sleep 30", 50 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			dispatch.Instruction.ValidationCommands = []string{test.command}
			ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
			defer cancel()
			result := newResult()
			if err := ExecuteValidations(ctx, dispatch, result); err == nil || result.GitEvidence != nil {
				t.Fatalf("accepted %s as verified success: %+v", test.name, result)
			}
			if test.name == "dirty worktree" {
				if err := os.Remove(filepath.Join(root, "generated.txt")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestVerifyRejectsRepositoryScopeAndValidationMismatch(t *testing.T) {
	root := t.TempDir()
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	baseline := commitFile(t, root, "src/original.go", "unchanged")
	if err := os.Mkdir(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "mv", "src/original.go", "docs/moved.go")
	gitCommand(t, root, "commit", "-m", "move file")
	head := gitCommand(t, root, "rev-parse", "HEAD")
	dispatch := core.GraphDispatch{
		ID: "coder", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Instruction: core.DispatchInstruction{Worktree: root, Branch: "work/task", BaselineSHA: baseline,
			AuthorizedActions: []string{"docs"}},
	}
	result := &core.DispatchResult{
		DispatchID: "coder", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Status: core.DispatchSucceeded, StartedSHA: baseline, OutputSHA: head,
		ChangedFiles: []string{"docs/moved.go", "src/original.go"},
		Validations:  []core.DispatchValidation{{Command: "test", Worktree: root, SHA: head}},
	}
	if err := Verify(dispatch, result); err == nil {
		t.Fatal("rename concealed deletion outside scope")
	}
	dispatch.Instruction.AuthorizedActions = []string{"docs", "src"}
	if err := Verify(dispatch, result); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	gitCommand(t, root, "worktree", "add", "--detach", other, head)
	result.Validations[0].Worktree = other
	if err := Verify(dispatch, result); err == nil {
		t.Fatal("accepted validation from another worktree at the same SHA")
	}
	result.Validations[0].Worktree = root
	gitCommand(t, root, "remote", "set-url", "origin", "https://github.com/owner/other.git")
	if err := Verify(dispatch, result); err == nil {
		t.Fatal("accepted another repository")
	}
}

func commitFile(t *testing.T, root, name, content string) string {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "add", "--", name)
	gitCommand(t, root, "commit", "-m", "test commit")
	return gitCommand(t, root, "rev-parse", "HEAD")
}

func gitCommand(t *testing.T, root string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
