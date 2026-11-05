package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestNormalReviewerReconcilesLegacyInstructionWithoutLineage(t *testing.T) {
	db, _, flow, coder, request := stoppedNormalReviewer(t, false, false)
	state, _ := db.LoadWorkspace()
	keep := func(decisions []core.TaskDecision) []core.TaskDecision {
		return slices.DeleteFunc(slices.Clone(decisions), func(d core.TaskDecision) bool {
			return strings.HasPrefix(d.ID, "coder-review-target:")
		})
	}
	for i := range state.Graph.Dispatches {
		d := &state.Graph.Dispatches[i]
		if d.ID != flow.LastDispatchID() {
			continue
		}
		d.Instruction.Decisions = keep(d.Instruction.Decisions)
		data, err := json.Marshal(d.Instruction)
		if err != nil {
			t.Fatal(err)
		}
		d.InstructionHash = fmt.Sprintf("%x", sha256.Sum256(data))
		for j := range state.Graph.Tasks {
			if state.Graph.Tasks[j].ID == d.TaskID {
				state.Graph.Tasks[j].Decisions = keep(state.Graph.Tasks[j].Decisions)
			}
		}
		for j := range state.Graph.History {
			if state.Graph.History[j].TaskID == d.TaskID {
				state.Graph.History[j].Task.Decisions = keep(state.Graph.History[j].Task.Decisions)
			}
		}
		diffBaseline, err := d.ReviewDiffBaselineSHA()
		if err != nil || diffBaseline != coder.Result.OutputSHA {
			t.Fatal("legacy fixture did not preserve the problematic B-based instruction")
		}
	}
	legacy, err := store.Open(filepath.Join(t.TempDir(), "legacy-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.InitializeWorkspace(state); err != nil {
		t.Fatal(err)
	}
	preview, err := ReconcileStoppedDispatch(legacy, request, true)
	if err != nil || preview.InputTreeSHA != coder.Result.OutputTreeSHA {
		t.Fatal("legacy issued instruction could not be reconciled from its accepted source", err)
	}
	request.ExpectedHash = preview.Hash
	if _, err := ReconcileStoppedDispatch(legacy, request, false); err != nil {
		t.Fatal(err)
	}
	after, _ := legacy.LoadWorkspace()
	saved, _ := after.Graph.GraphDispatch(flow.LastDispatchID())
	original, _ := state.Graph.GraphDispatch(saved.ID)
	if !reflect.DeepEqual(saved.Instruction, original.Instruction) || saved.InstructionHash != original.InstructionHash {
		t.Fatal("legacy reconciliation rewrote the immutable old instruction")
	}
}

func stoppedNormalReviewer(t *testing.T, prepared, expired bool) (*store.Store, string, core.ReviewFlow, core.GraphDispatch, DispatchReconciliationRequest) {
	t.Helper()
	db, ledger, flow, coder := acceptedCarriedCoder(t, core.OutputModeWorktreeTreeSnapshot)
	if prepared {
		if _, err := PrepareReview(db, flow.LastDispatchID(), flow.Contract.Worktree); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpdateWorkspace(func(s *store.Workspace) error {
		if expired {
			flow.StartedAt, flow.Deadline = time.Now().UTC().Add(-2*time.Minute), time.Now().UTC().Add(-time.Minute)
		}
		if err := holdReviewFlowForStop(&s.Graph, &flow, "Reviewer preparation stopped before execution"); err != nil {
			return err
		}
		flow.Status, flow.StopCode, flow.Reason = "STOPPED", "SAFETY_STOP", "Reviewer preparation stopped before execution"
		storeFlow(s, flow)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return db, ledger, flow, coder, DispatchReconciliationRequest{
		FlowID: flow.ID, DispatchID: flow.LastDispatchID(), Actor: "human", Source: "human:synthetic-reconciliation",
		Reason:                     "Retire the issued Reviewer while preserving accepted Coder output and unknown delivery",
		Evidence:                   []string{"synthetic operator observed no active process; delivery remains unknown"},
		NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
	}
}

func TestNormalReviewerReconciliationContinuesAcceptedOutputOnly(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, expired := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v/expired=%v", prepared, expired), func(t *testing.T) {
				db, ledger, old, coder, request := stoppedNormalReviewer(t, prepared, expired)
				before, _ := db.LoadWorkspace()
				report, err := ReconcileStoppedDispatch(db, request, true)
				if err != nil || report.StateChanged || report.InputTreeSHA != coder.Result.OutputTreeSHA ||
					report.InputOrigin != "accepted-coder-output" || report.DeliveryStatus != "unknown" || report.UsageStatus != "unavailable" ||
					(report.PreservedReviewWorktree != "") != prepared {
					t.Fatal("invalid normal Reviewer reconciliation preview", report, err)
				}
				after, _ := db.LoadWorkspace()
				if !reflect.DeepEqual(before, after) {
					t.Fatal("preview modified saved work")
				}
				request.ExpectedHash = report.Hash
				if _, err := ReconcileStoppedDispatch(db, request, false); err != nil {
					t.Fatal(err)
				}
				reconciled, _ := db.LoadWorkspace()
				savedCoder, _ := reconciled.Graph.GraphDispatch(coder.ID)
				savedFlow, _ := reconciled.Graph.ReviewFlow(old.ID)
				cancelled, _ := reconciled.Graph.GraphDispatch(old.LastDispatchID())
				if !reflect.DeepEqual(savedCoder, coder) || !reflect.DeepEqual(savedFlow, old) ||
					!reflect.DeepEqual(before.Records.Events, reconciled.Records.Events) ||
					cancelled.Status != core.DispatchCancelled || cancelled.Result.OutputSHA != "" {
					t.Fatal("reconciliation rewrote accepted output, receipts or stop history")
				}
				missingAudit := reconciled.Records
				missingAudit.DispatchReconciliations = nil
				if missingAudit.VerifyDispatchReconciliations(reconciled.Graph) == nil {
					t.Fatal("cancelled Reviewer accepted without reconciliation audit")
				}
				task := old.Contract
				task.ID, task.Status, task.AuthorizationID, task.Decisions = "review-only-successor", core.Draft, "", nil
				task.CreatedFrom, task.Supersedes = "flow:"+old.ID, old.TaskID
				task.TargetSHA, task.TargetTreeSHA, task.TargetChangedFiles = coder.Result.OutputSHA, coder.Result.OutputTreeSHA, coder.Result.ChangedFiles
				task.Worktree, task.Branch = filepath.Join(t.TempDir(), "successor"), "work/review-only-successor"
				gitTest(t, old.Contract.Worktree, "worktree", "add", "-b", task.Branch, task.Worktree, task.TargetSHA)
				for _, path := range coder.Result.ChangeManifest.ReportedChangedFiles {
					data, err := os.ReadFile(filepath.Join(old.Contract.Worktree, path))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(task.Worktree, path), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				missingReceipt := reconciled
				missingReceipt.Records.Events = nil
				if err := verifyReconciledInput(missingReceipt, task, report.Hash); err == nil {
					t.Fatal("successor intake accepted a Coder result without its controller receipt")
				}
				proposal, err := ProposeIntake(db, IntakeRequest{Task: task, Request: "Review exact accepted output without Coder or input validation replay",
					Actor: request.Actor, Source: request.Source, ReconciliationHash: report.Hash})
				if err != nil {
					t.Fatal(err)
				}
				hash, _ := IntakeHash(proposal)
				target, _ := core.ObserveFeedbackTarget(cancelled)
				_, _, _, selected := executionConfigurationFixture(t)
				approved, err := ConfirmIntake(db, IntakeConfirmation{
					TaskID: task.ID, ExpectedHash: hash, Actor: request.Actor, Source: request.Source,
					Reason:    "Approve unchanged candidate and a new future review window; preserve cumulative use",
					Selection: selected.Config, ReplacesFlow: old.ID, Target: &target, ReconciliationHash: report.Hash,
					AdditionalTaskIDs: []string{"review-only-flow-review-0"},
				})
				if err != nil || approved.Status != core.Ready {
					t.Fatal("normal review-only successor confirmation failed", err)
				}
				zero := 0
				deadline := time.Now().UTC().Add(5 * time.Minute)
				configuration, err := core.SelectApprovalConfiguration(mustFlowJSON(t, selected.Config), selected.Sources)
				if err != nil {
					t.Fatal(err)
				}
				next := ReviewFlowRequest{
					ID: "review-only-flow", TaskID: approved.ID, Repository: approved.Repo, ReviewRoot: filepath.Join(t.TempDir(), "reviews"),
					ContinueFromDispatchID: coder.ID, MaxRepairs: &zero, TimeoutSeconds: 600, Deadline: &deadline,
					MaxNanoAIU: 1e12, Configuration: configuration,
					StageBudget:         &core.StageBudget{Version: 1, ValidationSeconds: 10, ReviewSeconds: 30, ReviewNanoAIU: 100},
					ContinuationChange:  "New explicitly approved future deadline for an unstarted Reviewer",
					SavedOutputDecision: "Retain accepted Coder, four successful validation receipts and cumulative usage",
					Authorization:       core.TaskDecision{ID: "review-successor", Source: request.Source, Text: "Authorize exact future deadline and Reviewer-only execution within displayed ceilings"},
				}
				ready, _ := db.LoadWorkspace()
				expiredPlan := next
				past := time.Now().UTC().Add(-time.Second)
				expiredPlan.Deadline = &past
				if _, err := PreviewStagePlan(ready.Graph, expiredPlan); err == nil {
					t.Fatal("expired successor deadline was accepted")
				}
				plan, err := PreviewStagePlan(ready.Graph, next)
				if err != nil {
					t.Fatal(err)
				}
				next.StagePlanHash = plan.Plan.Hash
				if err := db.UpdateWorkspace(func(s *store.Workspace) error {
					s.Records.Events = nil
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := StartReviewFlow(db, ledger, next); err == nil {
					t.Fatal("successor start accepted loss of the Coder controller receipt")
				}
				if err := db.UpdateWorkspace(func(s *store.Workspace) error {
					s.Records.Events = ready.Records.Events
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				changedPlan := next
				later := deadline.Add(time.Minute)
				changedPlan.Deadline = &later
				if _, err := StartReviewFlow(db, ledger, changedPlan); err == nil {
					t.Fatal("changed absolute deadline reused the previous plan approval")
				}
				successor, err := StartReviewFlow(db, ledger, next)
				if err != nil {
					t.Fatal("saved Coder did not continue directly at review", err)
				}
				state, _ := db.LoadWorkspace()
				first, _ := state.Graph.GraphDispatch(successor.LastDispatchID())
				if successor.Budget.MaxCoderSessions != 0 || successor.Budget.MaxReviewerSessions != 1 ||
					successor.MaxRepairs != 0 || len(successor.DispatchIDs) != 1 || first.Recipient != "REVIEWER" ||
					!successor.Deadline.Equal(deadline) || first.RelatedID != coder.ID ||
					!slices.Equal(first.Instruction.TargetChangedFiles, coder.Result.ChangedFiles) {
					t.Fatal("successor changed execution scope or candidate", successor)
				}
				data, _ := os.ReadFile(filepath.Join(filepath.Dir(ledger), "validation-count"))
				if string(data) != "xxxx" {
					t.Fatal("reconciliation or continuation replayed accepted validation", string(data))
				}
				if _, err := PrepareReview(db, first.ID, successor.Contract.Worktree); err != nil {
					t.Fatal(err)
				}
				executeFlowDispatch(t, db, successor, "APPROVE", true, selected)
				if _, err := AdvanceReviewFlow(db, ledger, successor.ID, configuration); err != nil {
					t.Fatal(err)
				}
				data, _ = os.ReadFile(filepath.Join(filepath.Dir(ledger), "validation-count"))
				if string(data) != "xxxxxxxx" {
					t.Fatal("normal Reviewer result-acceptance validation was waived or replayed", string(data))
				}
			})
		}
	}
}

func TestNormalReviewerReconciliationRejectsUnsafeState(t *testing.T) {
	db, _, old, coder, request := stoppedNormalReviewer(t, false, false)
	before, _ := db.LoadWorkspace()
	for _, name := range []string{"acknowledged", "started", "session", "usage", "owner", "receipt", "reviewer-receipt", "bad-manifest", "target-paths", "contract", "uncertainty"} {
		t.Run(name, func(t *testing.T) {
			var state store.Workspace
			if err := core.DecodeGraphInput(strings.NewReader(string(mustFlowJSON(t, before))), &state); err != nil {
				t.Fatal(err)
			}
			r := request
			for i := range state.Graph.Dispatches {
				d := &state.Graph.Dispatches[i]
				if d.ID == old.LastDispatchID() {
					switch name {
					case "acknowledged":
						d.Acknowledged = true
					case "started":
						d.ExecutionStarted = true
					case "session":
						d.RuntimeSessionID = "unknown-session"
					case "usage":
						d.RuntimeUsage = []core.DispatchUsage{{Model: "synthetic-model"}}
					case "target-paths":
						d.Instruction.TargetChangedFiles = []string{"internal/app.go"}
					}
				}
				if d.ID == coder.ID && name == "bad-manifest" {
					d.Result.ChangeManifest.BaselineSHA = coder.Result.OutputSHA
				}
			}
			switch name {
			case "owner":
				state.Graph.ReviewFlows[0].RunnerID = "active-owner"
			case "receipt":
				state.Records.Events = nil
			case "reviewer-receipt":
				state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{DispatchID: old.LastDispatchID(), Type: "session-starting"})
			case "contract":
				state.Graph.Tasks[1].Objective = "changed"
			case "uncertainty":
				r.AcceptDeliveryUncertainty = false
			}
			if _, err := previewDispatchReconciliation(state, r); err == nil {
				t.Fatal("unsafe reconciliation accepted", name)
			}
		})
	}
}

func TestNormalReviewerReconciliationRejectsMutableGitDrift(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, change := range []string{"source", "extra", "ignored", "index", "head"} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, change), func(t *testing.T) {
				db, _, flow, _, request := stoppedNormalReviewer(t, prepared, false)
				before, _ := db.LoadWorkspace()
				reviewer, _ := before.Graph.GraphDispatch(flow.LastDispatchID())
				root := reviewer.Instruction.Worktree
				if !prepared {
					if err := os.MkdirAll(root, 0700); err != nil {
						t.Fatal(err)
					}
				}
				switch change {
				case "source":
					root = flow.Contract.Worktree
					if err := os.WriteFile(filepath.Join(root, "internal/app.go"), []byte("drift"), 0600); err != nil {
						t.Fatal(err)
					}
				case "extra":
					if err := os.WriteFile(filepath.Join(root, "extra.txt"), []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
				case "ignored":
					if prepared {
						exclude := gitTest(t, root, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
						if err := os.WriteFile(exclude, []byte("ignored.txt\n"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
				case "index":
					if prepared {
						gitTest(t, root, "add", "internal/app.go")
					}
				case "head":
					if prepared {
						gitTest(t, root, "switch", "-c", "unexpected-review-branch")
					}
				}
				if _, err := ReconcileStoppedDispatch(db, request, true); err == nil {
					t.Fatal("mutable target or unrecorded preparation was accepted")
				}
				after, _ := db.LoadWorkspace()
				if !reflect.DeepEqual(before, after) {
					t.Fatal("rejected preview modified saved state")
				}
				if _, err := os.Stat(root); err != nil {
					t.Fatal("rejected preview removed preserved worktree", err)
				}
			})
		}
	}
}

func TestNormalReviewerCombinedRecoveryExplainsUnsupportedSource(t *testing.T) {
	db, ledger, old, _, reconciliation := stoppedNormalReviewer(t, false, false)
	_, err := RecoverArtifactReview(db, ledger, ReviewRecoveryRequest{
		Reconciliation: reconciliation, TaskID: "new-task", FlowID: "new-flow",
		ReviewRoot: filepath.Join(t.TempDir(), "reviews"), Deadline: old.Deadline, TimeoutSeconds: 300,
		StageBudget: core.StageBudget{Version: 1},
	}, true)
	if err == nil || !strings.Contains(err.Error(), "source is a normal Coder Flow") ||
		!strings.Contains(err.Error(), "not evaluated") || !strings.Contains(err.Error(), "continueFromDispatchId") {
		t.Fatal("combined rejection hid the confirmed failure or claimed untested predicates", err)
	}
}
func acceptedCarriedCoder(t *testing.T, mode string) (*store.Store, string, core.ReviewFlow, core.GraphDispatch) {
	t.Helper()
	db, ledger, request := reviewFlowFixtureWithOutputMode(t, mode)
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask(request.TaskID)
	counter := filepath.Join(filepath.Dir(ledger), "validation-count")
	task.ValidationCommands = []string{}
	for i := range 4 {
		task.ValidationCommands = append(task.ValidationCommands, fmt.Sprintf("printf x >> %q; test -s internal/app.go # synthetic validation %d", counter, i))
	}
	if err := os.MkdirAll(filepath.Join(task.Worktree, "internal"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := range 24 {
		if err := os.WriteFile(filepath.Join(task.Worktree, fmt.Sprintf("internal/carried-%02d.txt", i)), []byte("carried\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gitTest(t, task.Worktree, "add", "internal")
	gitTest(t, task.Worktree, "commit", "-m", "preserved input")
	task.TargetSHA = gitTest(t, task.Worktree, "rev-parse", "HEAD")
	if mode == core.OutputModeWorktreeTreeSnapshot {
		task.TargetTreeSHA = gitTest(t, task.Worktree, "rev-parse", "HEAD^{tree}")
		for i := range 24 {
			task.TargetChangedFiles = append(task.TargetChangedFiles, fmt.Sprintf("internal/carried-%02d.txt", i))
		}
		gitTest(t, task.Worktree, "update-ref", "refs/taxion/snapshots/carried", task.TargetTreeSHA)
	}
	if err := db.UpdateWorkspace(func(s *store.Workspace) error {
		task.ID = "carried-implementation"
		task.Status = core.Draft
		if err := s.Graph.Track(task, "human", "retain original baseline and carried target"); err != nil {
			return err
		}
		s.Ledger.Tasks = append(s.Ledger.Tasks, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo})
		return s.Graph.Transition(task.ID, core.Ready, "human", "ready carried input", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	request.TaskID = task.ID
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = db.LoadWorkspace()
	coder, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
	control, err := New(db, &fakeRuntime{role: "CODER", worktree: task.Worktree, hash: "synthetic-config", sessionID: "synthetic-coder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), coder.ID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), coder.ID, "implement approved synthetic changes"); err != nil {
		t.Fatal(err)
	}
	usage, usageStatus := 1.0, "complete"
	if err := control.RecordUsage(coder.ID, core.DispatchUsage{Model: "synthetic-model", TotalNanoAIU: &usage, AICreditsStatus: &usageStatus}); err != nil {
		t.Fatal(err)
	}
	claim := []string{"internal/app.go", "internal/current-1.txt", "internal/current-2.txt", "internal/current-3.txt"}
	for _, path := range claim {
		if err := os.WriteFile(filepath.Join(task.Worktree, path), []byte("candidate\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	output := task.TargetSHA
	if mode != core.OutputModeWorktreeTreeSnapshot {
		gitTest(t, task.Worktree, "add", "internal")
		gitTest(t, task.Worktree, "commit", "-m", "feat: implement synthetic candidate", "-m", "intent(test): exercise accepted carried output")
		output = gitTest(t, task.Worktree, "rev-parse", "HEAD")
	}
	result := core.DispatchResult{
		DispatchID: coder.ID, TaskID: coder.TaskID, Recipient: "CODER", Repository: coder.Repository,
		Status: core.DispatchSucceeded, StartedSHA: task.TargetSHA, OutputSHA: output,
		ChangedFiles: claim, Evidence: []string{"test-report"}, NextTransition: core.WaitingForReview,
	}
	if _, err := control.Finish(t.Context(), coder.ID, string(mustFlowJSON(t, result))); err != nil {
		t.Fatal(err)
	}
	state, _ = db.LoadWorkspace()
	coder, _ = state.Graph.GraphDispatch(coder.ID)
	if len(coder.Result.ChangedFiles) != 28 || !slices.Equal(coder.Result.ChangeManifest.ReportedChangedFiles, claim) ||
		coder.Result.ChangeManifest.BaselineSHA != task.BaselineSHA || task.BaselineSHA == coder.Result.OutputSHA {
		t.Fatal("fixture did not formally accept full carried output and incremental claim")
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	return db, ledger, flow, coder
}

func TestNormalReviewerUsesOriginalCoderDiffBaseline(t *testing.T) {
	for _, mode := range []string{core.OutputModeWorktreeTreeSnapshot, core.OutputModeCommit} {
		t.Run(mode, func(t *testing.T) {
			db, ledger, flow, coder := acceptedCarriedCoder(t, mode)
			state, _ := db.LoadWorkspace()
			reviewer, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
			if _, err := PrepareReview(db, reviewer.ID, flow.Contract.Worktree); err != nil {
				t.Fatal(err)
			}
			if mode == core.OutputModeWorktreeTreeSnapshot {
				baseline, err := reviewer.ReviewDiffBaselineSHA()
				if err != nil || baseline != coder.Instruction.BaselineSHA {
					t.Fatal("review lost original baseline", baseline, err)
				}
			}
			executeFlowDispatch(t, db, flow, "APPROVE", true)
			if _, err := AdvanceReviewFlow(db, ledger, flow.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
