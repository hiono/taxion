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
	"github.com/hiono/taxion/internal/store"
)

func TestExecutedArtifactReviewerContinuesSavedWorkWithinOriginalDeadline(t *testing.T) {
	testExecutedReviewContinuation(t, false)
}

func TestExecutedArtifactReviewerRecontractsExpiredDeadlineWithoutDiscardingWork(t *testing.T) {
	testExecutedReviewContinuation(t, true)
}

func testExecutedReviewContinuation(t *testing.T, expired bool) {
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
	if expired {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			// A historical stopped fixture, not a production recovery mutation.
			state.Graph.ReviewFlows[0].StartedAt = time.Now().UTC().Add(-2 * time.Minute)
			state.Graph.ReviewFlows[0].Deadline = time.Now().UTC().Add(-time.Minute)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		state, _ := db.LoadWorkspace()
		old, _ = state.Graph.ReviewFlow(old.ID)
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
	if expired {
		request.Deadline = time.Now().UTC().Add(5 * time.Minute)
		if _, err := RecoverArtifactReview(db, ledger, request, true); err == nil {
			t.Fatal("expired source accepted without explicit deadline recontract")
		}
		request.DeadlineRecontract = &store.ReviewDeadlineRecontract{
			PreviousDeadline: old.Deadline, Deadline: request.Deadline,
			Reason: "Authorize a new review window after the preserved stop expired; retain cumulative spending",
		}
	}
	if _, err := ReconcileStoppedDispatch(db, request.Reconciliation, true); err == nil {
		t.Fatal("unstarted reconciliation accepted executed Reviewer")
	}
	preview, err := RecoverArtifactReview(db, ledger, request, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, badKind := range []string{"later-deadline", "missing-quiescence", "wrong-mode", "missing-stop", "mutated-receipt", "wrong-previous-deadline", "missing-change-reason"} {
		t.Run(badKind, func(t *testing.T) {
			bad := request
			if request.DeadlineRecontract != nil {
				copy := *request.DeadlineRecontract
				bad.DeadlineRecontract = &copy
			}
			switch badKind {
			case "later-deadline":
				bad.Deadline = old.Deadline.Add(time.Second)
			case "missing-quiescence":
				bad.Reconciliation.NoActiveExecutionConfirmed = false
			case "wrong-mode":
				bad.ContinueStoppedExecution = false
			case "wrong-previous-deadline", "missing-change-reason":
				if bad.DeadlineRecontract == nil {
					t.Skip("only recontract has change metadata")
				}
				if badKind == "wrong-previous-deadline" {
					bad.DeadlineRecontract.PreviousDeadline = old.Deadline.Add(-time.Second)
				} else {
					bad.DeadlineRecontract.Reason = ""
				}
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
	if expired {
		changed := request
		copy := *request.DeadlineRecontract
		changed.DeadlineRecontract = &copy
		copy.Reason += "; changed scope of deadline decision"
		if _, err := RecoverArtifactReview(db, ledger, changed, false); err == nil {
			t.Fatal("changed deadline decision reused old approval")
		}
	}
	applied, err := RecoverArtifactReview(db, ledger, request, false)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Flow.Deadline.After(request.Deadline) || !expired && applied.Flow.Deadline.After(old.Deadline) {
		t.Fatal("continuation exceeded approved deadline")
	}
	state, _ := db.LoadWorkspace()
	preserved, _ := state.Graph.GraphDispatch(dispatch.ID)
	preservedFlow, _ := state.Graph.ReviewFlow(old.ID)
	if !reflect.DeepEqual(dispatch, preserved) || !reflect.DeepEqual(old, preservedFlow) {
		t.Fatal("continuation rewrote old deadline/session/result/usage")
	}
	if err := state.Records.VerifyDispatchReconciliations(state.Graph); err != nil {
		t.Fatal(err)
	}
	if expired {
		audit := &state.Records.DispatchReconciliations[0]
		if !reflect.DeepEqual(audit.DeadlineRecontract, request.DeadlineRecontract) {
			t.Fatal("deadline recontract missing from append-only audit")
		}
		audit.DeadlineRecontract = nil
		if state.Records.VerifyDispatchReconciliations(state.Graph) == nil {
			t.Fatal("expired executed continuation audit accepted without new deadline consent")
		}
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
