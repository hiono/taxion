package gitproof

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestChangeManifestUsesWholeCandidateAndPreservesClaims(t *testing.T) {
	for _, mode := range []string{core.OutputModeCommit, core.OutputModeWorktreeTreeSnapshot} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			gitCommand(t, root, "init", "-b", "work/task")
			gitCommand(t, root, "config", "user.name", "Taxion Test")
			gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
			gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
			baseline := commitFile(t, root, "README.md", "baseline")
			carried := commitFile(t, root, "docs/carried.md", "carried input")
			candidate := carried
			if mode == core.OutputModeCommit {
				candidate = commitFile(t, root, "docs/current.md", "new work")
			} else if err := os.WriteFile(filepath.Join(root, "docs/current.md"), []byte("new work"), 0600); err != nil {
				t.Fatal(err)
			}
			dispatch := core.GraphDispatch{
				ID: "coder", TaskID: "task", Recipient: "CODER", Repository: "owner/repo", ExecutionStartSHA: carried,
				Instruction: core.DispatchInstruction{
					Version: core.DispatchInstructionVersion, Worktree: root, Branch: "work/task",
					BaselineSHA: baseline, TargetSHA: carried, OutputMode: mode,
					AuthorizedActions: []string{"docs"}, ValidationCommands: []string{"test -f docs/carried.md && test -f docs/current.md"},
					ChangeManifestPolicy: core.ChangeManifestPolicy,
				},
			}
			expected := []string{"docs/carried.md", "docs/current.md"}
			newResult := func(claim []string) *core.DispatchResult {
				return &core.DispatchResult{
					DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient, Repository: dispatch.Repository,
					Status: core.DispatchSucceeded, StartedSHA: carried, OutputSHA: candidate, ChangedFiles: slices.Clone(claim),
					Evidence: []string{"work-report"}, NextTransition: core.WaitingForEvidence,
				}
			}
			for _, claim := range [][]string{nil, {"docs/current.md"}, {"docs/current.md", "docs/current.md", "../not-an-access-grant"}} {
				result := newResult(claim)
				if err := ExecuteValidations(context.Background(), dispatch, result); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(result.ChangedFiles, expected) || result.ChangeManifest == nil ||
					!slices.Equal(result.ChangeManifest.ReportedChangedFiles, claim) ||
					!slices.Equal(result.GitEvidence.ChangedFiles, expected) || len(result.Validations) != 1 {
					t.Fatalf("lost original claim or full candidate: %+v", result)
				}
				if err := dispatch.ValidateResult(core.DispatchSucceeded, result); err != nil {
					t.Fatal(err)
				}
				if err := Verify(dispatch, result); err != nil {
					t.Fatal("saved result cannot be reverified", err)
				}
				result.ChangedFiles = []string{"docs/current.md"}
				if err := Verify(dispatch, result); err == nil {
					t.Fatal("accepted tampered canonical list")
				}
			}
			legacy := dispatch
			legacy.Instruction.ChangeManifestPolicy = ""
			if err := ExecuteValidations(context.Background(), legacy, newResult(nil)); err == nil {
				t.Fatal("silently upgraded a legacy instruction")
			}
			denied := dispatch
			denied.Instruction.AuthorizedActions = []string{"docs/current.md"}
			if err := ExecuteValidations(context.Background(), denied, newResult([]string{"docs/current.md"})); err == nil {
				t.Fatal("hid out-of-scope carried changes")
			}
			forged := newResult(nil)
			forged.ChangeManifest = &core.DispatchChangeManifest{Policy: core.ChangeManifestPolicy}
			if err := ExecuteValidations(context.Background(), dispatch, forged); err == nil {
				t.Fatal("accepted submitted controller evidence")
			}
			wrong := newResult(nil)
			wrong.OutputSHA = baseline
			if err := ExecuteValidations(context.Background(), dispatch, wrong); err == nil {
				t.Fatal("accepted the wrong candidate")
			}
		})
	}
}
