package controller

import (
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestClarificationScopeHashSurvivesSourceDispatchCompletion(t *testing.T) {
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

	executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
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

	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	successor, ok := func() (core.GraphDispatch, bool) {
		state, loadErr := db.LoadWorkspace()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return state.Graph.GraphDispatch(flow.LastDispatchID())
	}()
	if !ok || successor.Recipient != "CODER" {
		t.Fatal("REQUEST_CHANGES did not issue the successor Coder Dispatch")
	}
	foundQuestion := false
	for _, decision := range successor.Instruction.Decisions {
		if decision.ID == "clarification-question:"+input.ID {
			foundQuestion = strings.Contains(decision.Text, input.Question) &&
				strings.Contains(decision.Text, input.DecisionImpact) &&
				strings.Contains(decision.Text, input.StopCondition)
		}
	}
	if !foundQuestion {
		t.Fatal("successor Coder Dispatch does not carry the saved Reviewer question")
	}
}
