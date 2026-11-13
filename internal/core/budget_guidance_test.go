package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBudgetGuidanceSelectionApprovalAndLegacyShape(t *testing.T) {
	for _, test := range []struct{ config, override, mode, source string }{
		{"", "", "provider", "default"}, {"account", "", "account", "configuration"},
		{"account", "off", "off", "request"}, {"off", "account", "account", "request"},
	} {
		policy, err := NewBudgetGuidance(test.config, test.override)
		if err != nil || policy.Mode != test.mode || policy.Source != test.source ||
			policy.AccountReadApproved != (test.mode == "account") {
			t.Fatal("wrong policy resolution", policy, err)
		}
		if (policy.AccountAllocation == "reset-daily") != (test.mode == "account") {
			t.Fatal("new account proposals do not use the residual floor", policy)
		}
		copy := CloneBudgetGuidance(policy)
		copy.AllowedModes[0] = "invalid"
		copy.Billing.Overage = "allowed"
		if policy.Validate() != nil {
			t.Fatal("mutable policy alias")
		}
		if policy.Billing.Overage != "denied" {
			t.Fatal("billing declaration shares a mutable clone")
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

func TestProviderBillingSelectionRequiresCompatibleDeclaration(t *testing.T) {
	for _, contract := range []string{"", "provider-limit", "metered", "unknown"} {
		selected := BillingSettings{Contract: contract}
		policy, err := NewBillingBudgetGuidance("", "", selected)
		if err != nil {
			t.Fatal(err)
		}
		wantProvider := contract == "" || contract == "provider-limit"
		if ProviderBudget(policy) != wantProvider || policy.AccountReadApproved ||
			!wantProvider && policy.Mode != "flow" {
			t.Fatal("billing declaration selected unsafe guidance", policy)
		}
		_, err = NewBillingBudgetGuidance("", "provider", selected)
		if (err == nil) != wantProvider {
			t.Fatal("conflicting billing declaration accepted", selected, err)
		}
	}
	for _, policy := range []*BudgetGuidancePolicy{
		{Mode: "provider", Source: "request", AllowedModes: []string{"provider"}},
		{Mode: "provider", Source: "request", AllowedModes: []string{"provider"}, Billing: &BillingSettings{Contract: "metered"}},
		{Mode: "flow", Source: "request", AllowedModes: []string{"flow"}, Billing: &BillingSettings{Contract: "invalid"}},
	} {
		if policy.Validate() == nil {
			t.Fatal("unbound billing declaration accepted")
		}
	}
	legacy := &BudgetGuidancePolicy{Mode: "account", Source: "request", AllowedModes: []string{"account"}, AccountReadApproved: true}
	before, _ := json.Marshal(legacy)
	if legacy.Validate() != nil || ProviderBudget(legacy) || ExternalBudget(legacy) {
		t.Fatal("legacy account policy acquired provider-only authority")
	}
	after, _ := json.Marshal(legacy)
	if string(before) != string(after) || strings.Contains(string(after), "billing") {
		t.Fatal("legacy policy encoding changed")
	}
}

func TestResetDailyAllocationRequiresExplicitContract(t *testing.T) {
	legacy := &BudgetGuidancePolicy{Mode: "account", Source: "request",
		AllowedModes: []string{"off", "flow", "account"}, AccountReadApproved: true}
	if legacy.AccountAllocation != "" {
		t.Fatal("existing account selection changed its approved allocation")
	}
	task := graphTask("allocation")
	task.BudgetGuidance = legacy
	graph := trackedGraph(t, task)
	saved, _ := graph.GraphTask(task.ID)
	saved.BudgetGuidance = CloneBudgetGuidance(saved.BudgetGuidance)
	saved.BudgetGuidance.AccountAllocation = "reset-daily"
	if err := saved.BudgetGuidance.Validate(); err != nil {
		t.Fatal(err)
	}
	if graph.Track(saved, "human", "change allocation without approval") == nil {
		t.Fatal("new allocation reused old authorization")
	}
	for _, policy := range []*BudgetGuidancePolicy{
		{Mode: "flow", Source: "request", AllowedModes: []string{"flow"}, AccountAllocation: "reset-daily"},
		{Mode: "account", Source: "request", AllowedModes: []string{"account"}, AccountReadApproved: true, AccountAllocation: "auto"},
	} {
		if policy.Validate() == nil {
			t.Fatal("invalid allocation accepted")
		}
	}
}

func TestBudgetGuidanceRequiresReapprovalAndImmutableDispatch(t *testing.T) {
	task := graphTask("guidance")
	task.BudgetGuidance, _ = NewBudgetGuidance("", "flow")
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
