package controller

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestRecoveryRequiresStoppedExecutionAndExactOwner(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].RunnerID = "interrupted-runner"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recovery := RecoveryRequest{
		FlowID: flow.ID, ExpectedRunnerID: "interrupted-runner", PreviousProcessStopped: true,
		Actor: "human", Reason: "Original process ended; reconcile recorded execution",
		Evidence: []string{"process-stop:checked"},
	}
	if _, err := RecoverReviewFlow(db, recovery); err == nil {
		t.Fatal("recovery released owner of an active Dispatch")
	}
	dispatchID := flow.DispatchIDs[0]
	control, err := New(db, &fakeRuntime{role: "CODER", hash: "test", worktree: flow.Contract.Worktree})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), dispatchID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), dispatchID, "execute"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.TransitionDispatch(dispatchID, core.DispatchCancelRequested,
			"runtime", "cancel requested before process exit", false, false, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	worker := &fakeRuntime{role: "CODER", hash: "test", worktree: flow.Contract.Worktree}
	control, err = New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverReviewFlow(db, recovery); err == nil {
		t.Fatal("CANCEL_REQUESTED was accepted as confirmed stop")
	}
	if err := control.Resume(t.Context(), dispatchID); err == nil {
		t.Fatal("cancel request authorized normal execution resume")
	}
	if err := control.Stop(t.Context(), dispatchID); err != nil || worker.resumes != 1 || worker.stops != 1 {
		t.Fatalf("restart did not attach solely for safe stop: %v %+v", err, worker)
	}
	for _, change := range []func(*RecoveryRequest){
		func(r *RecoveryRequest) { r.ExpectedRunnerID = "stale-runner" },
		func(r *RecoveryRequest) { r.PreviousProcessStopped = false },
		func(r *RecoveryRequest) { r.Actor = "" },
		func(r *RecoveryRequest) { r.Reason = "" },
		func(r *RecoveryRequest) { r.Evidence = nil },
		func(r *RecoveryRequest) { r.Evidence = []string{"same", "same"} },
		func(r *RecoveryRequest) { r.Evidence = []string{" "} },
	} {
		invalid := recovery
		change(&invalid)
		if _, err := RecoverReviewFlow(db, invalid); err == nil {
			t.Fatal("invalid recovery confirmation accepted")
		}
	}
	recovery.Evidence = append(recovery.Evidence, "runtime-stop:sdk-session")
	recovered, err := RecoverReviewFlow(db, recovery)
	if err != nil || recovered.RunnerID != "" || recovered.Status != "RUNNING" ||
		len(recovered.Recoveries) != 1 || recovered.Recoveries[0].RunnerID != recovery.ExpectedRunnerID ||
		!reflect.DeepEqual(recovered.Recoveries[0].Evidence, recovery.Evidence) {
		t.Fatalf("ownership not recovered with provenance: %+v %v", recovered, err)
	}
	recovery.Evidence[0] = "mutated"
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := state.Graph.ReviewFlow(flow.ID)
	if saved.Recoveries[0].Evidence[0] != "process-stop:checked" {
		t.Fatal("saved recovery evidence aliased the input")
	}
	saved.Recoveries[0].Evidence[0] = "mutated snapshot"
	again, _ := state.Graph.ReviewFlow(flow.ID)
	if again.Recoveries[0].Evidence[0] != "process-stop:checked" {
		t.Fatal("recovery snapshot aliased the graph")
	}
	if _, err := RecoverReviewFlow(db, recovery); err == nil {
		t.Fatal("stale recovery was replayed")
	}
	// Releasing ownership does not authorize retry of cancelled work.
	stopped, err := AdvanceReviewFlow(db, ledger, flow.ID)
	if !errors.Is(err, ErrFlowStopped) || stopped.Status != "STOPPED" || len(stopped.DispatchIDs) != 1 {
		t.Fatalf("cancelled execution restarted after recovery: %+v %v", stopped, err)
	}
}

func TestRecoveryPreservesSuccessfulResultForHandoff(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	result := executeFlowDispatch(t, db, flow, "", true)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].RunnerID = "interrupted-after-result"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverReviewFlow(db, RecoveryRequest{
		FlowID: flow.ID, ExpectedRunnerID: "interrupted-after-result", PreviousProcessStopped: true,
		Actor: "human", Reason: "Result persisted before process exit",
		Evidence: []string{"process-stop:checked", "dispatch:" + result.ID},
	}); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 || len(flow.Recoveries) != 1 {
		t.Fatalf("successful handoff lost after recovery: %+v %v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	review, _ := state.Graph.GraphDispatch(flow.DispatchIDs[1])
	if review.Recipient != "REVIEWER" || review.Instruction.TargetSHA != result.Result.OutputSHA {
		t.Fatal("recovery bypassed exact-SHA independent review")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].Recoveries[0].Evidence = nil
		return nil
	}); !errors.Is(err, core.ErrGraphRejected) {
		t.Fatal("invalid recovery history persisted", err)
	}
}
