package controller

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/store"
)

func TestOperationBlocksUnrelatedWorkflowAndOnlyAdmitsOwnedContinuation(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	lease, err := db.LockOperation()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	operation, err := lease.Claim(store.Operation{
		Version: 1, ID: "operation", Kind: "request", TaskID: request.TaskID,
		State: "WAITING_FOR_ANSWER", Phase: "PLANNING", Owner: "owner",
		Deadline: time.Now().UTC().Add(time.Hour), MaxNanoAIU: 100, PlanningUsageState: "complete",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := db.LoadWorkspace()
	if _, err := StartReviewFlow(db, ledger, request); err == nil {
		t.Fatal("an unanswered question allowed a new workflow")
	}
	if _, err := StartReviewFlowContext(WithOperation(t.Context(), operation), db, ledger, request); err == nil {
		t.Fatal("an owner bypassed the answer-wait state")
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected workflow changed the saved contract")
	}
	operation.State, operation.Phase = "RUNNING", "PREFLIGHT"
	if err := db.SaveOperation(operation); err != nil {
		t.Fatal(err)
	}
	flow, err := StartReviewFlowContext(WithOperation(t.Context(), operation), db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	competing := request
	competing.ID, competing.TaskID = "other-flow", "other-task"
	if _, err := StartReviewFlow(db, ledger, competing); err == nil {
		t.Fatal("another Task started while the operation was active")
	}
	operation.FlowID = flow.ID
	if err := db.SaveOperation(operation); err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); err == nil {
		t.Fatal("a separate caller advanced an owned operation")
	}
	if err := checkOperation(&after, context.Background(), "other", ""); err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatal("unrelated execution was admitted", err)
	}
}

func TestOperationAdmissionRaceStopsUnboundRuntimeBeforeSend(t *testing.T) {
	db, dispatchID, worker, _ := executionConfigurationFixture(t)
	lease, err := db.LockOperation()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	worker.onStart = func() {
		_, err := lease.Claim(store.Operation{
			Version: 1, ID: "new-operation", Kind: "request", TaskID: "planning",
			State: "WAITING_FOR_ANSWER", Phase: "PLANNING", Owner: "new-owner",
			Deadline: time.Now().UTC().Add(time.Hour), MaxNanoAIU: 100, PlanningUsageState: "complete",
		}, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), dispatchID); err == nil {
		t.Fatal("a late admission was ignored while binding the runtime")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := state.Graph.GraphDispatch(dispatchID)
	if dispatch.RuntimeSessionID != "" || dispatch.ExecutionStarted || len(worker.sends) != 0 || worker.stops != 1 {
		t.Fatal("the raced session was bound, sent instructions or left unclosed")
	}
}

func TestRunningFlowBlocksAnotherTaskBeforeIssuingDispatch(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	if _, err := StartReviewFlow(db, ledger, request); err != nil {
		t.Fatal(err)
	}
	before, _ := db.LoadWorkspace()
	request.ID, request.TaskID = "another-flow", "another-task"
	if _, err := StartReviewFlow(db, ledger, request); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatal("another workflow was admitted", err)
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("competing workflow left a Dispatch or changed decisions")
	}
}
