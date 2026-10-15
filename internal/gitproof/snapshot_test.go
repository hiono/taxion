package gitproof

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestWorktreeTreeSnapshotIsReviewedWithoutCommit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	baseline := commitFile(t, root, "docs/guide.md", "baseline")
	indexBefore := gitCommand(t, root, "ls-files", "--stage")
	branchesBefore := gitCommand(t, root, "for-each-ref", "--format=%(refname):%(objectname)", "refs/heads")
	if err := os.WriteFile(filepath.Join(root, "docs/guide.md"), []byte("review this snapshot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	coder := core.GraphDispatch{
		ID: "coder-dispatch", TaskID: "task", Repository: "owner/repo", Recipient: "CODER",
		Relationship: core.RelationshipNone,
		Instruction: core.DispatchInstruction{
			Version: core.DispatchInstructionVersion, Worktree: root, Branch: "work/task",
			BaselineSHA: baseline, OutputMode: core.OutputModeWorktreeTreeSnapshot,
			AuthorizedActions: []string{"docs"}, FileAccess: core.DefaultFileAccessPolicy(),
			ValidationCommands: []string{"grep -q 'review this snapshot' docs/guide.md"},
		},
	}
	coderResult := &core.DispatchResult{
		DispatchID: coder.ID, TaskID: coder.TaskID, Recipient: coder.Recipient,
		Repository: coder.Repository, Status: core.DispatchSucceeded,
		StartedSHA: baseline, OutputSHA: baseline, ChangedFiles: []string{"docs/guide.md"},
		Evidence: []string{"coder-report"}, NextTransition: core.WaitingForReview,
	}
	if err := ExecuteValidations(context.Background(), coder, coderResult); err != nil {
		t.Fatal(err)
	}
	if coderResult.OutputTreeSHA == "" || coderResult.OutputTreeSHA == baseline ||
		coderResult.GitEvidence == nil || coderResult.GitEvidence.WorktreeClean {
		t.Fatalf("snapshot result did not record the uncommitted tree: %+v", coderResult)
	}
	if err := coder.ValidateResult(core.DispatchSucceeded, coderResult); err != nil {
		t.Fatalf("verified Coder snapshot rejected: %v", err)
	}
	if head := gitCommand(t, root, "rev-parse", "HEAD"); head != baseline {
		t.Fatalf("Coder changed HEAD: %s", head)
	}
	if count := gitCommand(t, root, "rev-list", "--count", "HEAD"); count != "1" {
		t.Fatalf("Coder created a commit: count=%s", count)
	}
	if index := gitCommand(t, root, "ls-files", "--stage"); index != indexBefore {
		t.Fatal("snapshot capture modified the real Git index")
	}
	if branches := gitCommand(t, root, "for-each-ref", "--format=%(refname):%(objectname)", "refs/heads"); branches != branchesBefore {
		t.Fatal("snapshot capture modified a branch ref")
	}
	if err := verifySnapshotRef(root, coder.ID, coderResult.OutputTreeSHA); err != nil {
		t.Fatal(err)
	}
	coder.Status, coder.Result = core.DispatchSucceeded, coderResult
	diff, err := ReviewDiff(root, baseline, coderResult.OutputTreeSHA)
	if err != nil || !strings.Contains(diff, "+review this snapshot") || !strings.Contains(diff, "-baseline") {
		t.Fatalf("immutable review diff missing actual change: %q %v", diff, err)
	}
	if _, err := ReviewDiff(root, "--output=unexpected", coderResult.OutputTreeSHA); err == nil {
		t.Fatal("review diff accepted an option instead of a full object SHA")
	}
	successorPath := filepath.Join(t.TempDir(), "successor")
	gitCommand(t, root, "worktree", "add", "-b", "work/successor", successorPath, baseline)
	if err := os.WriteFile(filepath.Join(successorPath, "docs/guide.md"), []byte("review this snapshot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	successor := core.GraphTask{
		ID: "successor", Repo: coder.Repository, OutputMode: core.OutputModeWorktreeTreeSnapshot,
		Worktree: successorPath, Branch: "work/successor", BaselineSHA: baseline, TargetSHA: baseline,
		TargetTreeSHA: coderResult.OutputTreeSHA, TargetChangedFiles: coderResult.ChangedFiles,
		Scope: []string{"docs"}, FileAccess: core.DefaultFileAccessPolicy(),
	}
	successorDispatch := core.GraphDispatch{
		ID: "successor-coder", TaskID: successor.ID, Repository: successor.Repo, Recipient: "CODER",
		Instruction: core.DispatchInstruction{
			Version: core.DispatchInstructionVersion, Worktree: successor.Worktree, Branch: successor.Branch,
			BaselineSHA: successor.BaselineSHA, TargetSHA: successor.TargetSHA,
			OutputMode: successor.OutputMode, TargetTreeSHA: successor.TargetTreeSHA,
			TargetChangedFiles: successor.TargetChangedFiles, AuthorizedActions: successor.Scope,
			FileAccess: successor.FileAccess,
		},
	}
	if start, err := ObserveStart(successorDispatch); err != nil || start != baseline {
		t.Fatalf("snapshot successor Coder could not start from the retained tree: %s %v", start, err)
	}
	if err := VerifyCompletedSuccessor(coder, successor); err != nil {
		t.Fatalf("saved snapshot could not be continued from a separate worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(successorPath, "docs/guide.md"), []byte("different tree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCompletedSuccessor(coder, successor); err == nil {
		t.Fatal("successor accepted a tree different from the saved Coder result")
	}
	if _, err := ObserveStart(successorDispatch); err == nil {
		t.Fatal("successor Coder started from a tree different from its approved target")
	}

	reviewRoot := filepath.Join(t.TempDir(), "reviews")
	if err := os.MkdirAll(reviewRoot, 0755); err != nil {
		t.Fatal(err)
	}
	reviewPath := filepath.Join(reviewRoot, "review-task")
	reviewer := core.GraphDispatch{
		ID: "review-dispatch", TaskID: "review-task", Repository: "owner/repo",
		Recipient: "REVIEWER", Relationship: core.RelationshipIndependent,
		RelatedID: coder.ID, ReviewWorktreeSource: root,
		Instruction: core.DispatchInstruction{
			Version: core.DispatchInstructionVersion, Worktree: reviewPath, Branch: "work/task",
			BaselineSHA: baseline, TargetSHA: baseline,
			OutputMode:         core.OutputModeWorktreeTreeSnapshot,
			TargetTreeSHA:      coderResult.OutputTreeSHA,
			TargetChangedFiles: coderResult.ChangedFiles,
			AuthorizedActions:  []string{"docs"}, FileAccess: core.DefaultFileAccessPolicy(),
			Acceptance:         []string{"reviewed snapshot"},
			ValidationCommands: []string{"grep -q 'review this snapshot' docs/guide.md"},
		},
	}
	if err := CreateReviewWorktree(root, reviewer); err != nil {
		t.Fatal(err)
	}
	if start, err := ObserveStart(reviewer); err != nil || start != baseline {
		t.Fatalf("Reviewer start = %s, %v", start, err)
	}
	if err := os.WriteFile(filepath.Join(reviewPath, "docs/guide.md"), []byte("changed by Reviewer\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveStart(reviewer); err == nil {
		t.Fatal("Reviewer started with a modified snapshot")
	}
	if err := os.WriteFile(filepath.Join(reviewPath, "docs/guide.md"), []byte("review this snapshot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worktreeChanged := false
	reviewResult := &core.DispatchResult{
		DispatchID: reviewer.ID, TaskID: reviewer.TaskID, Recipient: reviewer.Recipient,
		Repository: reviewer.Repository, Status: core.DispatchSucceeded,
		StartedSHA: baseline, OutputSHA: baseline, OutputTreeSHA: reviewer.Instruction.TargetTreeSHA,
		ChangedFiles: []string{}, Evidence: []string{"review-report"},
		ReviewVerdict: "APPROVE",
		Acceptance: []core.DispatchAcceptance{{
			Criterion: "reviewed snapshot", Status: "PASS", Evidence: []string{"review-report"},
		}},
		Findings: []core.DispatchFinding{}, WorktreeChanged: &worktreeChanged,
		NextTransition: core.Approved,
	}
	if err := ExecuteValidations(context.Background(), reviewer, reviewResult); err != nil {
		t.Fatal(err)
	}
	if err := reviewer.ValidateResult(core.DispatchSucceeded, reviewResult); err != nil {
		t.Fatalf("verified Reviewer snapshot rejected: %v", err)
	}
	if err := ReleaseReviewWorktree(root, reviewer, reviewResult); err != nil {
		t.Fatal(err)
	}
	reviewer.Status = core.DispatchSucceeded
	if err := ReverifyReleasedReview(reviewer, reviewResult); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "gc", "--prune=now", "--quiet")
	if err := verifySnapshotRef(root, coder.ID, coderResult.OutputTreeSHA); err != nil {
		t.Fatalf("retained snapshot did not survive Git garbage collection: %v", err)
	}
	if head := gitCommand(t, root, "rev-parse", "HEAD"); head != baseline {
		t.Fatalf("review changed HEAD: %s", head)
	}
}
