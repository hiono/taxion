package controller

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestAccountQuotaOnlyFlowPreservesOtherLimitsAndUnknownTelemetry(t *testing.T) {
	policy := budgetPolicy(t, "account")
	policy.AccountAllocation = "reset-daily"
	db, ledger, request := reviewFlowFixtureWithPolicy(t, "", policy)
	request.MaxNanoAIU = 0
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if !flow.Budget.AccountQuotaOnly || flow.Budget.MaxNanoAIU != 0 || state.Graph.Validate() != nil {
		t.Fatal("omitted ceiling was not explicitly bound to the account policy")
	}
	// Missing native telemetry stays unknown, but does not invent a quota conversion.
	state.Graph.Dispatches[0].RuntimeSessionID = "fixture-session"
	state.Graph.Dispatches[0].Status = core.DispatchSucceeded
	report := taskBudgetReport(state.Graph, flow, flow.StartedAt)
	if !report.AccountQuotaOnly || report.CreditsStatus != "unknown" || report.NanoAIURemaining != 0 ||
		flowBudgetStop(report, "REVIEWER") != "" || activeBudgetStop(report) != "" ||
		requireSessionBudget(state.Graph, flow, "REVIEWER") != nil {
		t.Fatal("native telemetry was fabricated or incorrectly gated quota control", report)
	}
	report.ElapsedRemainingMillis = 0
	if activeBudgetStop(report) != "elapsed budget reached" {
		t.Fatal("quota-only control disabled the deadline")
	}
	report.ElapsedRemainingMillis, report.DispatchesRemaining = 1, 0
	if flowBudgetStop(report, "REVIEWER") != "Task Dispatch ceiling reached" {
		t.Fatal("quota-only control disabled dispatch limits")
	}
	saved, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved.Graph.ReviewFlows[0].Budget.AccountQuotaOnly = false
	if saved.Graph.Validate() == nil {
		t.Fatal("zero native ceiling without an explicit mode was accepted")
	}
	saved.Graph.ReviewFlows[0].Budget.AccountQuotaOnly = true
	saved.Graph.ReviewFlows[0].Budget.MaxNanoAIU = 1
	if saved.Graph.Validate() == nil {
		t.Fatal("contradictory quota-only and native ceiling were accepted")
	}
}

func TestOmittedFlowCeilingRequiresApprovedResetDailyPolicy(t *testing.T) {
	for _, mode := range []string{"legacy", "off", "flow", "account"} {
		t.Run(mode, func(t *testing.T) {
			var policy *core.BudgetGuidancePolicy
			if mode != "legacy" {
				policy = budgetPolicy(t, mode)
			}
			db, ledger, request := reviewFlowFixtureWithPolicy(t, "", policy)
			request.MaxNanoAIU = 0
			if _, err := StartReviewFlow(db, ledger, request); err == nil {
				t.Fatal("missing ceiling silently expanded an existing contract")
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Graph.ReviewFlows) != 0 || len(state.Graph.Dispatches) != 0 {
				t.Fatal("rejected omitted ceiling changed state")
			}
		})
	}
}

func TestExplicitFlowCeilingRemainsHardWithAccountAllocation(t *testing.T) {
	policy := budgetPolicy(t, "account")
	policy.AccountAllocation = "reset-daily"
	for _, ceiling := range []float64{1, -1, math.NaN(), math.Inf(1)} {
		t.Run(fmt.Sprint(ceiling), func(t *testing.T) {
			db, ledger, request := reviewFlowFixtureWithPolicy(t, "", policy)
			request.MaxNanoAIU = ceiling
			flow, err := StartReviewFlow(db, ledger, request)
			if ceiling != 1 {
				if err == nil {
					t.Fatal("invalid explicit ceiling was accepted")
				}
				return
			}
			if err != nil || flow.Budget.AccountQuotaOnly || flow.Budget.MaxNanoAIU != 1 {
				t.Fatal("explicit ceiling replaced by account-only control", err)
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			report := taskBudgetReport(state.Graph, flow, flow.StartedAt)
			report.NanoAIURemaining = 0
			if flowBudgetStop(report, "REVIEWER") != "nano-AIU ceiling reached" {
				t.Fatal("explicit native ceiling no longer stops execution")
			}
		})
	}
}

func TestAccountQuotaOnlyFlowCannotOverrideMonitoring(t *testing.T) {
	policy := budgetPolicy(t, "account")
	policy.AccountAllocation = "reset-daily"
	db, id, base, _ := executionConfigurationFixture(t, policy)
	defer db.Close()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		dispatch, _ := state.Graph.GraphDispatch(id)
		task, _ := state.Graph.GraphTask(dispatch.TaskID)
		task.Status = core.Ready
		now := time.Now().UTC()
		state.Graph.ReviewFlows = append(state.Graph.ReviewFlows, core.ReviewFlow{
			ID: "quota-flow", TaskID: task.ID, Contract: task, ReviewRoot: filepath.Join(t.TempDir(), "reviews"),
			Budget:    core.FlowBudget{MaxDispatches: 2, MaxCoderSessions: 1, MaxReviewerSessions: 1, AccountQuotaOnly: true},
			StartedAt: now, Deadline: now.Add(time.Hour), Status: "RUNNING", DispatchIDs: []string{id},
			Authorization: core.TaskDecision{ID: "quota-approval", Source: "human:fixture", Text: "approve the saved account remaining floor"},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
	reset := time.Now().UTC().Add(8 * 24 * time.Hour)
	worker.quota.ResetAt = &reset
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	dispatch, err := control.boundDispatch(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"off", "flow"} {
		if _, err := control.prepareBudgetGuidance(t.Context(), dispatch, mode); err == nil {
			t.Fatal("account-only budget disabled mandatory monitoring", mode)
		}
	}
	if err := control.Send(t.Context(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	initial := &budgetObservation{Mode: "account", Account: &worker.quota}
	zero := 0.0
	worker.quota.RemainingPercentage = &zero
	if err := control.monitorAccountQuota(t.Context(), dispatch, initial); err == nil {
		t.Fatal("quota-only flow accepted exhausted account quota")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	flow, _ := state.Graph.ReviewFlow("quota-flow")
	if flow.StopCode != "BUDGET_STOP" || len(state.Records.Budgets) == 0 {
		t.Fatal("quota refusal was not recorded", flow)
	}
}
