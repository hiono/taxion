package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func stageFlowFixture(t *testing.T) (*store.Store, string, ReviewFlowRequest) {
	t.Helper()
	db, ledger, request := reviewFlowFixture(t)
	request.StageBudget = &core.StageBudget{Version: 1, ValidationSeconds: 5, ReviewSeconds: 10, RepairSeconds: 10,
		ReviewNanoAIU: 1, RepairNanoAIU: 1}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewStagePlan(state.Graph, request)
	if err != nil {
		t.Fatal(err)
	}
	request.StagePlanHash = preview.Plan.Hash
	return db, ledger, request
}

type asyncStageRuntime struct {
	fakeRuntime
	sendContext context.Context
}

func (w *asyncStageRuntime) Send(ctx context.Context, _, _ string) error {
	w.sendContext = ctx
	return w.sendErr
}

func TestPlannedSendRetainsContextUntilTurnCompletion(t *testing.T) {
	for _, end := range []string{"result", "failure", "stop", "parent-cancel", "deadline", "send-error", "stream-error"} {
		t.Run(end, func(t *testing.T) {
			db, ledger, request := stageFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			state, _ := db.LoadWorkspace()
			dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
			worker := &asyncStageRuntime{fakeRuntime: fakeRuntime{
				role: "CODER", hash: "test", worktree: dispatch.Instruction.Worktree,
			}}
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if _, err := control.Start(ctx, dispatch.ID); err != nil {
				t.Fatal(err)
			}
			if end == "deadline" {
				// Move only the test clock; the saved stage deadline must bound Send.
				flow.Deadline = time.Now().Add(time.Duration(flow.StagePlan.ReservedSeconds(0, "CODER"))*time.Second + time.Second)
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					storeFlow(state, flow)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if end == "send-error" {
				worker.sendErr = errors.New("send failed")
			}
			err = control.Send(ctx, dispatch.ID, "execute")
			if end == "send-error" {
				if !errors.Is(err, worker.sendErr) {
					t.Fatal("send error was lost", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := worker.sendContext.Err(); err != nil {
					t.Fatal("Send cancelled an asynchronous turn on return", err)
				}
				if deadline, ok := worker.sendContext.Deadline(); !ok || !deadline.Equal(stageDeadline(flow, dispatch, false)) {
					t.Fatal("Send lost the approved stage deadline", deadline)
				}
			}
			switch end {
			case "result":
				result := core.DispatchResult{
					DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
					Repository: dispatch.Repository, Status: core.DispatchFailed,
					StartedSHA: dispatch.ExpectedStartSHA(), Blockers: []string{"test result"},
					NextTransition: core.Blocked,
				}
				events := make(chan runtime.Event, 2)
				worker.events = events
				payload := string(mustFlowJSON(t, result))
				go func() {
					events <- runtime.Event{Type: runtime.EventOutput, Payload: payload}
					events <- runtime.Event{Type: runtime.EventTurnFinished}
					close(events)
				}()
				if err := control.StreamEvents(ctx, dispatch, 0, io.Discard, nil); err != nil {
					t.Fatal("asynchronous result could not be finalized", err)
				}
			case "failure":
				if err := control.Fail(dispatch.ID, "test failure"); err != nil {
					t.Fatal(err)
				}
			case "stop":
				if err := control.Stop(ctx, dispatch.ID); err != nil {
					t.Fatal(err)
				}
			case "parent-cancel":
				cancel()
			case "stream-error":
				if err := control.StreamEvents(ctx, dispatch, 0, nil, nil); err == nil {
					t.Fatal("invalid stream succeeded")
				}
			}
			select {
			case <-worker.sendContext.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("turn completion/cancellation did not release its context")
			}
			if end == "deadline" && !errors.Is(worker.sendContext.Err(), context.DeadlineExceeded) {
				t.Fatal("stage deadline did not expire normally", worker.sendContext.Err())
			}
			if end == "parent-cancel" || end == "deadline" {
				if err := control.Stop(t.Context(), dispatch.ID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPlannedFlowRunsVerifiedRepairAndIndependentReview(t *testing.T) {
	testFlowRunsRepairAndNewSHAReview(t, false, true)
}

func TestPlannedStepPreservesSameApprovalAndClock(t *testing.T) {
	testFlowRunsRepairAndNewSHAReview(t, true, true)
}

func TestStagePlanPreviewIsReadOnlyAndBindsExactEnvelope(t *testing.T) {
	db, ledger, request := stageFlowFixture(t)
	before, _ := db.LoadWorkspace()
	preview, err := PreviewStagePlan(before.Graph, request)
	if err != nil || preview.StateChanged || preview.Plan.Hash != request.StagePlanHash {
		t.Fatal("non-deterministic or mutating preview", preview, err)
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) || len(after.Graph.Dispatches) != 0 {
		t.Fatal("preview issued work or changed saved state")
	}
	for _, mutate := range []func(*ReviewFlowRequest){
		func(r *ReviewFlowRequest) { r.TimeoutSeconds++ },
		func(r *ReviewFlowRequest) { r.StageBudget.ReviewerSessionCredits = 30 },
		func(r *ReviewFlowRequest) { r.Authorization.Text += " changed" },
		func(r *ReviewFlowRequest) { r.MaxNanoAIU++ },
		func(r *ReviewFlowRequest) { r.StagePlanHash = "" },
	} {
		bad := request
		policy := *request.StageBudget
		bad.StageBudget = &policy
		mutate(&bad)
		if _, err := StartReviewFlow(db, ledger, bad); err == nil {
			t.Fatal("changed plan was silently authorized")
		}
	}
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil || flow.StagePlan == nil || flow.StagePlan.Hash != request.StagePlanHash {
		t.Fatal("approved plan did not start", flow, err)
	}
	for _, mutate := range []func(*core.ReviewFlow){
		func(f *core.ReviewFlow) { f.StagePlan = nil },
		func(f *core.ReviewFlow) { f.StagePlan.CoderSessionCredits = 300 },
		func(f *core.ReviewFlow) { f.StagePlan.ValidationSeconds++ },
	} {
		err := db.UpdateWorkspace(func(state *store.Workspace) error {
			saved, _ := state.Graph.ReviewFlow(flow.ID)
			mutate(&saved)
			storeFlow(state, saved)
			return nil
		})
		if err == nil {
			t.Fatal("immutable stage plan was removed or changed")
		}
	}
	report, err := ObserveFlowBudget(after, flow.ID)
	if err == nil || report.Budget.FlowID != "" {
		t.Fatal("observation invented an absent Flow")
	}
}

func TestStagePlanReportsMissingBundleWithoutApprovingExecution(t *testing.T) {
	configuration := approvalTestConfiguration(t, "https://api.example.test")
	graph, task := approvalEntryGraph(t, configuration)
	repairs := 0
	request := ReviewFlowRequest{
		ID: "plan-gate", TaskID: task.ID, Repository: task.Repo, MaxRepairs: &repairs,
		TimeoutSeconds: 3600, Configuration: configuration,
		StageBudget: &core.StageBudget{Version: 1},
	}
	bundles := graph.Approvals
	graph.Approvals = nil
	missing, err := PreviewStagePlan(graph, request)
	if err != nil || missing.StateChanged || missing.ContractGate.Verdict != GateVerdictBlocked ||
		len(missing.ContractGate.BlockingConditions) != 1 ||
		missing.ContractGate.BlockingConditions[0].Code != "missing-approval-bundle" ||
		missing.ContractGate.NextRequiredApproval == "" || missing.ContractGate.StateChanged {
		t.Fatal("plan disguised missing durable approval", missing, err)
	}
	graph.Approvals = bundles
	ready, err := PreviewStagePlan(graph, request)
	if err != nil || ready.ContractGate.Verdict != GateVerdictPass ||
		ready.Plan.Hash != missing.Plan.Hash {
		t.Fatal("recording a matching bundle changed the plan or remained blocked", ready, err)
	}
	request.Configuration = approvalTestConfiguration(t, "https://changed.example.test")
	drifted, err := PreviewStagePlan(graph, request)
	if err != nil || drifted.ContractGate.Verdict == GateVerdictPass ||
		drifted.Plan.Hash == ready.Plan.Hash {
		t.Fatal("configuration drift did not invalidate authorization and plan", drifted, err)
	}
}

func TestStageBudgetFakeClockAndNativeReservations(t *testing.T) {
	db, ledger, request := stageFlowFixture(t)
	request.StageBudget.ReviewNanoAIU, request.StageBudget.RepairNanoAIU = 20, 30
	request.MaxNanoAIU = 100
	state, _ := db.LoadWorkspace()
	preview, err := PreviewStagePlan(state.Graph, request)
	if err != nil {
		t.Fatal(err)
	}
	request.StagePlanHash = preview.Plan.Hash
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = db.LoadWorkspace()
	dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	deadline := stageDeadline(flow, dispatch, false)
	if stageBudgetStop(state.Graph, flow, dispatch, deadline.Add(-time.Nanosecond)) != "" ||
		!strings.Contains(stageBudgetStop(state.Graph, flow, dispatch, deadline), "submission deadline") {
		t.Fatal("submission boundary does not preserve the exact reserved intervals")
	}
	complete, used := "complete", 30.0
	for index := range state.Graph.Dispatches {
		state.Graph.Dispatches[index].RuntimeSessionID = "test"
		state.Graph.Dispatches[index].RuntimeUsage = []core.DispatchUsage{{TotalNanoAIU: &used, AICreditsStatus: &complete}}
	}
	if !strings.Contains(stageBudgetStop(state.Graph, flow, dispatch, flow.StartedAt), "native stage budget") {
		t.Fatal("Coder borrowed reserved 70 native units")
	}
	flow.Budget.MaxNanoAIU = 0
	flow.Budget.BudgetSource = core.ProviderStopBudgetSource
	if stageBudgetStop(state.Graph, flow, dispatch, flow.StartedAt) != "" {
		t.Fatal("provider-stop unexpectedly required native billing")
	}
}

func TestStagePlanRejectsMissingNativeReservationsBeforeDispatch(t *testing.T) {
	db, ledger, request := stageFlowFixture(t)
	state, _ := db.LoadWorkspace()
	for _, missing := range []string{"review", "repair"} {
		bad := request
		policy := *request.StageBudget
		bad.StageBudget = &policy
		if missing == "review" {
			policy.ReviewNanoAIU = 0
		} else {
			policy.RepairNanoAIU = 0
		}
		if _, err := PreviewStagePlan(state.Graph, bad); err == nil || !strings.Contains(err.Error(), "positive reviewNanoAiu") {
			t.Fatal("unknown native reservation silently became zero", missing, err)
		}
		if _, err := StartReviewFlow(db, ledger, bad); err == nil {
			t.Fatal("missing native reservation issued work")
		}
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(state, after) {
		t.Fatal("rejected native plan mutated saved state", err)
	}
	db, ledger, request = reviewFlowFixtureWithPolicy(t, "", budgetPolicy(t, "provider"))
	request.MaxNanoAIU = 0
	request.StageBudget = &core.StageBudget{Version: 1, ValidationSeconds: 5, ReviewSeconds: 10, RepairSeconds: 10}
	state, _ = db.LoadWorkspace()
	preview, err := PreviewStagePlan(state.Graph, request)
	if err != nil || preview.Plan.ReviewNanoAIU != 0 || preview.Plan.RepairNanoAIU != 0 {
		t.Fatal("provider-stop was incorrectly forced to supply native estimates", err)
	}
	request.StagePlanHash = preview.Plan.Hash
	if _, err := StartReviewFlow(db, ledger, request); err != nil {
		t.Fatal("provider-stop stage plan cannot start", err)
	}
}

func TestStagePlanRejectsInfeasibleEstimatesBeforeDispatch(t *testing.T) {
	db, _, request := stageFlowFixture(t)
	state, _ := db.LoadWorkspace()
	request.TimeoutSeconds = 60
	request.StageBudget = &core.StageBudget{Version: 1}
	request.MaxNanoAIU = 0
	if _, err := PreviewStagePlan(state.Graph, request); err == nil {
		t.Fatal("cold estimates were forced into an infeasible short Flow")
	}
	request.TimeoutSeconds = 3600
	preview, err := PreviewStagePlan(state.Graph, request)
	if err != nil || len(preview.Plan.Basis) != 1 || !strings.Contains(preview.Plan.Basis[0], "uncertain") {
		t.Fatal("cold estimates were not explicit", preview, err)
	}
}

func TestPlannedSessionCeilingMismatchRejectedBeforeRuntimeStart(t *testing.T) {
	db, ledger, request := stageFlowFixture(t)
	request.StageBudget.CoderSessionCredits = 300
	state, _ := db.LoadWorkspace()
	preview, err := PreviewStagePlan(state.Graph, request)
	if err != nil {
		t.Fatal(err)
	}
	request.StagePlanHash = preview.Plan.Hash
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = db.LoadWorkspace()
	dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	worker := &fakeRuntime{role: "CODER", hash: "test", worktree: dispatch.Instruction.Worktree}
	control, _ := New(db, worker)
	if _, err := control.Start(t.Context(), dispatch.ID); err == nil || !strings.Contains(err.Error(), "soft ceiling") {
		t.Fatal("mismatched session limit started runtime", err)
	}
	state, _ = db.LoadWorkspace()
	dispatch, _ = state.Graph.GraphDispatch(dispatch.ID)
	if dispatch.RuntimeSessionID != "" || dispatch.ExecutionStarted {
		t.Fatal("runtime work happened before ceiling match")
	}
}

func TestStageDeadlinesStopStreamingAndDirectFinalization(t *testing.T) {
	for _, phase := range []string{"streaming", "late-submission", "validation"} {
		t.Run(phase, func(t *testing.T) {
			commands := []string{"test -s internal/app.go"}
			if phase == "validation" {
				commands = []string{"sleep 2"}
			}
			db, ledger, request := reviewFlowFixtureWithValidations(t, "owner/repo", core.OutputModeWorktreeTreeSnapshot, nil, commands)
			request.StageBudget = &core.StageBudget{Version: 1, ValidationSeconds: 1, ReviewSeconds: 10, RepairSeconds: 10,
				ReviewNanoAIU: 1, RepairNanoAIU: 1}
			state, _ := db.LoadWorkspace()
			preview, err := PreviewStagePlan(state.Graph, request)
			if err != nil {
				t.Fatal(err)
			}
			request.StagePlanHash = preview.Plan.Hash
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			state, _ = db.LoadWorkspace()
			dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
			worker := &scriptedRuntime{fakeRuntime: fakeRuntime{role: "CODER", hash: "test",
				worktree: dispatch.Instruction.Worktree, sessionID: "test-session"},
				events: make(chan runtime.Event), script: func() error { return nil }}
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.Start(t.Context(), dispatch.ID); err != nil {
				t.Fatal(err)
			}
			prompt, err := core.RenderDispatchInstruction(dispatch)
			if err != nil {
				t.Fatal(err)
			}
			if err := control.Send(t.Context(), dispatch.ID, prompt); err != nil {
				t.Fatal(err)
			}
			partialPath := filepath.Join(dispatch.Instruction.Worktree, "internal", "partial.go")
			if err := os.MkdirAll(filepath.Dir(partialPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(partialPath, []byte("partial\n"), 0600); err != nil {
				t.Fatal(err)
			}
			// Shorten only this saved test clock after admission, never the caller's
			// context, so the actual stage boundary drives cancellation.
			room := 200 * time.Millisecond
			if phase == "late-submission" {
				room = -time.Second
			}
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				flow.Deadline = time.Now().Add(time.Duration(flow.StagePlan.ReservedSeconds(0, "CODER"))*time.Second + room)
				storeFlow(state, flow)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			state, _ = db.LoadWorkspace()
			dispatch, _ = state.Graph.GraphDispatch(dispatch.ID)
			if phase == "streaming" {
				err = control.StreamEvents(t.Context(), dispatch, 0, io.Discard, nil)
			} else {
				payload := core.DispatchResult{DispatchID: dispatch.ID, TaskID: dispatch.TaskID,
					Recipient: dispatch.Recipient, Repository: dispatch.Repository,
					Status: core.DispatchSucceeded, StartedSHA: dispatch.ExpectedStartSHA(),
					OutputSHA: dispatch.ExpectedStartSHA(), ChangedFiles: []string{"internal/partial.go"},
					Evidence: []string{"test-report"}, NextTransition: core.WaitingForReview}
				_, err = control.Finish(t.Context(), dispatch.ID, string(mustFlowJSON(t, payload)))
			}
			if err == nil {
				t.Fatal("expired stage completed")
			}
			state, loadErr := db.LoadWorkspace()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			stopped, _ := state.Graph.ReviewFlow(flow.ID)
			saved, _ := state.Graph.GraphDispatch(dispatch.ID)
			if stopped.Status != "STOPPED" || stopped.StopCode != "BUDGET_STOP" ||
				stopped.StopCause != "time-limit" || !stopped.HandoffHeld() ||
				len(state.Graph.Dispatches) != 1 || !time.Now().Before(stopped.Deadline) {
				t.Fatal("stage borrowed the remaining Flow deadline or issued a Reviewer", err, stopped)
			}
			if saved.Result == nil || saved.Result.PartialGitEvidence == nil {
				t.Fatal("stage stop lost the submitted or unsubmitted partial snapshot", saved)
			}
			if phase == "validation" {
				report, observeErr := ObserveFlowBudget(state, flow.ID)
				timedOut := false
				for _, stage := range report.Stages {
					if stage.Phase == "validation-finished" && stage.DurationMilliseconds != nil &&
						strings.Contains(stage.Reason, "deadline") {
						timedOut = true
					}
				}
				if observeErr != nil || !timedOut {
					t.Fatal("validation was not actually run through its reserved deadline", observeErr, report.Stages)
				}
			}
		})
	}
}
func TestPlannedDeadlineStopsAndPreservesPartialWithoutReviewer(t *testing.T) {
	db, ledger, request := stageFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	var worker *scriptedRuntime
	path := filepath.Join(flow.Contract.Worktree, "internal", "partial.go")
	factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		worker = &scriptedRuntime{fakeRuntime: fakeRuntime{role: "CODER", hash: "test",
			worktree: dispatch.Instruction.Worktree, sessionID: "test-session"}, events: make(chan runtime.Event)}
		worker.script = func() error {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			return os.WriteFile(path, []byte("partial\n"), 0600)
		}
		return worker, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	stopped, err := RunReviewFlow(ctx, db, ledger, flow.ID, factory, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) || stopped.Status != "STOPPED" ||
		stopped.StopCode != "BUDGET_STOP" || stopped.StopCause != "time-limit" || stopped.RunnerID != "" {
		t.Fatalf("deadline was not classified and saved: %+v %v", stopped, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	if worker == nil || worker.stops != 1 || dispatch.Status != core.DispatchCancelled ||
		dispatch.Result == nil || dispatch.Result.PartialGitEvidence == nil ||
		len(state.Graph.Dispatches) != 1 || !stopped.HandoffHeld() {
		t.Fatal("partial work or safe-stop evidence was lost", dispatch)
	}
	before, _ := json.Marshal(state)
	report, err := ObserveFlowBudget(state, flow.ID)
	if err != nil || report.StateChanged || report.Continuation.SavedTree == "" ||
		len(report.Stages) < 2 || report.Continuation.IdentityStatus == "verified" {
		t.Fatal("read-only stop observation is incomplete", report, err)
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, mustFlowJSON(t, after)) {
		t.Fatal("stop observation mutated saved work")
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "partial\n" {
		t.Fatal("partial worktree was changed", err)
	}
}
