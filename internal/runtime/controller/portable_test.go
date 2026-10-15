package controller

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/portable"
	"github.com/hiono/taxion/internal/store"
)

func TestPortableStoppedCoderContinuesWithoutOriginalStoresOrGit(t *testing.T) {
	db, ledger, request := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("partial-and-completed-evidence", func(t *testing.T) {
		for _, mode := range []string{"partial", "completed"} {
			t.Run(mode, func(t *testing.T) {
				db, ledger, request := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
				flow, err := StartReviewFlow(db, ledger, request)
				if err != nil {
					t.Fatal(err)
				}
				var coder core.GraphDispatch
				if mode == "partial" {
					worker := &fakeRuntime{role: "CODER", worktree: flow.Contract.Worktree,
						hash: "portable-partial", sessionID: "portable-partial"}
					control, err := New(db, worker)
					if err != nil {
						t.Fatal(err)
					}
					id := flow.DispatchIDs[0]
					if _, err := control.Start(t.Context(), id); err != nil {
						t.Fatal(err)
					}
					if err := control.Send(t.Context(), id, "Execute the approved Task."); err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Join(flow.Contract.Worktree, "internal"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(flow.Contract.Worktree, "internal/app.go"), []byte("partial"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := control.Stop(t.Context(), id); err != nil {
						t.Fatal(err)
					}
					saved, err := db.LoadWorkspace()
					if err != nil {
						t.Fatal(err)
					}
					coder, _ = saved.Graph.GraphDispatch(id)
					flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
					if !errors.Is(err, ErrFlowStopped) || coder.Result.PartialGitEvidence == nil {
						t.Fatal("partial work was not reconciled", err)
					}
				} else {
					coder = executeFlowDispatch(t, db, flow, "", true)
					flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
					if err != nil {
						t.Fatal(err)
					}
					executeFlowDispatch(t, db, flow, "APPROVE", true)
					flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
					if err != nil || flow.Status != "COMPLETED" {
						t.Fatal(err)
					}
				}
				archive := filepath.Join(t.TempDir(), "portable.db")
				report, err := portable.Create(t.Context(), filepath.Join(filepath.Dir(ledger), "workspace.db"),
					filepath.Join(filepath.Dir(ledger), "memory.db"), archive)
				if err != nil {
					t.Fatal(err)
				}
				destination := filepath.Join(t.TempDir(), "new-machine")
				if err := os.Rename(flow.Contract.Worktree, flow.Contract.Worktree+"-offline"); err != nil {
					t.Fatal(err)
				}
				defer os.Rename(flow.Contract.Worktree+"-offline", flow.Contract.Worktree)
				if _, err := portable.Restore(t.Context(), archive, portable.Approval{
					Hash: report.Hash, Destination: destination, Actor: "human", Source: "human:move",
				}); err != nil {
					t.Fatal(err)
				}
				restored, err := store.Open(filepath.Join(destination, ".taxion", "workspace.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer restored.Close()
				state, err := restored.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				if mode == "completed" {
					if err := revalidateFlowResult(state.Graph, flow, state.Records); err != nil {
						t.Fatal("restored completed review cannot be revalidated", err)
					}
					return
				}
				located := state.Records.RestoredDispatch(coder)
				source := located.Result.PartialGitEvidence.Worktree
				root := filepath.Join(destination, "successor")
				gitTest(t, source, "worktree", "add", "-b", "work/partial-successor", root, coder.Result.PartialGitEvidence.HeadSHA)
				gitTest(t, root, "read-tree", "--reset", "-u", coder.Result.PartialGitEvidence.TreeSHA)
				task := flow.Contract
				task.Worktree, task.Branch = root, "work/partial-successor"
				task.TargetSHA = coder.ExpectedStartSHA()
				task.TargetTreeSHA = coder.Result.PartialGitEvidence.TreeSHA
				task.TargetChangedFiles = coder.Result.PartialGitEvidence.ChangedFiles
				if err := gitproof.VerifyPartialSuccessor(located, task); err != nil {
					t.Fatal("restored partial work cannot continue", err)
				}
			})
		}
	})
	archive := filepath.Join(t.TempDir(), "portable.db")
	if _, err := portable.Create(t.Context(), filepath.Join(filepath.Dir(ledger), "workspace.db"),
		filepath.Join(filepath.Dir(ledger), "memory.db"), archive); err == nil {
		t.Fatal("live flow was snapshotted")
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("failed collection left an output")
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	flow = persistLegacyBudgetStoppedFlow(t, db, flow.ID)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("unrecorded-edit", func(t *testing.T) {
		path := filepath.Join(flow.Contract.Worktree, "internal/app.go")
		saved, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(saved, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		defer os.WriteFile(path, saved, 0600)
		if _, err := portable.Create(t.Context(), filepath.Join(filepath.Dir(ledger), "workspace.db"),
			filepath.Join(filepath.Dir(ledger), "memory.db"), filepath.Join(t.TempDir(), "edited.db")); err == nil {
			t.Fatal("unrecorded changes were silently omitted")
		}
	})
	t.Run("missing-object", func(t *testing.T) {
		object := coder.Instruction.BaselineSHA
		path := filepath.Join(flow.Contract.Worktree, ".git", "objects", object[:2], object[2:])
		if err := os.Rename(path, path+"-offline"); err != nil {
			t.Fatal(err)
		}
		defer os.Rename(path+"-offline", path)
		if _, err := portable.Create(t.Context(), filepath.Join(filepath.Dir(ledger), "workspace.db"),
			filepath.Join(filepath.Dir(ledger), "memory.db"), filepath.Join(t.TempDir(), "incomplete.db")); err == nil {
			t.Fatal("missing artifact was silently omitted")
		}
	})
	report, err := portable.Create(t.Context(), filepath.Join(filepath.Dir(ledger), "workspace.db"),
		filepath.Join(filepath.Dir(ledger), "memory.db"), archive)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repositories != 1 || !report.Memory {
		t.Fatal(report)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("snapshot changed source", err)
	}
	// The original repository and both stores become unavailable before restore.
	original := filepath.Dir(ledger)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	hidden := original + "-offline"
	if err := os.Rename(original, hidden); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(hidden, original)
	destination := filepath.Join(t.TempDir(), "restored")
	approval := portable.Approval{Hash: report.Hash, Destination: destination, Actor: "human", Source: "human:move"}
	t.Run("mid-restore-failure", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		failed := approval
		failed.Destination = filepath.Join(t.TempDir(), "failed")
		if _, err := portable.Restore(t.Context(), archive, failed); err == nil {
			t.Fatal("restore succeeded without Git")
		}
		if _, err := os.Stat(failed.Destination); !os.IsNotExist(err) {
			t.Fatal("mid-restore failure left usable partial state")
		}
	})
	if _, err := portable.Restore(t.Context(), archive, approval); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(filepath.Join(destination, ".taxion", "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	state, err := restored.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	got := state
	got.Records.Restore = nil
	if !reflect.DeepEqual(got, before) {
		t.Fatal("restore changed immutable state, usage or results")
	}
	located := state.Records.RestoredDispatch(coder)
	root := located.Result.GitEvidence.Worktree
	if root == coder.Result.GitEvidence.Worktree {
		t.Fatal("source placement was not mapped")
	}
	gitTest(t, root, "gc", "--prune=now")
	successorRoot := filepath.Join(destination, "successor")
	gitTest(t, root, "worktree", "add", "-b", "work/successor", successorRoot, coder.Result.OutputSHA)
	gitTest(t, successorRoot, "read-tree", "--reset", "-u", coder.Result.OutputTreeSHA)
	task := flow.Contract
	task.ID, task.Worktree, task.Branch = "restored-successor", successorRoot, "work/successor"
	task.CreatedFrom, task.Supersedes = "flow:"+flow.ID, flow.TaskID
	task.TargetSHA, task.TargetTreeSHA = coder.Result.OutputSHA, coder.Result.OutputTreeSHA
	task.TargetChangedFiles = coder.Result.ChangedFiles
	if err := gitproof.VerifyCompletedSuccessor(located, task); err != nil {
		t.Fatal("restored tree cannot safely continue", err)
	}
	if _, err := budgetContinuationSource(state.Graph, task, coder.ID, state.Records); err != nil {
		t.Fatal(err)
	}
	task.Status, task.AuthorizationID, task.Decisions = core.Draft, "", nil
	_, _, _, selection := executionConfigurationFixture(t)
	proposal, err := ProposeIntake(restored, IntakeRequest{Request: "Continue the restored result at independent review.",
		Task: task, Actor: "human", Source: "human:move"})
	if err != nil {
		t.Fatal(err)
	}
	proposalHash, err := IntakeHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	target, err := core.ObserveFeedbackTarget(coder)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := ConfirmIntake(restored, IntakeConfirmation{
		TaskID: proposal.ID, ExpectedHash: proposalHash, Actor: "human", Source: "human:move",
		Reason:    "Approve the restored placement and a new independent-review budget.",
		Selection: selection.Config, ReplacesFlow: flow.ID, Target: &target,
		AdditionalTaskIDs:       []string{"restored-review-review-0"},
		ConfigurationDecisionID: core.ConfigurationApprovalID(selection.Config.Execution.ApprovalRef, proposal.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	repairs := 0
	successor, err := StartReviewFlow(restored, filepath.Join(destination, ".taxion", "ledger.json"), ReviewFlowRequest{
		ID: "restored-review", TaskID: approved.ID, Repository: approved.Repo,
		ReviewRoot: filepath.Join(destination, "reviews"), ContinueFromDispatchID: coder.ID,
		MaxRepairs: &repairs, TimeoutSeconds: 60,
		Authorization: core.TaskDecision{ID: "new-budget", Source: "human:move", Text: "Approve review of saved result"},
	})
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, restored, successor, "APPROVE", true, selection)
	successor, err = AdvanceReviewFlow(restored, filepath.Join(destination, ".taxion", "ledger.json"), successor.ID)
	if err != nil || successor.Status != "COMPLETED" {
		t.Fatal("restored result did not complete independent review", err)
	}
	secondArchive := filepath.Join(t.TempDir(), "second.db")
	secondReport, err := portable.Create(t.Context(), filepath.Join(destination, ".taxion", "workspace.db"),
		filepath.Join(destination, ".taxion", "memory.db"), secondArchive)
	if err != nil {
		t.Fatal("continued workspace could not be snapshotted again", err)
	}
	secondDestination := filepath.Join(t.TempDir(), "second-machine")
	if _, err := portable.Restore(t.Context(), secondArchive, portable.Approval{
		Hash: secondReport.Hash, Destination: secondDestination, Actor: "human", Source: "human:second-move",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(filepath.Join(secondDestination, ".taxion", "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondState, err := second.LoadWorkspace()
	if err != nil || len(secondState.Records.Restores) != 1 {
		t.Fatal("prior placement receipt was lost", err)
	}
	if err := revalidateFlowResult(secondState.Graph, successor, secondState.Records); err != nil {
		t.Fatal("second transfer lost approved results", err)
	}
	if _, err := portable.Restore(t.Context(), archive, approval); err == nil {
		t.Fatal("existing destination was overwritten")
	}
	approval.Destination = filepath.Join(t.TempDir(), "wrong-hash")
	approval.Hash = "incorrect"
	if _, err := portable.Restore(t.Context(), archive, approval); err == nil {
		t.Fatal("unapproved snapshot was installed")
	}
	if _, err := os.Stat(approval.Destination); !os.IsNotExist(err) {
		t.Fatal("failed restore left a partial destination")
	}
}
