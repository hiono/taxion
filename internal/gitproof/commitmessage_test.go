package gitproof

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestCommitPolicyChecksEveryNewCommitAndPreservesBaseline(t *testing.T) {
	root := t.TempDir()
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	baseline := commitFile(t, root, "README.md", "historical baseline")
	commit := func(message, content string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		gitCommand(t, root, "add", "app.go")
		gitCommand(t, root, "commit", "-m", message)
		return gitCommand(t, root, "rev-parse", "HEAD")
	}
	dispatch := core.GraphDispatch{
		ID: "dispatch", TaskID: "task", Repository: "owner/repo", Recipient: "CODER",
		Instruction: core.DispatchInstruction{
			Version: 4, Worktree: root, Branch: "work/task", BaselineSHA: baseline,
			AuthorizedActions: []string{"app.go"}, CommitMessagePolicy: core.CommitMessagePolicy,
		},
	}
	result := &core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient, Repository: dispatch.Repository,
		Status: core.DispatchSucceeded, StartedSHA: baseline, ChangedFiles: []string{"app.go"},
	}
	first := commit("feat(app): implement application\n\nintent(app): satisfy the approved acceptance", "first")
	result.OutputSHA = first
	if err := Verify(dispatch, result); err != nil {
		t.Fatalf("legacy baseline affected new policy: %v", err)
	}
	invalid := commit("fix(app): repair application", "second")
	last := commit("test(app): cover repaired application\n\nintent(app): verify the repaired acceptance", "third")
	result.OutputSHA = last
	if err := Verify(dispatch, result); err == nil || !strings.Contains(err.Error(), invalid) || result.GitEvidence != nil {
		t.Fatalf("checked HEAD only instead of every authored commit: %v", err)
	}
	dispatch.Instruction.ValidationCommands = []string{"touch validation-ran"}
	if err := ExecuteValidations(context.Background(), dispatch, result); err == nil {
		t.Fatal("invalid commit was accepted by controller validation")
	}
	if _, err := os.Stat(filepath.Join(root, "validation-ran")); !os.IsNotExist(err) {
		t.Fatal("validation executed before commit policy passed", err)
	}
	// Saved Dispatches without the policy retain their original contract.
	legacy := dispatch
	legacy.Instruction.CommitMessagePolicy = ""
	if err := Verify(legacy, result); err != nil {
		t.Fatal("legacy Dispatch changed retroactively", err)
	}
	// A repair checks commits after its saved execution-start SHA.
	dispatch.Instruction.TargetSHA = invalid
	result.StartedSHA = invalid
	if err := Verify(dispatch, result); err != nil {
		t.Fatalf("repair policy rechecked earlier work instead of authored commits: %v", err)
	}
	dispatch.Instruction.CommitMessagePolicy = "unknown-policy"
	if err := Verify(dispatch, result); err == nil {
		t.Fatal("unknown commit policy accepted")
	}
}
