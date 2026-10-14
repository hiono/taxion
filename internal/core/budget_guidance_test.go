package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBudgetGuidanceSelectionApprovalAndLegacyShape(t *testing.T) {
	for _, test := range []struct{ config, override, mode, source string }{
		{"", "", "flow", "default"}, {"account", "", "account", "configuration"},
		{"account", "off", "off", "request"}, {"off", "account", "account", "request"},
	} {
		policy, err := NewBudgetGuidance(test.config, test.override)
		if err != nil || policy.Mode != test.mode || policy.Source != test.source ||
			policy.AccountReadApproved != (test.mode == "account") {
			t.Fatal("wrong policy resolution", policy, err)
		}
		copy := CloneBudgetGuidance(policy)
		copy.AllowedModes[0] = "invalid"
		if policy.Validate() != nil {
			t.Fatal("mutable policy alias")
		}
		policy.AccountReadApproved = !policy.AccountReadApproved
		if policy.Validate() == nil {
			t.Fatal("account read was not bound to the allowed modes")
		}
	}
	if _, err := NewBudgetGuidance("", "invalid"); err == nil {
		t.Fatal("invalid explicit override was ignored")
	}
	data, err := json.Marshal(GraphTask{})
	if err != nil || strings.Contains(string(data), "budgetGuidance") {
		t.Fatal("legacy contracts acquired a new field")
	}
}

func TestBudgetGuidanceRequiresReapprovalAndImmutableDispatch(t *testing.T) {
	task := graphTask("guidance")
	task.BudgetGuidance, _ = NewBudgetGuidance("", "")
	graph := trackedGraph(t, task)
	saved, _ := graph.GraphTask(task.ID)
	saved.BudgetGuidance = nil
	if err := graph.Track(saved, "human", "edit without changing guidance"); err != nil {
		t.Fatal(err)
	}
	saved, _ = graph.GraphTask(task.ID)
	if saved.BudgetGuidance == nil || saved.BudgetGuidance.Mode != "flow" {
		t.Fatal("omission removed an approved policy")
	}
	saved.BudgetGuidance, _ = NewBudgetGuidance("", "account")
	if graph.Track(saved, "human", "add account read") == nil {
		t.Fatal("account read reused old authorization")
	}
	saved.AuthorizationID = "decision:2"
	saved.Decisions = append(saved.Decisions, TaskDecision{
		ID: saved.AuthorizationID, Source: "human:2", Text: "approve selected-account read and guidance",
	})
	if err := graph.Track(saved, "human", "approve account guidance"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(saved.ID, Ready, "human", "ready", TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := NewGraphDispatch(graph, "guidance-dispatch", saved.ID, saved.Repo, RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderDispatchInstruction(dispatch)
	if err != nil || !strings.Contains(rendered, `"accountReadApproved":true`) {
		t.Fatal("read scope absent from immutable instruction", err)
	}
	saved.BudgetGuidance.AllowedModes[0] = "invalid"
	if dispatch.Instruction.BudgetGuidance.Validate() != nil {
		t.Fatal("Dispatch instruction shares mutable Task policy")
	}
}
