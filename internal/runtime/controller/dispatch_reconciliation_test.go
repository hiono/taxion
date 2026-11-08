package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

func stoppedInputFixture(t *testing.T) (*store.Store, string, core.ReviewFlow, DispatchReconciliationRequest) {
	t.Helper()
	db, ledger, start := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask(start.TaskID)
	if err := os.MkdirAll(filepath.Join(task.Worktree, "internal"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.Worktree, "internal/input.go"), []byte("prior partial input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, task.Worktree, "add", "internal/input.go")
	tree := gitTest(t, task.Worktree, "write-tree")
	gitTest(t, task.Worktree, "update-ref", "refs/taxion/snapshots/previous-input", tree)
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "carried-input", core.Draft, "", nil
	task.TargetSHA, task.TargetTreeSHA, task.TargetChangedFiles = task.BaselineSHA, tree, []string{"internal/input.go"}
	proposed, err := ProposeIntake(db, IntakeRequest{Request: "Continue the prior input, not a new output", Task: task, Actor: "human", Source: "human:input"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := IntakeHash(proposed)
	_, _, _, selected := executionConfigurationFixture(t)
	if _, err := ConfirmIntake(db, IntakeConfirmation{
		TaskID: task.ID, ExpectedHash: hash, Actor: "human", Source: "human:input", Reason: "Approve prior input",
		Selection: selected.Config,
	}); err != nil {
		t.Fatal(err)
	}
	start.TaskID = task.ID
	flow, err := StartReviewFlow(db, ledger, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		reason := "repository identity preflight failed"
		if err := holdReviewFlowForStop(&state.Graph, &flow, reason); err != nil {
			return err
		}
		flow.Status, flow.StopCode, flow.Reason = "STOPPED", "SAFETY_STOP", reason
		storeFlow(state, flow)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return db, ledger, flow, DispatchReconciliationRequest{
		FlowID: flow.ID, DispatchID: flow.DispatchIDs[0], Actor: "human", Source: "human:reconcile",
		Reason:                     "Retire issued instruction without asserting non-delivery",
		Evidence:                   []string{"synthetic human execution-quiescence check"},
		NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
	}
}

func TestStoppedDispatchReconciliationPreservesInputAndStop(t *testing.T) {
	db, _, flow, request := stoppedInputFixture(t)
	before, _ := db.LoadWorkspace()
	report, err := ReconcileStoppedDispatch(db, request, true)
	if err != nil || report.StateChanged || report.Hash == "" || report.InputTreeSHA != flow.Contract.TargetTreeSHA ||
		report.DeliveryStatus != "unknown" || report.UsageStatus != "unavailable" {
		t.Fatal("invalid preview", report, err)
	}
	again, err := ReconcileStoppedDispatch(db, request, true)
	if err != nil || !reflect.DeepEqual(report, again) {
		t.Fatal("preview was not stable", err)
	}
	afterPreview, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, afterPreview) {
		t.Fatal("preview changed workspace")
	}
	stale := request
	stale.ExpectedHash = strings.Repeat("a", 64)
	if _, err := ReconcileStoppedDispatch(db, stale, false); err == nil {
		t.Fatal("stale preview accepted")
	}
	request.ExpectedHash = report.Hash
	applied, err := ReconcileStoppedDispatch(db, request, false)
	if err != nil || !applied.StateChanged {
		t.Fatal("approved reconciliation failed", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := state.Graph.ReviewFlow(flow.ID)
	cancelled, _ := state.Graph.GraphDispatch(request.DispatchID)
	task, _ := state.Graph.GraphTask(flow.TaskID)
	if !reflect.DeepEqual(saved, flow) || task.Status != core.Blocked || cancelled.Status != core.DispatchCancelled ||
		cancelled.Result.OutputSHA != "" || cancelled.Result.OutputTreeSHA != "" || cancelled.Result.PartialGitEvidence != nil ||
		len(state.Records.DispatchReconciliations) != 1 || len(state.Records.Events) != 0 {
		t.Fatal("recovery fabricated output/session or changed the old stop", saved, cancelled)
	}
	for _, legacy := range state.Ledger.Dispatches {
		if legacy.ID == cancelled.ID && legacy.Active {
			t.Fatal("cancelled graph Dispatch remained active in ledger")
		}
	}
	for _, change := range []string{"result", "input", "human-confirmation", "delivery-acceptance"} {
		var corrupted store.Workspace
		if err := core.DecodeGraphInput(strings.NewReader(string(mustFlowJSON(t, state))), &corrupted); err != nil {
			t.Fatal(err)
		}
		switch change {
		case "result":
			corrupted.Graph.Dispatches[0].Result.Evidence = []string{"not-started:" + cancelled.ID}
		case "input":
			corrupted.Records.DispatchReconciliations[0].InputContractHash = strings.Repeat("b", 64)
		case "human-confirmation":
			corrupted.Records.DispatchReconciliations[0].NoActiveExecutionConfirmed = false
		case "delivery-acceptance":
			corrupted.Records.DispatchReconciliations[0].AcceptDeliveryUncertainty = false
		}
		if err := corrupted.Records.VerifyFlowAudits(corrupted.Graph); err == nil {
			t.Fatal("corrupt reconciliation accepted on read", change)
		}
	}
	if _, err := ReconcileStoppedDispatch(db, request, false); err == nil {
		t.Fatal("repeat cancellation was accepted")
	}
	for _, mutate := range []func(*store.Workspace){
		func(s *store.Workspace) { s.Records.DispatchReconciliations = nil },
		func(s *store.Workspace) { s.Records.DispatchReconciliations[0].Reason = "changed" },
		func(s *store.Workspace) {
			f, _ := s.Graph.ReviewFlow(flow.ID)
			f.Reason = "changed old stop"
			storeFlow(s, f)
		},
	} {
		if err := db.UpdateWorkspace(func(s *store.Workspace) error { mutate(s); return nil }); err == nil {
			t.Fatal("immutable recovery/stop audit changed")
		}
	}

	successor := flow.Contract
	successor.ID, successor.Status, successor.AuthorizationID, successor.Decisions = "after-reconcile", core.Draft, "", nil
	successor.CreatedFrom, successor.Supersedes = "flow:"+flow.ID, flow.TaskID
	successor.Worktree, successor.Branch = filepath.Join(t.TempDir(), "successor"), "work/reconciled"
	gitTest(t, flow.Contract.Worktree, "worktree", "add", "-b", successor.Branch, successor.Worktree, successor.TargetSHA)
	if err := os.MkdirAll(filepath.Join(successor.Worktree, "internal"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(successor.Worktree, "internal/input.go"), []byte("prior partial input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	proposalRequest := IntakeRequest{Request: "Continue approved input with a new contract", Task: successor,
		Actor: "human", Source: "human:new-input", ReconciliationHash: report.Hash}
	for _, change := range []string{"same-worktree", "wrong-tree", "wrong-paths", "wrong-hash"} {
		wrong := proposalRequest
		switch change {
		case "same-worktree":
			wrong.Task.Worktree, wrong.Task.Branch = flow.Contract.Worktree, flow.Contract.Branch
		case "wrong-tree":
			wrong.Task.TargetTreeSHA = strings.Repeat("f", 40)
		case "wrong-paths":
			wrong.Task.TargetChangedFiles = []string{"internal/unrelated.go"}
		case "wrong-hash":
			wrong.ReconciliationHash = strings.Repeat("a", 64)
		}
		if _, err := ProposeIntake(db, wrong); err == nil {
			t.Fatal("unsafe successor proposal accepted", change)
		}
	}
	proposal, err := ProposeIntake(db, proposalRequest)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := IntakeHash(proposal)
	target, _ := core.ObserveFeedbackTarget(cancelled)
	_, _, _, selection := executionConfigurationFixture(t)
	selection.Config.Execution.ApprovalRef = "successor-configuration"
	confirmation := IntakeConfirmation{TaskID: successor.ID, ExpectedHash: hash, Actor: "human", Source: "human:new-contract",
		Reason: "Approve the input origin and new contract, not execution", Selection: selection.Config,
		ReplacesFlow: flow.ID, Target: &target, ReconciliationHash: report.Hash}
	sibling := proposalRequest
	sibling.Task.ID = "competing-successor"
	siblingProposal, err := ProposeIntake(db, sibling)
	if err != nil {
		t.Fatal(err)
	}
	siblingHash, _ := IntakeHash(siblingProposal)
	staleTarget := confirmation
	wrongTarget := target
	wrongTarget.UpdatedAt = wrongTarget.UpdatedAt.Add(1)
	staleTarget.Target = &wrongTarget
	if _, err := ConfirmIntake(db, staleTarget); err == nil {
		t.Fatal("stale cancelled target accepted")
	}
	wrong := confirmation
	wrong.ReconciliationHash = ""
	if _, err := ConfirmIntake(db, wrong); err == nil {
		t.Fatal("ordinary cancellation used as successor authority")
	}
	if err := os.WriteFile(filepath.Join(successor.Worktree, "internal/input.go"), []byte("drift"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfirmIntake(db, confirmation); err == nil {
		t.Fatal("changed carried input accepted")
	}
	if err := os.WriteFile(filepath.Join(successor.Worktree, "internal/input.go"), []byte("prior partial input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	approved, err := ConfirmIntake(db, confirmation)
	if err != nil || approved.Status != core.Ready {
		t.Fatal("exact reconciled successor rejected", err)
	}
	if _, err := ConfirmIntake(db, confirmation); err != nil {
		t.Fatal("exact confirmation replay rejected", err)
	}
	second := confirmation
	second.TaskID, second.ExpectedHash = sibling.Task.ID, siblingHash
	if _, err := ConfirmIntake(db, second); err == nil {
		t.Fatal("same reconciliation authorized a competing successor")
	}
	final, _ := db.LoadWorkspace()
	saved, _ = final.Graph.ReviewFlow(flow.ID)
	if !reflect.DeepEqual(saved, flow) || len(final.Graph.Dispatches) != len(before.Graph.Dispatches) ||
		len(final.Records.Events) != 0 {
		t.Fatal("successor rewrote old stop or issued execution")
	}
}

func TestStoppedDispatchReconciliationRejectsInsufficientEvidence(t *testing.T) {
	db, _, flow, request := stoppedInputFixture(t)
	state, _ := db.LoadWorkspace()
	for _, change := range []string{"missing-evidence", "unconfirmed-execution", "unknown-delivery-not-accepted", "running", "session", "runtime-receipt", "started", "start-sha", "usage", "acknowledged", "runner", "another-dispatch", "no-input", "not-first", "ledger-mismatch", "task-input-drift", "unknown-stop-schema", "changed-tree"} {
		t.Run(change, func(t *testing.T) {
			var snapshot store.Workspace
			if err := core.DecodeGraphInput(strings.NewReader(string(mustFlowJSON(t, state))), &snapshot); err != nil {
				t.Fatal(err)
			}

			req := request
			d := &snapshot.Graph.Dispatches[0]
			f := &snapshot.Graph.ReviewFlows[0]
			switch change {
			case "missing-evidence":
				req.Evidence = nil
			case "unconfirmed-execution":
				req.NoActiveExecutionConfirmed = false
			case "unknown-delivery-not-accepted":
				req.AcceptDeliveryUncertainty = false
			case "running":
				f.Status, f.StopCode, f.Reason = "RUNNING", "", ""
			case "session":
				d.RuntimeSessionID = "unknown-session"
			case "runtime-receipt":
				snapshot.Records.Events = []store.RuntimeReceipt{{DispatchID: d.ID}}
			case "started":
				d.ExecutionStarted = true
			case "start-sha":
				d.ExecutionStartSHA = d.ExpectedStartSHA()
			case "usage":
				d.RuntimeUsage = append(d.RuntimeUsage, core.DispatchUsage{})
			case "acknowledged":
				d.Acknowledged = true
			case "runner":
				f.RunnerID = "unreleased-owner"
			case "another-dispatch":
				snapshot.Ledger.Dispatches = append(snapshot.Ledger.Dispatches, core.Dispatch{ID: "other", Active: true})
			case "no-input":
				f.Contract.TargetTreeSHA = ""
			case "not-first":
				f.DispatchIDs = append(f.DispatchIDs, "other")
			case "ledger-mismatch":
				snapshot.Ledger.Dispatches = nil
			case "task-input-drift":
				for i := range snapshot.Graph.Tasks {
					if snapshot.Graph.Tasks[i].ID == flow.TaskID {
						snapshot.Graph.Tasks[i].TargetTreeSHA = strings.Repeat("f", 40)
					}
				}
			case "unknown-stop-schema":
				f.StopCode = "UNKNOWN_SCHEMA_VALUE"
			case "changed-tree":
				if err := os.WriteFile(filepath.Join(flow.Contract.Worktree, "internal/input.go"), []byte("drift"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := previewDispatchReconciliation(snapshot, req); err == nil {
				t.Fatal("unsafe reconciliation accepted", change)
			}
		})
	}
}

func TestStoppedFirstCoderReconciliationDoesNotDependOnStopCode(t *testing.T) {
	for _, stopCode := range []string{"SAFETY_STOP", "BUDGET_STOP", "BUDGET_LIMIT", "RECONTRACT"} {
		for _, baselineOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/baselineOnly=%t", stopCode, baselineOnly), func(t *testing.T) {
				var db *store.Store
				var flow core.ReviewFlow
				var request DispatchReconciliationRequest
				if baselineOnly {
					var ledger string
					var start ReviewFlowRequest
					db, ledger, start = reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
					var err error
					flow, err = StartReviewFlow(db, ledger, start)
					if err != nil {
						t.Fatal(err)
					}

					request = DispatchReconciliationRequest{
						FlowID: flow.ID, DispatchID: flow.LastDispatchID(), Actor: "human", Source: "human:reconcile",
						Reason: "Cancel without asserting non-delivery", Evidence: []string{"synthetic process quiescence"},
						NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
					}
				} else {
					db, _, flow, request = stoppedInputFixture(t)
				}
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					if !flow.HandoffHeld() {
						if err := holdReviewFlowForStop(&state.Graph, &flow, "fixed deadline elapsed"); err != nil {
							return err
						}
					}
					flow.Status, flow.StopCode, flow.Reason = "STOPPED", stopCode, "fixed deadline elapsed"
					flow.StartedAt = time.Now().UTC().Add(-2 * time.Hour)
					flow.Deadline = flow.StartedAt.Add(time.Hour)
					storeFlow(state, flow)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				before, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				report, err := ReconcileStoppedDispatch(db, request, true)
				if err != nil || report.UsageStatus != "unavailable" || report.DeliveryStatus != "unknown" {
					t.Fatalf("preview: %+v, %v", report, err)
				}
				afterPreview, _ := db.LoadWorkspace()
				if !reflect.DeepEqual(before, afterPreview) {
					t.Fatal("preview changed saved state")
				}
				request.ExpectedHash = report.Hash
				if _, err := ReconcileStoppedDispatch(db, request, false); err != nil {
					t.Fatal(err)
				}
				after, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				saved, _ := after.Graph.ReviewFlow(flow.ID)
				dispatch, _ := after.Graph.GraphDispatch(request.DispatchID)
				task, _ := after.Graph.GraphTask(flow.TaskID)
				if !reflect.DeepEqual(saved, flow) || task.Status != core.Blocked ||
					dispatch.Status != core.DispatchCancelled || dispatch.ExecutionStarted ||
					dispatch.Acknowledged || dispatch.RuntimeSessionID != "" || len(dispatch.RuntimeUsage) != 0 ||
					dispatch.Result.OutputTreeSHA != "" || len(after.Records.Events) != len(before.Records.Events) {
					t.Fatal("cancellation changed authorization, deadline, usage or execution evidence")
				}
				if err := after.Records.VerifyFlowAudits(after.Graph); err != nil {
					t.Fatal(err)
				}
				if err := after.Graph.CheckLedger(after.Ledger); err != nil {
					t.Fatal(err)
				}
				if baselineOnly {
					successor := flow.Contract
					successor.Worktree, successor.Branch = filepath.Join(t.TempDir(), "successor"), "work/baseline-successor"
					gitTest(t, flow.Contract.Worktree, "worktree", "add", "-b", successor.Branch, successor.Worktree, successor.BaselineSHA)
					original := before.Records.RestoredDispatch(before.Graph.Dispatches[0])
					if err := gitproof.VerifyApprovedInputSuccessor(original, successor); err != nil {
						t.Fatal("unchanged baseline-only successor rejected:", err)
					}
					if err := os.WriteFile(filepath.Join(successor.Worktree, "README.md"), []byte("drift"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := gitproof.VerifyApprovedInputSuccessor(original, successor); err == nil {
						t.Fatal("changed baseline-only successor accepted")
					}
				}
			})
		}
	}
}

func TestStoppedReviewerCancellationDoesNotRequireListedStopCode(t *testing.T) {
	for _, artifact := range []bool{false, true} {
		t.Run(fmt.Sprintf("artifact=%t", artifact), func(t *testing.T) {
			var db *store.Store
			var flow core.ReviewFlow
			var request DispatchReconciliationRequest
			if artifact {
				var ledger string
				var start ReviewFlowRequest
				db, ledger, start, _ = artifactFixture(t, []string{"test -s internal/input-00.txt"}, false)
				var err error
				flow, err = StartReviewFlow(db, ledger, start)
				if err != nil {
					t.Fatal(err)
				}
				flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, start.Configuration)
				if err != nil {
					t.Fatal(err)
				}
				request = DispatchReconciliationRequest{
					FlowID: flow.ID, DispatchID: flow.LastDispatchID(), Actor: "human", Source: "human:reconcile",
					Reason: "Cancel without asserting non-delivery", Evidence: []string{"synthetic process quiescence"},
					NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
				}
			} else {
				db, _, flow, _, request = stoppedNormalReviewer(t, false, true)
			}
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				if !flow.HandoffHeld() {
					if err := holdReviewFlowForStop(&state.Graph, &flow, "separate successor requested"); err != nil {
						return err
					}
				}
				flow.Status, flow.StopCode, flow.Reason = "STOPPED", "RECONTRACT", "separate successor requested"
				storeFlow(state, flow)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, _ := db.LoadWorkspace()
			preview, err := ReconcileStoppedDispatch(db, request, true)
			if err != nil {
				t.Fatal(err)
			}
			request.ExpectedHash = preview.Hash
			if _, err := ReconcileStoppedDispatch(db, request, false); err != nil {
				t.Fatal(err)
			}
			after, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			saved, _ := after.Graph.ReviewFlow(flow.ID)
			if !reflect.DeepEqual(flow, saved) || !reflect.DeepEqual(before.Records.Events, after.Records.Events) {
				t.Fatal("cancellation rewrote Flow or execution receipts")
			}
			if err := after.Records.VerifyFlowAudits(after.Graph); err != nil {
				t.Fatal(err)
			}
			if artifact {
				next := flow.Contract
				next.ID, next.Supersedes, next.CreatedFrom = "next-review", flow.TaskID, "flow:"+flow.ID
				next.TargetSHA, next.TargetTreeSHA = flow.Artifact.Target.CommitSHA, flow.Artifact.Target.TreeSHA
				next.TargetChangedFiles = flow.Artifact.Target.ChangedFiles
				if _, err := validatedArtifactSource(after.Graph, next, ReviewFlowRequest{
					ArtifactSource: flow.Artifact.Target.Source, Configuration: flow.Artifact.Configuration,
				}); err != nil {
					t.Fatal("reconciled RECONTRACT input rejected by downstream validation reuse:", err)
				}
			}
		})
	}
}
