package controller

import "testing"

func TestClarificationScopeHashSurvivesSourceDispatchCompletion(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeFlowDispatch(t, db, flow, "", true); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	reviewer, ok := state.Graph.GraphDispatch(flow.LastDispatchID())
	if !ok {
		t.Fatal("Reviewer Dispatch is missing")
	}
	input := clarificationRequest(t, db, reviewer.TaskID, reviewer.ID, "REVIEWER")
	input.Category = "evidence"
	if _, err := RequestClarification(db, input); err != nil {
		t.Fatal(err)
	}
	before, err := ObserveClarification(db, reviewer.TaskID, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true); err != nil {
		t.Fatal(err)
	}
	after, err := ObserveClarification(db, reviewer.TaskID, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.ScopeHash != after.ScopeHash {
		t.Fatalf("completed source Dispatch changed the approved clarification scope: before=%s after=%s",
			before.ScopeHash, after.ScopeHash)
	}
	if before.Hash == after.Hash {
		t.Fatal("full clarification context did not retain the changed execution result")
	}
}
