package controller

import (
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestIntakeSeparatesOriginalRequestExactConfirmationAndExecution(t *testing.T) {
	db, _, _, resolved := executionConfigurationFixture(t)
	resolved.Config.Execution.ApprovalRef = "intake-configuration-approval"
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask("task")
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "intake", core.Draft, "", nil
	proposal := IntakeRequest{Request: "Implement the requested behavior without changing unrelated files.", Task: task, Actor: "human", Source: "human:request"}
	saved, err := ProposeIntake(db, proposal)
	if err != nil || saved.Status != core.Draft || saved.AuthorizationID != "" {
		t.Fatal("intake invented execution permission", err)
	}
	if _, err := ProposeIntake(db, proposal); err != nil {
		t.Fatal("exact intake replay failed", err)
	}
	hash, err := IntakeHash(saved)
	if err != nil {
		t.Fatal(err)
	}
	confirmation := IntakeConfirmation{TaskID: saved.ID, ExpectedHash: hash, Actor: "human", Source: "human:confirm",
		Reason: "Confirm this scope, acceptance, validation, actors and contract.", Selection: resolved.Config,
		AdditionalTaskIDs: []string{"intake-flow-review-0", "intake-flow-review-1"}}
	before, _ := db.LoadWorkspace()
	stale := confirmation
	stale.ExpectedHash = "different"
	if _, err := ConfirmIntake(db, stale); err == nil {
		t.Fatal("stale intake was approved")
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected confirmation partially changed approval")
	}
	saved, err = ConfirmIntake(db, confirmation)
	if err != nil || saved.Status != core.Ready || saved.AuthorizationID != "intake-confirmation:"+saved.ID {
		t.Fatal("exact confirmed intake was not prepared", err)
	}
	if _, err := CheckConfiguration(db, saved.ID, resolved.Config); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfirmIntake(db, confirmation); err != nil {
		t.Fatal("exact confirmation replay failed", err)
	}
	changed := confirmation
	changed.Selection.Runtime.Account = "another-account"
	if _, err := ConfirmIntake(db, changed); err == nil {
		t.Fatal("confirmation silently widened its actor")
	}
	after, _ = db.LoadWorkspace()
	if len(after.Graph.Dispatches) != len(state.Graph.Dispatches) {
		t.Fatal("confirmation issued or ran work")
	}
}

func TestReapprovalCreatesSuccessorWithoutRewritingIssuedContract(t *testing.T) {
	db, ledger, planner := plannerDraftFixture(t)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	old, _ := before.Graph.ReviewFlow(planner.FlowID)
	task := old.Contract
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "successor", core.Draft, "", nil
	task.Supersedes, task.CreatedFrom = old.TaskID, "flow:"+old.ID
	task.Acceptance = []string{"revised acceptance explicitly confirmed"}
	task.BaselineSHA = planner.Target.OutputSHA
	_, _, _, selected := executionConfigurationFixture(t)
	request := IntakeRequest{Request: "Change acceptance after reviewing this exact result.", Task: task, Actor: "human", Source: "human:changed-request"}
	saved, err := ProposeIntake(db, request)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := IntakeHash(saved)
	confirm := IntakeConfirmation{TaskID: saved.ID, ExpectedHash: hash, Actor: "human", Source: "human:reapprove",
		Reason: "Explicitly approve revised scope, acceptance and selected identity.", Selection: selected.Config,
		ReplacesFlow: old.ID, Target: &planner.Target, AdditionalTaskIDs: []string{"successor-flow-review-0"}}
	stale := confirm
	target := *confirm.Target
	target.OutputSHA = old.Contract.BaselineSHA
	stale.Target = &target
	if _, err := ConfirmIntake(db, stale); err == nil {
		t.Fatal("reapproval applied to a stale SHA")
	}
	saved, err = ConfirmIntake(db, confirm)
	if err != nil || saved.Status != core.Ready {
		t.Fatal("explicit successor was not confirmed", err)
	}
	after, _ := db.LoadWorkspace()
	previous, _ := after.Graph.ReviewFlow(old.ID)
	previousTask, _ := after.Graph.GraphTask(old.TaskID)
	if previous.Status != "STOPPED" || previousTask.Status != core.Superseded ||
		!reflect.DeepEqual(previous.Contract, old.Contract) || previous.MaxTokens != old.MaxTokens ||
		!reflect.DeepEqual(after.Graph.Dispatches, before.Graph.Dispatches) {
		t.Fatal("reapproval rewrote issued contracts, stop/result evidence or old ceilings")
	}
	if _, err := ConfirmIntake(db, confirm); err != nil {
		t.Fatal("reapproval replay affected another flow", err)
	}
	repairs := 0
	if _, err := StartReviewFlow(db, ledger, ReviewFlowRequest{
		ID: "successor-flow", TaskID: saved.ID, Repository: saved.Repo, ReviewRoot: old.ReviewRoot,
		MaxRepairs: &repairs, MaxTokens: 2000, TimeoutSeconds: 30,
		Authorization: core.TaskDecision{ID: "new-flow-approval", Source: "human:new-budget", Text: "Authorize a separately bounded successor"},
	}); err != nil {
		t.Fatal("old held flow blocked explicitly approved successor execution", err)
	}
}

func TestDraftPlanningApprovalDoesNotAuthorizeExecution(t *testing.T) {
	db, _, _, resolved := executionConfigurationFixture(t)
	resolved.Config.Execution.ApprovalRef = "draft-planning-config"
	state, _ := db.LoadWorkspace()
	task, _ := state.Graph.GraphTask("task")
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "planning-intake", core.Draft, "", nil
	saved, err := ProposeIntake(db, IntakeRequest{Request: "Clarify this scope and acceptance before implementation.",
		Task: task, Actor: "human", Source: "human:request", PlanningSelection: &resolved.Config,
		PlanningReason: "Authorize only a separate bounded tool-free planning conversation."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckConfiguration(db, saved.ID, resolved.Config); err != nil {
		t.Fatal("planning approval cannot authorize its isolated question", err)
	}
	state, _ = db.LoadWorkspace()
	if saved.AuthorizationID != "" || saved.Status != core.Draft || state.Graph.CheckExecutable(saved.ID, saved.Repo) == nil {
		t.Fatal("planning approval became execution approval")
	}
}
func TestIntakeRejectsIncompleteOrImplicitAuthorization(t *testing.T) {
	db, _, _, _ := executionConfigurationFixture(t)
	state, _ := db.LoadWorkspace()
	task, _ := state.Graph.GraphTask("task")
	if _, err := ProposeIntake(db, IntakeRequest{Request: "request", Task: task, Actor: "human", Source: "human:request"}); err == nil {
		t.Fatal("intake accepted an already authorized active Task")
	}
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "incomplete", core.Draft, "", nil
	task.Scope = nil
	saved, err := ProposeIntake(db, IntakeRequest{Request: "Clarify scope", Task: task, Actor: "human", Source: "human:request"})
	if err != nil {
		t.Fatal("incomplete draft cannot be recorded for clarification", err)
	}
	_, _, _, selected := executionConfigurationFixture(t)
	hash, _ := IntakeHash(saved)
	before, _ := db.LoadWorkspace()
	if _, err := ConfirmIntake(db, IntakeConfirmation{TaskID: saved.ID, ExpectedHash: hash, Actor: "human", Source: "human:confirm",
		Reason: "Confirm", Selection: selected.Config}); err == nil {
		t.Fatal("incomplete contract became authorized")
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed contract confirmation retained partial approval")
	}
}

func TestPlannerNonExecutionActionRecordsDecisionWithoutRepair(t *testing.T) {
	for _, action := range []string{"REPLAN", "INVESTIGATE", "HOLD", "HUMAN_REQUIRED", "REPORT"} {
		t.Run(action, func(t *testing.T) {
			db, ledger, request := plannerDraftFixture(t)
			request.Proposal.Answer.Recommendation = action
			before, _ := db.LoadWorkspace()
			flow, err := RecordPlannerAction(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			if !flow.HandoffHeld() || len(flow.PendingFeedbackIDs()) != 1 || len(flow.InstructionDrafts) != 0 {
				t.Fatal("non-execution action became an instruction or silently released work")
			}
			if _, err := RecordPlannerAction(db, ledger, request); err != nil {
				t.Fatal("exact selected action replay failed", err)
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before.Graph.Dispatches, after.Graph.Dispatches) {
				t.Fatal("selected action changed issued work")
			}
			if _, err := ChangeHandoff(db, handoffRequest("release-action", "hold", "RELEASE")); err == nil {
				t.Fatal("unresolved selected action permitted continuation")
			}
			if _, err := ResolveFeedback(db, FeedbackResolutionRequest{
				FlowID: flow.ID, FeedbackID: request.ID, FeedbackResolution: core.FeedbackResolution{
					ID: "dispose-action", ExpectedChangeID: "hold", Target: request.Target, Action: "KEEP_CONTRACT",
					Actor: "human", Source: "human:decision", Reason: "Explicitly retain original contract after considering advice",
				},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := ChangeHandoff(db, handoffRequest("release-action", "hold", "RELEASE")); err != nil {
				t.Fatal(err)
			}
			released, _ := db.LoadWorkspace()
			if _, err := RecordPlannerAction(db, ledger, request); err != nil {
				t.Fatal("exact replay after release failed", err)
			}
			replayed, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(released, replayed) {
				t.Fatal("old advice replay changed released work")
			}
			request.ExpectedDraftID = "another-version"
			if _, err := RecordPlannerAction(db, ledger, request); err == nil {
				t.Fatal("same ID changed selected draft version")
			}
		})
	}
}

func TestIntakeRejectsLedgerIDCollision(t *testing.T) {
	db, _, _, _ := executionConfigurationFixture(t)
	before, _ := db.LoadWorkspace()
	task, _ := before.Graph.GraphTask("task")
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "ledger-only", core.Draft, "", nil
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: task.ID, Title: "Existing", Repo: task.Repo})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ = db.LoadWorkspace()
	if _, err := ProposeIntake(db, IntakeRequest{Request: "request", Task: task, Actor: "human", Source: "human:request"}); err == nil {
		t.Fatal("intake duplicated a ledger Task ID")
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected collision changed the workspace")
	}
}
