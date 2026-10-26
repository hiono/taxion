package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func TestProviderBudgetNeverReadsQuotaForDispatchOrIsolatedTurns(t *testing.T) {
	policy := budgetPolicy(t, "provider")
	db, id, base, selection := executionConfigurationFixture(t, policy)
	defer db.Close()
	worker := &quotaRuntime{configuredRuntime: base, onRead: func() {
		t.Fatal("declared provider billing unexpectedly required a quota RPC")
	}}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	if worker.reads != 0 || len(worker.sends) != 1 ||
		!strings.Contains(worker.sends[0], `"billingVerification":"human-declaration"`) {
		t.Fatal("provider declaration was not visible or caused a quota read")
	}
	observation, text, err := isolatedBudgetGuidance(t.Context(), worker, policy, selection.Config)
	if err != nil || observation.Account != nil || worker.reads != 0 ||
		!strings.Contains(text, `"contract":"provider-limit"`) {
		t.Fatal("isolated turn required account quota or hid the billing declaration", err)
	}
	if current, err := monitorIsolatedQuota(t.Context(), worker, selection.Config, observation, time.Now()); err != nil || current != nil {
		t.Fatal("provider-only isolated turn enabled quota monitoring", err)
	}
	if _, err := CheckDispatchConfiguration(db, id, selection.Config); err != nil {
		t.Fatal("unchanged billing declaration rejected", err)
	}
	for _, settings := range []core.BillingSettings{
		{Contract: "metered"}, {Overage: "allowed"}, {AutoRecharge: "enabled"}, {PaidFallback: "unknown"},
	} {
		changed := selection.Config
		changed.Billing = settings
		if _, err := CheckDispatchConfiguration(db, id, changed); err == nil {
			t.Fatal("changed billing settings reused Dispatch approval", settings)
		}
	}
	selection.Config.Billing.Contract = "metered"
	if _, _, err := isolatedBudgetGuidance(t.Context(), worker, policy, selection.Config); err == nil {
		t.Fatal("isolated turn accepted a contradictory billing declaration")
	}
}

func TestProviderOnlyFlowRetainsLimitsAndUnknownUsage(t *testing.T) {
	for _, ceiling := range []float64{0, 1} {
		db, ledger, request := reviewFlowFixtureWithPolicy(t, "", budgetPolicy(t, "provider"))
		request.MaxNanoAIU = ceiling
		flow, err := StartReviewFlow(db, ledger, request)
		if err != nil {
			t.Fatal(err)
		}
		state, err := db.LoadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		if (flow.Budget.BudgetSource == core.ProviderStopBudgetSource) != (ceiling == 0) || flow.Budget.AccountQuotaOnly || state.Graph.Validate() != nil {
			t.Fatal("provider budget mode was not explicitly bound")
		}
		state.Graph.Dispatches[0].RuntimeSessionID = "fixture-session"
		state.Graph.Dispatches[0].Status = core.DispatchSucceeded
		report := taskBudgetReport(state.Graph, flow, flow.StartedAt)
		if report.CreditsStatus != "unknown" || report.NanoAIURemaining != 0 {
			t.Fatal("unknown usage was fabricated")
		}
		if (activeBudgetStop(report) == "") != (ceiling == 0) ||
			(flowBudgetStop(report, "REVIEWER") == "") != (ceiling == 0) ||
			(requireSessionBudget(state.Graph, flow, "REVIEWER") == nil) != (ceiling == 0) {
			t.Fatal("explicit native ceiling or provider-only mode was ignored", report)
		}
		report.ElapsedRemainingMillis = 0
		if activeBudgetStop(report) != "elapsed budget reached" {
			t.Fatal("provider mode disabled deadline enforcement")
		}
		report.ElapsedRemainingMillis, report.DispatchesRemaining = 1, 0
		if flowBudgetStop(report, "REVIEWER") != "Task Dispatch ceiling reached" {
			t.Fatal("provider mode disabled Dispatch ceilings")
		}
		if ceiling == 0 {
			state.Graph.ReviewFlows[0].Budget.BudgetSource = ""
			if state.Graph.Validate() == nil {
				t.Fatal("omitted native ceiling lost its provider binding")
			}
			state.Graph.ReviewFlows[0].Budget = core.FlowBudget{BudgetSource: "unknown"}
			if state.Graph.Validate() == nil {
				t.Fatal("unknown budget source bypassed legacy empty-budget validation")
			}
		}
	}
}
