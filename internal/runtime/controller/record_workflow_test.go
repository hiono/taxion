package controller

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestRecoveryFindingUncertainReportAndVerifiedClose(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	objection := executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	body, _ := json.Marshal(objection.Result)
	finding := store.FailureRecord{
		ID: "review-correction", TaskID: objection.TaskID, DispatchID: objection.ID,
		ResultHash: receiptHash(string(body)), Cause: "acceptance test missing",
		Correction:         "add acceptance test and independently review repaired SHA",
		LessonCandidate:    "include acceptance evidence in Coder handoff",
		ValidationEvidence: objection.Result.Evidence, Actor: "human", DecisionSource: "human:review-disposition",
	}
	if _, err := RecordFailureAnalysis(db, finding); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatal(flow, err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].RunnerID = "lost-after-completion"
		return nil
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
	if _, err := RecoverReviewFlow(db, RecoveryRequest{
		FlowID: flow.ID, ExpectedRunnerID: "lost-after-completion", PreviousProcessStopped: true,
		Actor: "human", Reason: "reconcile completed checkpoint", Evidence: []string{"process-stop:verified"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordFailureAnalysis(db, finding); err != nil {
		t.Fatal("sourced decision was not replayable after restart", err)
	}
	report := publicationProposal()
	report.FlowID = flow.ID
	hash, _ := PublicationHash(report)
	scanner := publicationScanner(t)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{report, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	remote := &publicationFixture{failure: true}
	saved, err := DeliverPublication(t.Context(), db, report.ID, scanner, remote)
	if err == nil || saved.Status != "UNKNOWN" {
		t.Fatal(saved, err)
	}
	closeRequest := report
	closeRequest.ID, closeRequest.Action, closeRequest.Body = "verified-close", "CLOSE", ""
	closeHash, _ := PublicationHash(closeRequest)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{closeRequest, closeHash}, scanner); err == nil {
		t.Fatal("unsynchronized report allowed close")
	}
	if _, err := ReconcilePublication(t.Context(), db, saved.ID, saved.Claim, true, remote); err != nil {
		t.Fatal(err)
	}
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{closeRequest, closeHash}, scanner); err != nil {
		t.Fatal(err)
	}
	closer := &publicationFixture{}
	closed, err := DeliverPublication(t.Context(), db, closeRequest.ID, scanner, closer)
	if err != nil || closed.Status != "SENT" || remote.sends != 1 || closer.sends != 1 {
		t.Fatal(closed, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.Findings) != 1 || len(state.Records.Updates) != 2 {
		t.Fatal("persistent execution record", err)
	}
	task, _ := state.Graph.GraphTask(request.TaskID)
	if task.Status != core.Completed || len(task.DependsOn) != 0 {
		t.Fatal("external recording rewrote the execution contract")
	}
}
