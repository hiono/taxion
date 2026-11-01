package controller

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func TestStoppedArtifactReviewerReusesValidationWithoutCoderOrInputReplay(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			testStoppedArtifactReviewerRecovery(t, prepared)
		})
	}
}

func testStoppedArtifactReviewerRecovery(t *testing.T, prepared bool) {
	counter := filepath.Join(t.TempDir(), "validation-count")
	db, ledger, request, selected := artifactFixture(t, []string{"test -s internal/input-00.txt && printf x >> " + counter}, false)
	if err := db.UpdateWorkspace(func(s *store.Workspace) error {
		task, _ := s.Graph.GraphTask(request.TaskID)
		if err := s.Graph.Transition(task.ID, core.Draft, "human", "bind explicit billing guidance", core.TransitionProof{}); err != nil {
			return err
		}
		task.Status = core.Draft
		task.BudgetGuidance, _ = core.NewBillingBudgetGuidance("", "", selected.Config.Billing)
		task.AuthorizationID = "explicit-budget"
		task.Decisions = append(task.Decisions, core.TaskDecision{ID: task.AuthorizationID, Source: "human:budget", Text: "retain explicit billing guidance"})
		if err := s.Graph.Track(task, "human", "retain budget policy in successor"); err != nil {
			return err
		}
		return s.Graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	refreshArtifactPlan(t, db, &request)
	old, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	old, err = ValidateReviewFlowInput(t.Context(), db, ledger, old.ID, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	reviewer, _ := state.Graph.GraphDispatch(old.LastDispatchID())
	if !prepared {
		if err := os.MkdirAll(reviewer.Instruction.Worktree, 0700); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	var worker *configuredRuntime
	old, err = RunReviewFlow(t.Context(), db, ledger, old.ID, func(d core.GraphDispatch) (runtime.Identity, error) {
		calls++
		if !prepared {
			return nil, fmt.Errorf("must not start")
		}
		if err := BindConfiguration(db, d.ID, selected); err != nil {
			return nil, err
		}
		worker = &configuredRuntime{
			fakeRuntime:   &fakeRuntime{role: "REVIEWER", worktree: d.Instruction.Worktree, hash: "prepared-review"},
			selection:     mustFlowJSON(t, selected.Config),
			repositoryErr: fmt.Errorf("REP-006 GitLab API identity cannot be verified: identity-query: GET failed; provider details withheld"),
		}
		return worker, nil
	}, io.Discard, request.Configuration)
	wantCalls := 0
	if prepared {
		wantCalls = 1
	}
	if err == nil || old.Status != "STOPPED" || calls != wantCalls || old.Artifact.Validation.Status != "PASSED" {
		t.Fatalf("preparation failure not reproduced: %+v %v", old, err)
	}
	state, _ = db.LoadWorkspace()
	reviewer, _ = state.Graph.GraphDispatch(reviewer.ID)
	if prepared && (worker.starts != 0 || worker.repositoryChecks != 1 || reviewer.ReviewWorktreeSource == "" ||
		reviewer.ExecutionStarted || reviewer.RuntimeSessionID != "" || reviewer.Acknowledged || len(state.Records.Events) != 0 ||
		!strings.Contains(old.Reason, "identity-query")) {
		t.Fatal("identity failure did not preserve the prepared, unstarted Reviewer", reviewer, worker)
	}
	index := gitTest(t, old.Contract.Worktree, "ls-files", "--stage")
	reviewIndex := ""
	if prepared {
		reviewIndex = gitTest(t, reviewer.Instruction.Worktree, "ls-files", "--stage")
	}
	reconcile := DispatchReconciliationRequest{
		FlowID: old.ID, DispatchID: reviewer.ID, Actor: "controller", Source: "evidence:quiescence",
		Reason:                     "Retire unstarted Reviewer; preserve passed validation",
		Evidence:                   []string{"no runner, process or SDK execution"},
		NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
	}
	preview, err := ReconcileStoppedDispatch(db, reconcile, true)
	if err != nil {
		t.Fatal(err)
	}
	if prepared {
		if preview.PreservedReviewWorktree != reviewer.Instruction.Worktree {
			t.Fatal("preview omitted the preserved managed worktree")
		}
		for _, change := range []string{"edited", "untracked", "ignored", "staged", "branch", "wrong-source", "missing", "unregistered"} {
			t.Run(change, func(t *testing.T) {
				input := filepath.Join(reviewer.Instruction.Worktree, "internal/input-00.txt")
				original, err := os.ReadFile(input)
				if err != nil {
					t.Fatal(err)
				}
				var snapshot store.Workspace
				current, _ := db.LoadWorkspace()
				if err := core.DecodeGraphInput(strings.NewReader(string(mustFlowJSON(t, current))), &snapshot); err != nil {
					t.Fatal(err)
				}
				switch change {
				case "edited":
					if err := os.WriteFile(input, []byte("drift"), 0600); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.WriteFile(input, original, 0600) })
				case "untracked", "ignored":
					extra := filepath.Join(reviewer.Instruction.Worktree, "extra.tmp")
					if change == "ignored" {
						exclude := gitTest(t, reviewer.Instruction.Worktree, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
						before, err := os.ReadFile(exclude)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(exclude, append(before, []byte("\nextra.tmp\n")...), 0600); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = os.WriteFile(exclude, before, 0600) })
					}
					if err := os.WriteFile(extra, []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Remove(extra) })
				case "staged":
					gitTest(t, reviewer.Instruction.Worktree, "add", "internal/input-00.txt")
					t.Cleanup(func() { gitTest(t, reviewer.Instruction.Worktree, "read-tree", reviewer.Instruction.BaselineSHA) })
				case "branch":
					gitTest(t, reviewer.Instruction.Worktree, "symbolic-ref", "HEAD", "refs/heads/prepared-review")
					gitTest(t, reviewer.Instruction.Worktree, "update-ref", "HEAD", reviewer.Instruction.BaselineSHA)
					t.Cleanup(func() {
						gitTest(t, reviewer.Instruction.Worktree, "checkout", "--detach", reviewer.Instruction.BaselineSHA)
					})
				case "wrong-source":
					snapshot.Graph.Dispatches[len(snapshot.Graph.Dispatches)-1].ReviewWorktreeSource = reviewer.Instruction.Worktree
				case "missing":
					moved := reviewer.Instruction.Worktree + "-preserved"
					if err := os.Rename(reviewer.Instruction.Worktree, moved); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Rename(moved, reviewer.Instruction.Worktree) })
				case "unregistered":
					link := gitTest(t, reviewer.Instruction.Worktree, "rev-parse", "--path-format=absolute", "--git-path", "gitdir")
					before, err := os.ReadFile(link)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(link, []byte(filepath.Join(t.TempDir(), "not-the-review", ".git")+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.WriteFile(link, before, 0600) })
				}
				if _, err := previewDispatchReconciliation(snapshot, reconcile); err == nil {
					t.Fatal("unsafe prepared review accepted", change)
				}
			})
		}
		afterPreview, _ := db.LoadWorkspace()
		if !reflect.DeepEqual(state, afterPreview) {
			t.Fatal("read-only reconciliation preview changed stored state")
		}
		path := filepath.Join(reviewer.Instruction.Worktree, "internal/input-00.txt")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("drift after preview"), 0600); err != nil {
			t.Fatal(err)
		}
		stale := reconcile
		stale.ExpectedHash = preview.Hash
		if _, err := ReconcileStoppedDispatch(db, stale, false); err == nil {
			t.Fatal("application did not reverify prepared worktree after preview")
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	state, _ = db.LoadWorkspace()
	for name, mutate := range map[string]func(*store.Workspace){
		"started":           func(s *store.Workspace) { s.Graph.Dispatches[len(s.Graph.Dispatches)-1].ExecutionStarted = true },
		"acknowledged":      func(s *store.Workspace) { s.Graph.Dispatches[len(s.Graph.Dispatches)-1].Acknowledged = true },
		"session":           func(s *store.Workspace) { s.Graph.Dispatches[len(s.Graph.Dispatches)-1].RuntimeSessionID = "unknown" },
		"failed-validation": func(s *store.Workspace) { s.Graph.ReviewFlows[0].Artifact.Validation.Status = "FAILED" },
	} {
		t.Run(name, func(t *testing.T) {
			var bad store.Workspace
			if err := core.DecodeGraphInput(strings.NewReader(string(mustFlowJSON(t, state))), &bad); err != nil {
				t.Fatal(err)
			}
			mutate(&bad)
			if _, err := previewDispatchReconciliation(bad, reconcile); err == nil {
				t.Fatal("accepted uncertain execution or invalid validation")
			}
		})
	}
	reconcile.ExpectedHash = preview.Hash
	if _, err := ReconcileStoppedDispatch(db, reconcile, false); err != nil {
		t.Fatal(err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if index != gitTest(t, old.Contract.Worktree, "ls-files", "--stage") ||
		prepared && reviewIndex != gitTest(t, reviewer.Instruction.Worktree, "ls-files", "--stage") {
		t.Fatal("reconciliation changed the source or review index")
	}
	cancelled, _ := state.Graph.GraphDispatch(reviewer.ID)
	target, _ := core.ObserveFeedbackTarget(cancelled)
	task := old.Contract
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "review-recovery", core.Draft, "", nil
	task.CreatedFrom, task.Supersedes = "flow:"+old.ID, old.TaskID
	task.TargetSHA, task.TargetTreeSHA, task.TargetChangedFiles = old.Artifact.Target.CommitSHA, old.Artifact.Target.TreeSHA, old.Artifact.Target.ChangedFiles
	intake := IntakeRequest{Task: task, Request: "Review unchanged validated input without replay",
		Actor: "human", Source: "human:recovery", ReconciliationHash: preview.Hash}
	bad := intake
	bad.Task.ValidationCommands = []string{"true"}
	if _, err := ProposeIntake(db, bad); err == nil {
		t.Fatal("accepted changed validation contract")
	}
	proposal, err := ProposeIntake(db, intake)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := IntakeHash(proposal)
	_, err = ConfirmIntake(db, IntakeConfirmation{
		TaskID: task.ID, ExpectedHash: hash, Actor: "human", Source: "human:recovery",
		Reason: "Approve review-only successor within saved limits", Selection: selected.Config,
		ConfigurationDecisionID: selected.Config.Execution.ApprovalRef + "@task:" + task.ID,
		AdditionalTaskIDs:       []string{"recovery-flow-review-0"}, ReplacesFlow: old.ID,
		Target: &target, ReconciliationHash: preview.Hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	next := request
	next.ID, next.TaskID, next.StagePlanHash = "recovery-flow", task.ID, ""
	next.ReviewRoot = filepath.Join(t.TempDir(), "reviews-not-created")
	next.ReuseArtifactValidation = true
	next.ContinuationChange = "let Controller create the new review path"
	next.SavedOutputDecision = "reuse same candidate and exact passed receipt"
	refreshArtifactPlan(t, db, &next)
	input := filepath.Join(old.Contract.Worktree, "internal/input-00.txt")
	original, _ := os.ReadFile(input)
	if err := os.WriteFile(input, []byte("drift"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StartReviewFlow(db, ledger, next); err == nil {
		t.Fatal("reused validation after candidate drift")
	}
	if err := os.WriteFile(input, original, 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := StartReviewFlow(db, ledger, next)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Artifact.ValidationSourceFlowID != old.ID ||
		!reflect.DeepEqual(recovered.Artifact.Validation, old.Artifact.Validation) {
		t.Fatal("receipt was changed")
	}
	recovered, err = ValidateReviewFlowInput(t.Context(), db, ledger, recovered.ID, request.Configuration)
	if err != nil || len(recovered.DispatchIDs) != 1 || recovered.Budget.MaxCoderSessions != 0 {
		t.Fatalf("review-only successor failed: %+v %v", recovered, err)
	}
	if count, err := os.ReadFile(counter); err != nil || string(count) != "x" {
		t.Fatalf("input validation was replayed before review: %q %v", count, err)
	}
	executeFlowDispatch(t, db, recovered, "APPROVE", true, selected)
	recovered, err = AdvanceReviewFlow(db, ledger, recovered.ID, request.Configuration)
	if err != nil || recovered.Status != "COMPLETED" {
		t.Fatalf("same-target independent review failed: %+v %v", recovered, err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	preserved, _ := state.Graph.ReviewFlow(old.ID)
	if !reflect.DeepEqual(preserved, old) || !reflect.DeepEqual(preserved.Artifact.Validation, recovered.Artifact.Validation) {
		t.Fatal("old stop or successful validation changed")
	}
	if _, err := os.Stat(reviewer.Instruction.Worktree); err != nil {
		t.Fatal("pre-existing review path was removed")
	}
	if count, err := os.ReadFile(counter); err != nil || string(count) != "xx" {
		t.Fatalf("Reviewer result acceptance lost its existing validation check: %q %v", count, err)
	}
	if prepared {
		data, err := os.ReadFile(filepath.Join(reviewer.Instruction.Worktree, "internal/input-00.txt"))
		if err != nil || string(data) != "user implementation\n" ||
			reviewIndex != gitTest(t, reviewer.Instruction.Worktree, "ls-files", "--stage") {
			t.Fatal("successor changed the preserved review worktree", err)
		}
	}
}
