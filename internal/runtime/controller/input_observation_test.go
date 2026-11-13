package controller

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func stoppedObservationFixture(t *testing.T) (*store.Store, string, core.ReviewFlow, InputObservationRequest) {
	t.Helper()
	db, ledger, start := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
	flow, err := StartReviewFlow(db, ledger, start)
	if err != nil {
		t.Fatal(err)
	}
	worker := &fakeRuntime{role: "CODER", hash: "observation-test", worktree: flow.Contract.Worktree, bound: true}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), flow.LastDispatchID()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(flow.Contract.Worktree, "extras"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flow.Contract.Worktree, "extras/input.txt"), []byte("unattributed input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := control.Stop(t.Context(), flow.LastDispatchID()); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("scope rejection was not reproduced: %v", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := holdReviewFlowForStop(&state.Graph, &flow, "preserve rejected partial input"); err != nil {
			return err
		}
		flow.Status, flow.StopCode, flow.Reason = "STOPPED", "SAFETY_STOP", "preserve rejected partial input"
		storeFlow(state, flow)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, _, _, selected := executionConfigurationFixture(t)
	return db, ledger, flow, InputObservationRequest{
		TaskID: flow.TaskID, FlowID: flow.ID, DispatchID: flow.LastDispatchID(),
		Scope: []string{"internal", "extras"}, TargetSHA: flow.Contract.BaselineSHA,
		Configuration: mustFlowJSON(t, selected.Config),
		Actor:         "human", Source: "evidence:current-observation", Reason: "Observe current input, not stop-time authorship",
		Evidence:                   []string{"controller stop receipt and explicit execution-quiescence check"},
		NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
	}
}

func TestObservedInputPreservesCancelledResultAndCompletesApprovedSuccessor(t *testing.T) {
	db, ledger, flow, request := stoppedObservationFixture(t)
	before, _ := db.LoadWorkspace()
	old, _ := before.Graph.GraphDispatch(flow.LastDispatchID())
	if old.RuntimeSessionID == "" || old.Result.PartialGitEvidence != nil || len(old.RuntimeUsage) != 0 {
		t.Fatal("fixture did not preserve a session with unknown usage and no partial evidence")
	}
	index := gitTest(t, flow.Contract.Worktree, "ls-files", "--stage")
	preview, err := ObserveInput(db, request, true)
	if err != nil || preview.StateChanged || preview.Observation.PriorPolicy.Verdict != "blocked" {
		t.Fatalf("current observation was coupled to old suitability: %+v %v", preview, err)
	}
	afterPreview, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, afterPreview) || index != gitTest(t, flow.Contract.Worktree, "ls-files", "--stage") {
		t.Fatal("preview modified saved state or the real index")
	}
	request.ExpectedHash = preview.Observation.Hash
	saved, err := ObserveInput(db, request, false)
	if err != nil || !saved.StateChanged || saved.Observation.Hash != request.ExpectedHash {
		t.Fatalf("exact observation was not recorded: %+v %v", saved, err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Records.InputObservations[0].Contract.Scope[0] = "rewritten"
		return nil
	}); err == nil {
		t.Fatal("nested observation history was mutable")
	}
	o := saved.Observation
	successorPath := filepath.Join(t.TempDir(), "successor")
	gitTest(t, flow.Contract.Worktree, "worktree", "add", "-b", "work/observed-successor", successorPath, o.Target.CommitSHA)
	if err := os.MkdirAll(filepath.Join(successorPath, "extras"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(successorPath, "extras/input.txt"), []byte("unattributed input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task := flow.Contract
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "observed-successor", core.Draft, "", nil
	task.Supersedes, task.CreatedFrom = flow.TaskID, "flow:"+flow.ID
	task.Worktree, task.Branch = successorPath, "work/observed-successor"
	task.Scope, task.TargetSHA = request.Scope, o.Target.CommitSHA
	task.TargetTreeSHA, task.TargetChangedFiles = o.Target.TreeSHA, o.Target.ChangedFiles
	proposal, err := ProposeIntake(db, IntakeRequest{
		Task: task, ObservationHash: o.Hash, Request: "Implement and validate supplied input without claiming prior success",
		Actor: "human", Source: "human:successor",
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := AssessInput(db, proposal.ID, nil)
	if err != nil || decision.Verdict != "accept" || decision.Metadata != "verified" {
		t.Fatal("shared assessment rejected exact observed input", decision, err)
	}
	assessmentState, _ := db.LoadWorkspace()
	assessmentState.Records.Operation = &store.Operation{ID: "other-operation", State: "RUNNING"}
	beforeAssessment := mustFlowJSON(t, assessmentState)
	observed, err := assessObservedInput(assessmentState, proposal, nil)
	if err != nil || observed.Verdict != "accept" ||
		string(beforeAssessment) != string(mustFlowJSON(t, assessmentState)) {
		t.Fatal("read-only assessment was blocked by execution-only quiescence or changed state", observed, err)
	}
	if err := verifyObservedIntake(assessmentState, proposal, o.Hash, nil); err == nil {
		t.Fatal("read-only eligibility was treated as permission to apply during another operation")
	}
	hash, _ := IntakeHash(proposal)
	target, _ := core.ObserveFeedbackTarget(old)
	_, _, _, selected := executionConfigurationFixture(t)
	confirmation := IntakeConfirmation{
		TaskID: proposal.ID, ExpectedHash: hash, Actor: "human", Source: "human:successor",
		Reason:    "Approve a new explicit scope, validation, execution and independent review",
		Selection: selected.Config, ReplacesFlow: flow.ID, Target: &target, ObservationHash: o.Hash,
		AdditionalTaskIDs: []string{"observed-successor-flow-review-0"},
	}
	missing := confirmation
	missing.ObservationHash = ""
	if _, err := ConfirmIntake(db, missing); err == nil {
		t.Fatal("confirmation lost its observation binding")
	}
	approved, err := ConfirmIntake(db, confirmation)
	if err != nil || approved.Status != core.Ready {
		t.Fatal("observed input could not reach an explicitly approved READY contract", err)
	}
	repairs := 0
	next, err := StartReviewFlow(db, ledger, ReviewFlowRequest{
		ID: "observed-successor-flow", TaskID: approved.ID, Repository: approved.Repo,
		ReviewRoot: flow.ReviewRoot, MaxRepairs: &repairs, TimeoutSeconds: 120, MaxNanoAIU: 1e12,
		Authorization: core.TaskDecision{ID: "observed-successor-execution", Source: "human:successor", Text: "Execute one implementation and independent review"},
	})
	if err != nil {
		t.Fatal("approved observation did not connect to normal execution", err)
	}
	issuedState, _ := db.LoadWorkspace()
	issued, _ := issuedState.Graph.GraphDispatch(next.LastDispatchID())
	instruction, err := core.RenderDispatchInstruction(issued)
	if err != nil || !strings.Contains(instruction, "partial Git evidence unavailable") ||
		!strings.Contains(instruction, "unverified-current-input") {
		t.Fatal("the actual Coder instruction lost concrete stopped-result diagnostics", err)
	}
	executeFlowDispatch(t, db, next, "", true)
	next, err = AdvanceReviewFlow(db, ledger, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, next, "APPROVE", true)
	next, err = AdvanceReviewFlow(db, ledger, next.ID)
	if err != nil || next.Status != "COMPLETED" {
		t.Fatal("new validation and independent review did not complete", next, err)
	}
	after, _ := db.LoadWorkspace()
	preserved, _ := after.Graph.GraphDispatch(old.ID)
	preservedFlow, _ := after.Graph.ReviewFlow(flow.ID)
	if !reflect.DeepEqual(old, preserved) || !reflect.DeepEqual(flow, preservedFlow) ||
		index != gitTest(t, flow.Contract.Worktree, "ls-files", "--stage") {
		t.Fatal("successor rewrote old execution, usage, stop or source index")
	}
}

func TestInputObservationAcceptsUnchangedSuppliedInputButNotMalformedMetadata(t *testing.T) {
	db, _, start := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
	state, _ := db.LoadWorkspace()
	task, _ := state.Graph.GraphTask(start.TaskID)
	decision, err := AssessInput(db, task.ID, nil)
	if err != nil || decision.Verdict != "accept" || decision.Metadata != "absent" {
		t.Fatal("ordinary supplied input was forced through a previous role", decision, err)
	}
	task.Decisions = append(task.Decisions, core.TaskDecision{
		ID: "input-observation:" + task.ID, Source: "input-observation", Text: "corrupted",
	})
	invalid, err := assessObservedInput(state, task, nil)
	if err != nil || invalid.Verdict != "blocked" || invalid.Metadata != "invalid" {
		t.Fatal("invalid metadata was downgraded to absent", invalid, err)
	}
	_, _, _, selected := executionConfigurationFixture(t)
	request := InputObservationRequest{
		TaskID: task.ID, Scope: task.Scope, TargetSHA: task.BaselineSHA,
		Configuration: mustFlowJSON(t, selected.Config),
		Actor:         "human", Source: "human:unchanged", Reason: "Observe unchanged supplied input",
		Evidence: []string{"explicit quiescence check"}, NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
	}
	preview, err := ObserveInput(db, request, true)
	if err != nil || len(preview.Observation.Target.ChangedFiles) != 0 {
		t.Fatal("unchanged input could not be observed", err)
	}
	request.ExpectedHash = preview.Observation.Hash
	if _, err := ObserveInput(db, request, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoadWorkspace(); err != nil {
		t.Fatal("unchanged observation was not durably decodable", err)
	}
	again, err := ObserveInput(db, request, false)
	if err != nil || again.StateChanged {
		t.Fatal("exact repeated observation was not idempotent", again, err)
	}
}

func TestInputObservationBlocksUnverifiedReceiptAndForbiddenCapture(t *testing.T) {
	for _, change := range []string{"missing-receipt", "forbidden", "missing-scope", "stale-hash", "omitted-metadata", "active-runner"} {
		t.Run(change, func(t *testing.T) {
			db, _, flow, request := stoppedObservationFixture(t)
			preview, err := ObserveInput(db, request, true)
			if err != nil {
				t.Fatal(err)
			}
			request.ExpectedHash = preview.Observation.Hash
			switch change {
			case "missing-receipt":
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					state.Records.Events = nil
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "forbidden":
				request.Scope = append(request.Scope, "README.md")
				if err := os.WriteFile(filepath.Join(flow.Contract.Worktree, "README.md"), []byte("forbidden"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-scope":
				request.Scope = nil
			case "omitted-metadata":
				request.FlowID, request.DispatchID = "", ""
			case "active-runner":
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					current, _ := state.Graph.ReviewFlow(flow.ID)
					current.RunnerID = "another-owner"
					storeFlow(state, current)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "stale-hash":
				if err := os.WriteFile(filepath.Join(flow.Contract.Worktree, "extras/input.txt"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ObserveInput(db, request, false); err == nil {
				t.Fatal("unsafe observation was recorded")
			}
			state, _ := db.LoadWorkspace()
			if len(state.Records.InputObservations) != 0 {
				t.Fatal("blocked observation changed saved metadata")
			}
		})
	}
}
