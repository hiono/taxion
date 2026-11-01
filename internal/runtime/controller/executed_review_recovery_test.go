package controller

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func TestExecutedArtifactReviewerContinuesSavedWorkWithinOriginalDeadline(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "validations")
	db, ledger, initial, selected := artifactFixture(t, []string{"printf x >> " + counter}, false)
	old, err := StartReviewFlow(db, ledger, initial)
	if err != nil {
		t.Fatal(err)
	}
	old, err = ValidateReviewFlowInput(t.Context(), db, ledger, old.ID, initial.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := prepareReview(t.Context(), db, old.LastDispatchID(), old.Contract.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindConfiguration(db, dispatch.ID, selected); err != nil {
		t.Fatal(err)
	}
	worker := &configuredRuntime{fakeRuntime: &fakeRuntime{role: "REVIEWER", worktree: dispatch.Instruction.Worktree, hash: "test"}, selection: mustFlowJSON(t, selected.Config)}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), dispatch.ID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), dispatch.ID, "execute approved review"); err != nil {
		t.Fatal(err)
	}
	const savedAnalysis = "Already examined TestClamp and the docs: bounds mismatch is in the caller; remaining review concerns boundary tests."
	if err := control.recordEvent(dispatch.ID, "test-stream", runtime.Event{Type: runtime.EventOutput, Payload: savedAnalysis, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := control.Stop(t.Context(), dispatch.ID); err != nil {
		t.Fatal(err)
	}
	old, err = AdvanceReviewFlow(db, ledger, old.ID, initial.Configuration)
	if err == nil || old.Status != "STOPPED" {
		t.Fatal("missing formal terminal stop", err)
	}
	before, _ := db.LoadWorkspace()
	dispatch, _ = before.Graph.GraphDispatch(dispatch.ID)
	if !old.StoppedExecutedArtifactReviewer(dispatch) {
		t.Fatal("fixture is not an executed stopped artifact Reviewer")
	}
	request := ReviewRecoveryRequest{
		ContinueStoppedExecution: true,
		Reconciliation: DispatchReconciliationRequest{
			FlowID: old.ID, DispatchID: dispatch.ID, Actor: "human", Source: "human:saved-continuation-approval",
			Reason:   "Continue already-approved same-candidate review within remaining cumulative budget and original deadline",
			Evidence: []string{"provider stop confirmed and no active process"}, NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
		},
		TaskID: "continued-task", FlowID: "continued-flow", ReviewRoot: filepath.Join(t.TempDir(), "next"),
		Deadline: old.Deadline, TimeoutSeconds: initial.TimeoutSeconds,
		MaxNanoAIU: initial.MaxNanoAIU, StageBudget: *initial.StageBudget,
	}
	if _, err := ReconcileStoppedDispatch(db, request.Reconciliation, true); err == nil {
		t.Fatal("unstarted reconciliation accepted executed Reviewer")
	}
	preview, err := RecoverArtifactReview(db, ledger, request, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, badKind := range []string{"later-deadline", "missing-quiescence", "wrong-mode", "missing-stop", "mutated-receipt"} {
		t.Run(badKind, func(t *testing.T) {
			bad := request
			switch badKind {
			case "later-deadline":
				bad.Deadline = old.Deadline.Add(time.Second)
			case "missing-quiescence":
				bad.Reconciliation.NoActiveExecutionConfirmed = false
			case "wrong-mode":
				bad.ContinueStoppedExecution = false
			case "missing-stop", "mutated-receipt":
				copy := dispatch
				result := *copy.Result
				copy.Result = &result
				if badKind == "missing-stop" {
					copy.Result.Evidence = []string{}
				} else {
					copy.Result.Blockers = []string{"changed after stop"}
				}
				if old.StoppedExecutedArtifactReviewer(copy) && badKind == "missing-stop" {
					t.Fatal("missing stop evidence accepted")
				}
				snapshot := before
				snapshot.Graph.Dispatches = append([]core.GraphDispatch(nil), before.Graph.Dispatches...)
				for i := range snapshot.Graph.Dispatches {
					if snapshot.Graph.Dispatches[i].ID == copy.ID {
						snapshot.Graph.Dispatches[i] = copy
					}
				}
				if _, err := previewReviewRecoverySource(snapshot, bad); err == nil {
					t.Fatal("invalid source accepted")
				}
				return
			}
			if _, err := RecoverArtifactReview(db, ledger, bad, true); err == nil {
				t.Fatal("invalid executed continuation accepted")
			}
		})
	}
	afterPreview, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, afterPreview) {
		t.Fatal("preview changed stopped execution")
	}
	request.ExpectedHash = preview.Hash
	applied, err := RecoverArtifactReview(db, ledger, request, false)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Flow.Deadline.After(old.Deadline) {
		t.Fatal("continuation extended original deadline")
	}
	state, _ := db.LoadWorkspace()
	preserved, _ := state.Graph.GraphDispatch(dispatch.ID)
	if !reflect.DeepEqual(dispatch, preserved) {
		t.Fatal("continuation rewrote old session/result/usage")
	}
	if err := state.Records.VerifyDispatchReconciliations(state.Graph); err != nil {
		t.Fatal(err)
	}
	next, err := ValidateReviewFlowInput(t.Context(), db, ledger, applied.Flow.ID, initial.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := os.ReadFile(counter); err != nil || string(count) != "x" {
		t.Fatal("executed continuation replayed the accepted input validation", err)
	}
	state, _ = db.LoadWorkspace()
	nextDispatch, _ := state.Graph.GraphDispatch(next.LastDispatchID())
	context, err := RenderDispatchContext(db, nextDispatch, true)
	if err != nil || !strings.Contains(context, savedAnalysis) {
		t.Fatal("continued Reviewer discarded saved analysis", err)
	}
	executeFlowDispatch(t, db, next, "APPROVE", true, selected)
	next, err = AdvanceReviewFlow(db, ledger, next.ID, initial.Configuration)
	if err != nil || next.Status != "COMPLETED" {
		t.Fatal("continued review failed", err)
	}
	if count, err := os.ReadFile(counter); err != nil || string(count) != "xx" {
		t.Fatal("continued review omitted result acceptance validation", err)
	}
}
