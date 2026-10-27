package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/tools"
)

func intakeApprovalFixture(t *testing.T, bundleRequired bool) (*store.Store, IntakeConfirmation) {
	t.Helper()
	db, _, _, resolved := executionConfigurationFixture(t)
	resolved.Config.Execution.ApprovalRef = "confirmed-intake-config"
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask("task")
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "confirmed-intake", core.Draft, "", nil
	if bundleRequired {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			state.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	proposal, err := ProposeIntake(db, IntakeRequest{Task: task, Request: "Implement within this contract.",
		Actor: "human", Source: "human:request"})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := IntakeHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	return db, IntakeConfirmation{TaskID: task.ID, ExpectedHash: hash, Actor: "human", Source: "human:confirmed",
		Reason: "Approve the displayed contract and selected configuration.", Selection: resolved.Config}
}

func TestIntakeConfirmationDerivesBundleWithoutSecondApproval(t *testing.T) {
	db, request := intakeApprovalFixture(t, true)
	before, _ := db.LoadWorkspace()
	task, err := ConfirmIntake(db, request)
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	bundle, ok := after.Graph.NewestApproval(task.ID)
	if !ok || len(after.Graph.ApprovalsForTask(task.ID)) != 1 || bundle.ProposalHash != request.ExpectedHash ||
		bundle.Actor != request.Actor || bundle.Source != request.Source || bundle.Reason != request.Reason {
		t.Fatal("formal confirmation was not preserved as the approval basis", bundle)
	}
	selection, _ := json.Marshal(request.Selection)
	configuration, err := core.SelectApprovalConfiguration(selection, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range core.ApprovalEntryStages() {
		if err := checkEntryApproval(after.Graph, task, stage, configuration); err != nil {
			t.Fatalf("same contract still needs a second approval at %s: %v", stage, err)
		}
	}
	changed := request.Selection
	changed.Runtime.Account = "another-account"
	changedSelection, _ := json.Marshal(changed)
	changedConfiguration, _ := core.SelectApprovalConfiguration(changedSelection, nil)
	if err := bundle.Verify(task, changedConfiguration); err == nil {
		t.Fatal("derived bundle authorized a different configuration")
	}
	if !reflect.DeepEqual(before.Graph.Dispatches, after.Graph.Dispatches) ||
		!reflect.DeepEqual(before.Graph.ReviewFlows, after.Graph.ReviewFlows) ||
		!reflect.DeepEqual(before.Records, after.Records) {
		t.Fatal("confirmation issued execution or delegated unapproved roles")
	}
	if _, err := ConfirmIntake(db, request); err != nil {
		t.Fatal(err)
	}
	replayed, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(after, replayed) {
		t.Fatal("exact replay changed the original approval or created another bundle")
	}
}

func TestIntakeConfirmationReplayMaterializesOnlyMissingBundle(t *testing.T) {
	db, request := intakeApprovalFixture(t, false)
	if _, err := ConfirmIntake(db, request); err != nil {
		t.Fatal(err)
	}
	legacy, _ := db.LoadWorkspace()
	if legacy.Graph.ApprovalRequirement() != core.ApprovalVerifiedLegacy || len(legacy.Graph.Approvals) != 0 {
		t.Fatal("confirmation silently migrated a legacy graph")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := db.LoadWorkspace()
	if _, err := ConfirmIntake(db, request); err != nil {
		t.Fatal("original exact confirmation could not fill its missing representation", err)
	}
	after, _ := db.LoadWorkspace()
	if len(after.Graph.Approvals) != 1 {
		t.Fatal("confirmation replay did not append exactly one bundle")
	}
	after.Graph.Approvals = nil
	if !reflect.DeepEqual(before, after) {
		t.Fatal("confirmation replay changed the original contract, history, budget or execution records")
	}
}

func TestIntakeConfirmationReuseRejectsUncoveredChanges(t *testing.T) {
	db, request := intakeApprovalFixture(t, false)
	task, err := ConfirmIntake(db, request)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := db.LoadWorkspace()
	original.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion
	for name, mutate := range map[string]func(*store.Workspace, *core.GraphTask){
		"objective":       func(_ *store.Workspace, task *core.GraphTask) { task.Objective += " changed" },
		"acceptance":      func(_ *store.Workspace, task *core.GraphTask) { task.Acceptance = []string{"changed"} },
		"scope":           func(_ *store.Workspace, task *core.GraphTask) { task.Scope = []string{"other"} },
		"permissions":     func(_ *store.Workspace, task *core.GraphTask) { task.Owner = "REVIEWER" },
		"authorization":   func(_ *store.Workspace, task *core.GraphTask) { task.AuthorizationID = "other" },
		"active":          func(_ *store.Workspace, task *core.GraphTask) { task.Status = core.Active },
		"missing-history": func(state *store.Workspace, _ *core.GraphTask) { state.Graph.History = nil },
		"flow": func(state *store.Workspace, task *core.GraphTask) {
			state.Graph.ReviewFlows = append(state.Graph.ReviewFlows, core.ReviewFlow{TaskID: task.ID})
		},
		"dispatch": func(state *store.Workspace, task *core.GraphTask) {
			state.Graph.Dispatches = append(state.Graph.Dispatches, core.GraphDispatch{TaskID: task.ID})
		},
		"ledger-dispatch": func(state *store.Workspace, task *core.GraphTask) {
			state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{TaskID: task.ID})
		},
		"unknown-schema": func(state *store.Workspace, _ *core.GraphTask) {
			state.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion + 1
		},
		"conflicting-bundle": func(state *store.Workspace, task *core.GraphTask) {
			state.Graph.Approvals = append(state.Graph.Approvals, core.ApprovalBundle{TaskID: task.ID})
		},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			var state store.Workspace
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			current, _ := state.Graph.GraphTask(task.ID)
			mutate(&state, &current)
			before, _ := core.CanonicalSHA256(state)
			if err := appendConfirmedIntakeApproval(&state, current, request); err == nil {
				t.Fatal("confirmation reuse authorized a changed or executed contract")
			}
			after, _ := core.CanonicalSHA256(state)
			if before != after {
				t.Fatal("rejected reuse changed the workspace")
			}
		})
	}
	changed := request
	changed.Selection.Runtime.Account = "another-account"
	if _, err := ConfirmIntake(db, changed); err == nil {
		t.Fatal("confirmation replay accepted a different setting")
	}
}

func TestIntakeConfirmationAndBundlePersistAtomically(t *testing.T) {
	db, request := intakeApprovalFixture(t, true)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		other, _ := state.Graph.GraphTask("task")
		selection, _ := json.Marshal(request.Selection)
		configuration, err := core.SelectApprovalConfiguration(selection, nil)
		if err != nil {
			return err
		}
		bundle, err := core.ComposeApprovalBundle("intake-confirmation:"+request.TaskID,
			other, other, configuration, "human", "human:other", "Other approval", time.Now())
		if err != nil {
			return err
		}
		return state.Graph.AppendApproval(bundle)
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := db.LoadWorkspace()
	if _, err := ConfirmIntake(db, request); err == nil {
		t.Fatal("confirmation ignored a bundle persistence conflict")
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("bundle save failure left a partially confirmed Task")
	}
}

func TestConfirmedFlowUsesAlreadyApprovedReviewerWithoutNewDelegation(t *testing.T) {
	db, ledger, flowRequest := reviewFlowFixture(t)
	_, _, _, resolved := executionConfigurationFixture(t)
	state, _ := db.LoadWorkspace()
	task, _ := state.Graph.GraphTask(flowRequest.TaskID)
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "confirmed-flow", core.Draft, "", nil
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	proposal, err := ProposeIntake(db, IntakeRequest{Task: task, Request: "Implement and independently review.",
		Actor: "human", Source: "human:request"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := IntakeHash(proposal)
	confirmation := IntakeConfirmation{TaskID: task.ID, ExpectedHash: hash, Actor: "human", Source: "human:confirm",
		Reason: "Approve this contract and both bounded review configurations.", Selection: resolved.Config,
		AdditionalTaskIDs: []string{flowRequest.ID + "-review-0", flowRequest.ID + "-review-1"}}
	if _, err := ConfirmIntake(db, confirmation); err != nil {
		t.Fatal(err)
	}
	flowRequest.TaskID = task.ID
	selection, _ := json.Marshal(resolved.Config)
	flowRequest.Configuration, err = core.SelectApprovalConfiguration(selection, resolved.Sources)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := StartReviewFlow(db, ledger, flowRequest)
	if err != nil {
		t.Fatal("approved Flow could not start without another bundle/delegation approval", err)
	}
	if err := CheckReviewFlowConfiguration(db, flow.ID, resolved.Config, nil); err != nil {
		t.Fatal("explicitly covered Reviewer incorrectly requires packet delegation", err)
	}
	state, _ = db.LoadWorkspace()
	if len(state.Records.PacketAuthorizations) != 0 || len(state.Graph.Dispatches) != 1 {
		t.Fatal("Flow invented delegation or issued duplicate work")
	}
	if _, err := ConfirmIntake(db, confirmation); err != nil {
		t.Fatal("exact confirmation replay stopped an already issued Flow", err)
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(state, after) {
		t.Fatal("confirmation replay after issuance changed the Flow or original approval")
	}
}

func TestIntakeSeparatesOriginalRequestExactConfirmationAndExecution(t *testing.T) {
	for _, repo := range []string{"owner/repo", "group/subgroup/project", strings.Repeat("group/", 21) + "project"} {
		t.Run(repo, func(t *testing.T) {
			testIntakeRepositoryConfirmation(t, repo)
		})
	}
}

func testIntakeRepositoryConfirmation(t *testing.T, repo string) {
	t.Helper()
	db, _, _, resolved := executionConfigurationFixture(t)
	resolved.Config.Execution.ApprovalRef = "intake-configuration-approval"
	resolved.Config.Repository.Path = repo
	if strings.Count(repo, "/") > 1 {
		resolved.Config.Repository.Provider = "gitlab"
		resolved.Config.Repository.Host = "gitlab.example.com"
		resolved.Config.Repository.CredentialRef = "glab:active"
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask("task")
	task.Repo = repo
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "intake", core.Draft, "", nil
	proposal := IntakeRequest{Request: "Implement the requested behavior without changing unrelated files.", Task: task, Actor: "human", Source: "human:request"}
	saved, err := ProposeIntake(db, proposal)
	if err != nil || saved.Status != core.Draft || saved.AuthorizationID != "" ||
		saved.FileAccess == nil || saved.FileAccess.Level != core.FileAccessCommitted {
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
	wrongRepository := confirmation
	wrongRepository.Selection.Repository.Path = "other/" + repo
	if _, err := ConfirmIntake(db, wrongRepository); err == nil {
		t.Fatal("confirmation accepted a different parent namespace")
	}
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
		MaxRepairs: &repairs, TimeoutSeconds: 30, MaxNanoAIU: 1e12,
		Authorization: core.TaskDecision{ID: "new-flow-approval", Source: "human:new-budget", Text: "Authorize a separately bounded successor"},
	}); err != nil {
		t.Fatal("old held flow blocked explicitly approved successor execution", err)
	}
}

func TestBudgetStoppedFlowContinuesFromVerifiedPartialTreeWithNewApproval(t *testing.T) {
	for _, mode := range []string{core.OutputModeCommit, core.OutputModeWorktreeTreeSnapshot} {
		t.Run(mode, func(t *testing.T) {
			testStoppedFlowContinuesFromVerifiedPartialTree(t, mode)
		})
	}
}

func testStoppedFlowContinuesFromVerifiedPartialTree(t *testing.T, mode string) {
	t.Helper()
	db, ledger, request := reviewFlowFixtureWithOutputMode(t, mode)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	dispatchID := flow.DispatchIDs[0]
	worker := &fakeRuntime{role: "CODER", hash: "budget-runtime", worktree: flow.Contract.Worktree}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), dispatchID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(context.Background(), dispatchID, "execute approved work"); err != nil {
		t.Fatal(err)
	}
	partialFile := filepath.Join(flow.Contract.Worktree, "internal", "partial.go")
	if err := os.MkdirAll(filepath.Dir(partialFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialFile, []byte("partial implementation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := control.Stop(context.Background(), dispatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowStopped) {
		t.Fatalf("cancelled flow did not stop: %v", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	last, _ := state.Graph.GraphDispatch(dispatchID)
	flow, _ = state.Graph.ReviewFlow(flow.ID)
	if !flow.HandoffHeld() || last.Result == nil || last.Result.PartialGitEvidence == nil {
		t.Fatal("stopped flow did not retain a held partial-work checkpoint")
	}

	sourceWorktree := flow.Contract.Worktree
	if mode == core.OutputModeCommit {
		gitTest(t, sourceWorktree, "add", "-A")
		gitTest(t, sourceWorktree, "commit", "-m", "chore: checkpoint partial work")
	}
	targetSHA := gitTest(t, sourceWorktree, "rev-parse", "HEAD")
	successorWorktree := filepath.Join(t.TempDir(), "successor")
	gitTest(t, sourceWorktree, "worktree", "add", "-b", "work/successor", successorWorktree, targetSHA)
	task := flow.Contract
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "budget-successor", core.Draft, "", nil
	task.BudgetGuidance = budgetPolicy(t, "flow")
	task.Supersedes, task.CreatedFrom = flow.TaskID, "flow:"+flow.ID
	task.Worktree, task.Branch, task.TargetSHA = successorWorktree, "work/successor", targetSHA
	if mode == core.OutputModeWorktreeTreeSnapshot {
		if err := os.MkdirAll(filepath.Join(successorWorktree, "internal"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(successorWorktree, "internal/partial.go"),
			[]byte("partial implementation\n"), 0600); err != nil {
			t.Fatal(err)
		}
		task.TargetTreeSHA = last.Result.PartialGitEvidence.TreeSHA
		task.TargetChangedFiles = last.Result.PartialGitEvidence.ChangedFiles
	}
	_, _, _, selection := executionConfigurationFixture(t)
	proposal, err := ProposeIntake(db, IntakeRequest{
		Request: "Continue the stopped work from its recorded partial tree with a new budget.",
		Task:    task, Actor: "human", Source: "human:budget-successor",
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := IntakeHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	target, err := core.ObserveFeedbackTarget(last)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := ConfirmIntake(db, IntakeConfirmation{
		TaskID: proposal.ID, ExpectedHash: hash, Actor: "human", Source: "human:budget-successor",
		Reason:    "Approve the exact partial tree and successor contract under a new execution budget.",
		Selection: selection.Config, ReplacesFlow: flow.ID, Target: &target,
	})
	if err != nil || approved.Status != core.Ready {
		t.Fatal("verified partial tree was not explicitly reapproved", err)
	}
	repairs := 0
	successor, err := StartReviewFlow(db, ledger, ReviewFlowRequest{
		ID: "budget-successor-flow", TaskID: approved.ID, Repository: approved.Repo, ReviewRoot: flow.ReviewRoot,
		MaxRepairs: &repairs, TimeoutSeconds: 60, MaxNanoAIU: 1e12,
		Authorization: core.TaskDecision{ID: "new-budget", Source: "human:new-budget", Text: "Approve a separate execution budget"},
	})
	if err != nil {
		t.Fatal("successor flow did not accept its separate budget", err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	successorDispatch, _ := state.Graph.GraphDispatch(successor.DispatchIDs[0])
	if successorDispatch.Instruction.BaselineSHA != flow.Contract.BaselineSHA ||
		successorDispatch.Instruction.TargetSHA != targetSHA {
		t.Fatal("successor dispatch did not retain original baseline and partial target")
	}
	if _, err := gitproof.ObserveStart(successorDispatch); err != nil {
		t.Fatal("successor worktree failed execution-start verification", err)
	}
	if mode == core.OutputModeWorktreeTreeSnapshot {
		executeFlowDispatch(t, db, successor, "", true)
		if head := gitTest(t, successorWorktree, "rev-parse", "HEAD"); head != flow.Contract.BaselineSHA {
			t.Fatal("snapshot continuation created a commit")
		}
	}
}

func TestBudgetStoppedCompletedCoderContinuesAtReviewerWithoutRerun(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		legacy, ok := state.Graph.ReviewFlow(flow.ID)
		if !ok {
			return errors.New("flow missing")
		}
		if err := holdReviewFlowForStop(&state.Graph, &legacy, "legacy token threshold"); err != nil {
			return err
		}
		legacy.Reason = "legacy token threshold"
		legacy.Status, legacy.StopCode = "STOPPED", "BUDGET_LIMIT"
		storeFlow(state, legacy)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if !errors.Is(err, ErrFlowStopped) || flow.StopCode != "BUDGET_LIMIT" ||
		flow.Status != "STOPPED" || !flow.HandoffHeld() || len(flow.DispatchIDs) != 1 {
		t.Fatalf("legacy stopped Coder result was not preserved: %+v %v", flow, err)
	}
	successorWorktree := filepath.Join(t.TempDir(), "successor")
	gitTest(t, flow.Contract.Worktree, "worktree", "add", "-b", "work/successor", successorWorktree, coder.Result.OutputSHA)
	task := flow.Contract
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "review-successor", core.Draft, "", nil
	task.Supersedes, task.CreatedFrom = flow.TaskID, "flow:"+flow.ID
	task.Worktree, task.Branch, task.TargetSHA = successorWorktree, "work/successor", coder.Result.OutputSHA
	_, _, _, selection := executionConfigurationFixture(t)
	proposal, err := ProposeIntake(db, IntakeRequest{
		Request: "Continue the saved Coder result directly at independent review with a new budget.",
		Task:    task, Actor: "human", Source: "human:review-successor",
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := IntakeHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	target, err := core.ObserveFeedbackTarget(coder)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := ConfirmIntake(db, IntakeConfirmation{
		TaskID: proposal.ID, ExpectedHash: hash, Actor: "human", Source: "human:review-successor",
		Reason:    "Approve the exact saved Coder output and a new independent-review budget.",
		Selection: selection.Config, ReplacesFlow: flow.ID, Target: &target,
	})
	if err != nil || approved.Status != core.Ready {
		t.Fatal("completed Coder result was not explicitly approved for continuation", err)
	}
	repairs := 0
	successor, err := StartReviewFlow(db, ledger, ReviewFlowRequest{
		ID: "review-successor-flow", TaskID: approved.ID, Repository: approved.Repo, ReviewRoot: flow.ReviewRoot,
		ContinueFromDispatchID: coder.ID, MaxRepairs: &repairs, TimeoutSeconds: 60, MaxNanoAIU: 1e12,
		Authorization: core.TaskDecision{ID: "new-review-budget", Source: "human:new-review-budget", Text: "Approve a separate review budget"},
	})
	if err != nil {
		t.Fatal("new flow did not continue at the saved Coder result", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	first, _ := state.Graph.GraphDispatch(successor.DispatchIDs[0])
	if first.Recipient != "REVIEWER" || first.Instruction.TargetSHA != coder.Result.OutputSHA ||
		successor.SourceDispatchID != coder.ID || len(state.Graph.Dispatches) != 2 {
		t.Fatalf("completed Coder turn was repeated instead of resuming at review: flow=%+v dispatch=%+v", successor, first)
	}
}

func TestBudgetContinuationReusesCoderAfterReviewerSafeStop(t *testing.T) {
	testContinuationReusesCoderAfterReviewerSafeStop(t, "BUDGET_LIMIT")
}

func TestSafetyStopContinuationReusesCoderAfterReviewerSafeStop(t *testing.T) {
	testContinuationReusesCoderAfterReviewerSafeStop(t, "SAFETY_STOP")
}

func testContinuationReusesCoderAfterReviewerSafeStop(t *testing.T, stopCode string) {
	t.Helper()
	db, ledger, request := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	reviewerID := flow.DispatchIDs[len(flow.DispatchIDs)-1]
	if err := holdReviewFlowForStop(&state.Graph, &flow, "Reviewer exceeded observed token ceiling"); err != nil {
		t.Fatal(err)
	}
	flow.Status, flow.StopCode = "STOPPED", stopCode
	storeFlow(&state, flow)
	for index := range state.Graph.Dispatches {
		reviewer := &state.Graph.Dispatches[index]
		if reviewer.ID == reviewerID {
			reviewer.Status, reviewer.RuntimeSessionID = core.DispatchCancelled, "stopped-review"
			reviewer.RuntimeConfigHash = "fixture-config"
			reviewer.Result = &core.DispatchResult{
				Status: core.DispatchCancelled, Evidence: []string{"runtime-stop:stopped-review"},
			}
		}
	}
	task := flow.Contract
	task.ID, task.Supersedes, task.CreatedFrom = "review-recovery", flow.TaskID, "flow:"+flow.ID
	task.Worktree, task.Branch = filepath.Join(t.TempDir(), "successor"), "work/review-recovery"
	gitTest(t, flow.Contract.Worktree, "worktree", "add", "-b", task.Branch, task.Worktree, coder.Result.OutputSHA)
	source, err := os.ReadFile(filepath.Join(flow.Contract.Worktree, "internal/app.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(task.Worktree, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.Worktree, "internal/app.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	task.TargetSHA, task.TargetTreeSHA = coder.Result.OutputSHA, coder.Result.OutputTreeSHA
	task.TargetChangedFiles = coder.Result.ChangedFiles
	if _, err := budgetContinuationSource(state.Graph, task, coder.ID); err != nil {
		t.Fatal("saved Coder could not be reused after safe Reviewer stop", err)
	}
	for _, change := range []string{"running", "missing-stop-proof", "different-coder", "wrong-tree"} {
		t.Run(change, func(t *testing.T) {
			graph := state.Graph
			graph.Dispatches = append([]core.GraphDispatch(nil), graph.Dispatches...)
			candidate := task
			for index := range graph.Dispatches {
				reviewer := &graph.Dispatches[index]
				if reviewer.ID != reviewerID {
					continue
				}
				switch change {
				case "running":
					reviewer.Status = core.DispatchRunning
				case "missing-stop-proof":
					reviewer.Result = &core.DispatchResult{Status: core.DispatchCancelled}
				case "different-coder":
					reviewer.RelatedID = "other-coder"
				case "wrong-tree":
					candidate.TargetTreeSHA = strings.Repeat("f", 40)
				}
			}
			if _, err := budgetContinuationSource(graph, candidate, coder.ID); err == nil {
				t.Fatal("unsafe review continuation was accepted")
			}
		})
	}
}

func persistLegacyBudgetStoppedFlow(t *testing.T, db *store.Store, flowID string) core.ReviewFlow {
	t.Helper()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		flow, ok := state.Graph.ReviewFlow(flowID)
		if !ok {
			return errors.New("flow not found")
		}
		if err := holdReviewFlowForStop(&state.Graph, &flow, "legacy token ceiling"); err != nil {
			return err
		}
		flow.Reason = "legacy token ceiling"
		flow.Status, flow.StopCode, flow.MaxTokens = "STOPPED", "BUDGET_LIMIT", 15
		storeFlow(state, flow)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	flow, ok := state.Graph.ReviewFlow(flowID)
	if !ok {
		t.Fatal("legacy stopped flow was not saved")
	}
	return flow
}

func TestLegacyBudgetStoppedSnapshotCoderContinuesAtReviewerWithoutCommitOrRerun(t *testing.T) {
	testStoppedSnapshotCoderContinuesAtReviewerWithoutCommitOrRerun(t, "BUDGET_LIMIT", false)
}

func TestSafetyStoppedSnapshotCoderContinuesAtReviewerWithoutCommitOrRerun(t *testing.T) {
	testStoppedSnapshotCoderContinuesAtReviewerWithoutCommitOrRerun(t, "SAFETY_STOP", false)
}

func TestAlreadySupersededStoppedSnapshotCoderContinuesAtReviewerWithoutRerun(t *testing.T) {
	for _, stopCode := range []string{"BUDGET_LIMIT", "SAFETY_STOP"} {
		t.Run(stopCode, func(t *testing.T) {
			testStoppedSnapshotCoderContinuesAtReviewerWithoutCommitOrRerun(t, stopCode, true)
		})
	}
}

func testStoppedSnapshotCoderContinuesAtReviewerWithoutCommitOrRerun(t *testing.T, stopCode string, superseded bool) {
	t.Helper()
	db, ledger, request := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	flow = persistLegacyBudgetStoppedFlow(t, db, flow.ID)
	if stopCode == "SAFETY_STOP" {
		flow.StopCode, flow.MaxTokens, flow.Reason = stopCode, 0, "deadline reached"
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			storeFlow(state, flow)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if flow.StopCode != stopCode ||
		flow.Status != "STOPPED" || !flow.HandoffHeld() || len(flow.DispatchIDs) != 1 {
		t.Fatalf("legacy stopped snapshot flow was not represented: %+v", flow)
	}
	successorWorktree := filepath.Join(t.TempDir(), "successor")
	gitTest(t, flow.Contract.Worktree, "worktree", "add", "-b", "work/successor",
		successorWorktree, coder.Result.OutputSHA)
	source, err := os.ReadFile(filepath.Join(flow.Contract.Worktree, "internal/app.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(successorWorktree, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(successorWorktree, "internal/app.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	task := flow.Contract
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "snapshot-review-successor", core.Draft, "", nil
	task.Supersedes, task.CreatedFrom = flow.TaskID, "flow:"+flow.ID
	task.Worktree, task.Branch = successorWorktree, "work/successor"
	task.TargetSHA = coder.Result.OutputSHA
	task.TargetTreeSHA = coder.Result.OutputTreeSHA
	task.TargetChangedFiles = coder.Result.ChangedFiles
	_, _, _, selection := executionConfigurationFixture(t)
	proposal, err := ProposeIntake(db, IntakeRequest{
		Request: "Continue the saved uncommitted Coder tree directly at independent review.",
		Task:    task, Actor: "human", Source: "human:snapshot-review-successor",
	})
	if err != nil {
		t.Fatal(err)
	}
	var previousTask core.GraphTask
	if superseded {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			if err := state.Graph.Transition(flow.TaskID, core.Superseded, "human", "Previously confirmed successor", core.TransitionProof{}); err != nil {
				return err
			}
			previousTask, _ = state.Graph.GraphTask(flow.TaskID)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	skills := t.TempDir()
	if err := os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte("synthetic read-only review skill\n"), 0600); err != nil {
		t.Fatal(err)
	}
	skillHash, err := tools.Hash(skills)
	if err != nil {
		t.Fatal(err)
	}
	policy := tools.Policy{
		Skills: []tools.Asset{{Path: skills, Hash: skillHash}}, EvidenceDirectory: filepath.Join(t.TempDir(), "review-evidence"),
	}
	approval, err := json.Marshal(tools.Approval{
		Kind: tools.ApprovalKind, TaskID: proposal.ID, Actor: "human",
		Roles: map[string]tools.Policy{"REVIEWER": policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal.Decisions = append(proposal.Decisions, core.TaskDecision{
		ID: "successor-review-tools", Source: "human:snapshot-review-successor", Text: string(approval),
	})
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.Track(proposal, "human", "approve fresh tools only for the successor Reviewer")
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := IntakeHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	target, err := core.ObserveFeedbackTarget(coder)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := ConfirmIntake(db, IntakeConfirmation{
		TaskID: proposal.ID, ExpectedHash: hash, Actor: "human", Source: "human:snapshot-review-successor",
		Reason:    "Approve the exact saved tree and a separate independent-review budget.",
		Selection: selection.Config, ReplacesFlow: flow.ID, Target: &target,
		AdditionalTaskIDs:       []string{"snapshot-review-successor-flow-review-0"},
		ConfigurationDecisionID: core.ConfigurationApprovalID(selection.Config.Execution.ApprovalRef, proposal.ID),
	})
	if err != nil || approved.Status != core.Ready {
		t.Fatal("snapshot result was not explicitly approved for continuation", err)
	}
	afterConfirmation, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	preserved, _ := afterConfirmation.Graph.ReviewFlow(flow.ID)
	if !reflect.DeepEqual(flow, preserved) {
		t.Fatal("successor confirmation rewrote the terminal predecessor Flow")
	}
	repairs := 0
	successor, err := StartReviewFlow(db, ledger, ReviewFlowRequest{
		ID: "snapshot-review-successor-flow", TaskID: approved.ID, Repository: approved.Repo,
		ReviewRoot: flow.ReviewRoot, ContinueFromDispatchID: coder.ID,
		MaxRepairs: &repairs, TimeoutSeconds: 60, MaxNanoAIU: 1e12,
		Authorization: core.TaskDecision{
			ID: "new-snapshot-review-budget", Source: "human:new-snapshot-review-budget",
			Text: "Approve a separate review budget",
		},
	})
	if err != nil {
		t.Fatal("new flow did not continue at the saved snapshot", err)
	}
	if err := CheckReviewFlowConfiguration(db, successor.ID, selection.Config,
		map[string][]string{"CODER": {"view", "edit"}, "REVIEWER": {"view"}}); err != nil {
		t.Fatal("review continuation lost its newly approved configuration", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if superseded {
		preserved, _ := state.Graph.GraphTask(flow.TaskID)
		if !reflect.DeepEqual(previousTask, preserved) {
			t.Fatal("new exact approval rewrote the already superseded source Task")
		}
	}
	reviewer, _ := state.Graph.GraphDispatch(successor.DispatchIDs[0])
	if reviewer.Recipient != "REVIEWER" || reviewer.Instruction.TargetSHA != coder.Result.OutputSHA ||
		reviewer.Instruction.TargetTreeSHA != coder.Result.OutputTreeSHA ||
		successor.SourceDispatchID != coder.ID || len(state.Graph.Dispatches) != 2 {
		t.Fatalf("continuation repeated the Coder turn or changed its tree: flow=%+v dispatch=%+v", successor, reviewer)
	}
	resolved, err := tools.ResolveForTask(state.Graph, reviewer.Instruction.Decisions, reviewer.TaskID, "REVIEWER")
	if err != nil || !tools.Equal(resolved, &policy) {
		t.Fatal("Reviewer-only continuation lost its freshly approved skill policy", err)
	}
	for _, change := range []string{"wrong-role", "copied-task", "wrong-source", "wrong-binding", "missing-approval"} {
		t.Run(change, func(t *testing.T) {
			graph := state.Graph
			graph.Tasks = append([]core.GraphTask(nil), graph.Tasks...)
			graph.Dispatches = append([]core.GraphDispatch(nil), graph.Dispatches...)
			graph.ReviewFlows = append([]core.ReviewFlow(nil), graph.ReviewFlows...)
			role := "REVIEWER"
			switch change {
			case "wrong-role":
				role = "CODER"
			case "copied-task", "missing-approval":
				for i := range graph.Tasks {
					if change == "copied-task" && graph.Tasks[i].ID == reviewer.TaskID {
						graph.Tasks[i].CreatedFrom = "flow:unrelated"
					}
					if change == "missing-approval" && graph.Tasks[i].ID == approved.ID {
						graph.Tasks[i].Decisions = nil
					}
				}
			case "wrong-source":
				graph.ReviewFlows[len(graph.ReviewFlows)-1].SourceDispatchID = "unrelated-coder"
			case "wrong-binding":
				graph.Dispatches[len(graph.Dispatches)-1].RelatedID = "unrelated-coder"
			}
			if _, err := tools.ResolveForTask(graph, reviewer.Instruction.Decisions, reviewer.TaskID, role); err == nil {
				t.Fatal("fresh tools approval escaped its independent successor Reviewer")
			}
		})
	}
	reviewer, err = PrepareReview(db, reviewer.ID, approved.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if start, err := gitproof.ObserveStart(reviewer); err != nil || start != coder.Result.OutputSHA {
		t.Fatalf("Reviewer worktree did not reproduce the saved snapshot: %s %v", start, err)
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
