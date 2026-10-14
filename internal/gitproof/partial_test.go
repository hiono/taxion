package gitproof

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestPartialGitEvidenceMovesOnlyAsAnExactCleanSuccessorTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "git@github.com:owner/repo.git")
	baseline := commitFile(t, root, "README.md", "baseline")
	dispatch := core.GraphDispatch{
		ID: "dispatch", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Status: core.DispatchCancelled,
		Instruction: core.DispatchInstruction{
			Branch: "work/task", Worktree: root, BaselineSHA: baseline,
			AuthorizedActions: []string{"docs"},
		},
	}
	partialFile := filepath.Join(root, "docs", "partial.md")
	if err := os.MkdirAll(filepath.Dir(partialFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialFile, []byte("unfinished work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	evidence, err := CapturePartial(dispatch)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.BaselineSHA != baseline || evidence.HeadSHA != baseline || !evidence.UncommittedChanges ||
		evidence.TreeSHA == "" || len(evidence.ChangedFiles) != 1 || evidence.ChangedFiles[0] != "docs/partial.md" {
		t.Fatalf("incomplete partial evidence: %+v", evidence)
	}
	if status := gitCommand(t, root, "status", "--porcelain", "--untracked-files=all"); status != "?? docs/partial.md" {
		t.Fatalf("capture mutated the source index or worktree: %q", status)
	}

	gitCommand(t, root, "add", "-A")
	gitCommand(t, root, "commit", "-m", "docs: checkpoint partial work")
	target := gitCommand(t, root, "rev-parse", "HEAD")
	worktree := filepath.Join(t.TempDir(), "successor")
	gitCommand(t, root, "worktree", "add", "-b", "work/successor", worktree, target)
	task := core.GraphTask{
		Repo: "owner/repo", Worktree: worktree, Branch: "work/successor",
		BaselineSHA: baseline, TargetSHA: target,
		Scope: []string{"docs"},
	}
	dispatch.Result = &core.DispatchResult{Status: core.DispatchCancelled, PartialGitEvidence: evidence}
	if err := VerifyPartialSuccessor(dispatch, task); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "docs", "partial.md"), []byte("changed after approval\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPartialSuccessor(dispatch, task); err == nil {
		t.Fatal("dirty successor worktree was accepted")
	}
}

func TestPartialSnapshotContinuesInAnotherWorktreeWithoutCommit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	baseline := commitFile(t, root, "docs/guide.md", "baseline")
	if err := os.WriteFile(filepath.Join(root, "docs/guide.md"), []byte("partial work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fileAccess := &core.FileAccessPolicy{Level: core.FileAccessAll}
	dispatch := core.GraphDispatch{
		ID: "partial-snapshot-dispatch", TaskID: "task", Recipient: "CODER",
		Repository: "owner/repo", Status: core.DispatchCancelled,
		Instruction: core.DispatchInstruction{
			Version: core.DispatchInstructionVersion, Worktree: root, Branch: "work/task",
			BaselineSHA: baseline, TargetSHA: baseline,
			OutputMode:        core.OutputModeWorktreeTreeSnapshot,
			AuthorizedActions: []string{"docs"}, FileAccess: fileAccess,
		},
	}
	evidence, err := CapturePartial(dispatch)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Result = &core.DispatchResult{Status: core.DispatchCancelled, PartialGitEvidence: evidence}
	successorPath := filepath.Join(t.TempDir(), "successor")
	gitCommand(t, root, "worktree", "add", "-b", "work/successor", successorPath, baseline)
	if err := os.WriteFile(filepath.Join(successorPath, "docs/guide.md"), []byte("partial work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task := core.GraphTask{
		ID: "successor", Repo: dispatch.Repository, Worktree: successorPath, Branch: "work/successor",
		BaselineSHA: baseline, TargetSHA: baseline, TargetTreeSHA: evidence.TreeSHA,
		TargetChangedFiles: evidence.ChangedFiles, OutputMode: core.OutputModeWorktreeTreeSnapshot,
		Scope: []string{"docs"}, FileAccess: fileAccess,
	}
	if err := VerifyPartialSuccessor(dispatch, task); err != nil {
		t.Fatalf("approved successor did not preserve partial snapshot: %v", err)
	}
	successorDispatch := core.GraphDispatch{
		ID: "successor-dispatch", TaskID: task.ID, Repository: task.Repo, Recipient: "CODER",
		Instruction: core.DispatchInstruction{
			Version: core.DispatchInstructionVersion, Worktree: task.Worktree, Branch: task.Branch,
			BaselineSHA: task.BaselineSHA, TargetSHA: task.TargetSHA,
			TargetTreeSHA: task.TargetTreeSHA, TargetChangedFiles: task.TargetChangedFiles,
			OutputMode: task.OutputMode, AuthorizedActions: task.Scope, FileAccess: fileAccess,
		},
	}
	if start, err := ObserveStart(successorDispatch); err != nil || start != baseline {
		t.Fatalf("new Coder could not start from the exact partial tree: %s %v", start, err)
	}
	if count := gitCommand(t, root, "rev-list", "--count", "HEAD"); count != "1" {
		t.Fatalf("snapshot continuation created a commit: %s", count)
	}
}
