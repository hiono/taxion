package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type savedReviewRuntime struct {
	*configuredRuntime
	turn     runtime.SavedTurn
	closeErr error
	reads    int
}

func (w *savedReviewRuntime) ReadSavedTurn(context.Context, string) (runtime.SavedTurn, error) {
	w.reads++
	return w.turn, nil
}

func (w *savedReviewRuntime) Close() error { return w.closeErr }

func TestSavedReviewFinalizationRejoinsNormalValidationAfterRunnerLoss(t *testing.T) {
	for _, phase := range []string{"native-only", "checkpoint-only", "validation-unknown", "validated", "validated-shutdown-usage", "validated-shutdown-usage-retry", "runtime-closed", "worktree-released"} {
		t.Run(phase, func(t *testing.T) {
			testSavedReviewFinalization(t, phase)
		})
	}
}

func testSavedReviewFinalization(t *testing.T, phase string) {
	checkpointOnly := phase == "checkpoint-only"
	counter := filepath.Join(t.TempDir(), "validations")
	db, ledger, request, selected := artifactFixture(t, []string{"printf x >> " + counter}, false)
	request.StageBudget.ValidationSeconds, request.StageBudget.ReviewSeconds = 1, 1
	request.MaxNanoAIU = 1000
	refreshArtifactPlan(t, db, &request)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := PrepareReview(db, flow.LastDispatchID(), flow.Contract.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindConfiguration(db, dispatch.ID, selected); err != nil {
		t.Fatal(err)
	}
	worker := &savedReviewRuntime{configuredRuntime: &configuredRuntime{
		fakeRuntime: &fakeRuntime{role: "REVIEWER", hash: "saved-review", worktree: dispatch.Instruction.Worktree},
		selection:   mustFlowJSON(t, selected.Config),
	}}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), dispatch.ID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), dispatch.ID, "execute saved review"); err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	dispatch, _ = state.Graph.GraphDispatch(dispatch.ID)
	anchor := runtime.Event{Type: runtime.EventExecution, Payload: `{"phase":"tool-complete","callId":"observed-read","success":true}`, Sequence: 1}
	if err := control.recordEvent(dispatch.ID, "original-stream", anchor); err != nil {
		t.Fatal(err)
	}
	input, cost := int64(10), float64(100)
	complete := "complete"
	usage := core.DispatchUsage{Model: "native-model", InputTokens: &input, TotalNanoAIU: &cost, AICreditsStatus: &complete}
	if !checkpointOnly {
		if err := control.recordEvent(dispatch.ID, "original-stream", runtime.Event{
			Type: runtime.EventUsage, Payload: string(mustFlowJSON(t, usage)), Sequence: 2,
		}); err != nil {
			t.Fatal(err)
		}
	} else {
		zero := int64(0)
		usage.OutputTokens = &zero
	}
	unchanged := false
	payload := string(mustFlowJSON(t, core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: "REVIEWER", Repository: dispatch.Repository,
		Status: core.DispatchSucceeded, StartedSHA: dispatch.ExecutionStartSHA, OutputSHA: dispatch.ExecutionStartSHA,
		ChangedFiles: []string{}, Evidence: []string{"test-report"}, WorktreeChanged: &unchanged,
		NextTransition: core.WaitingForReview, ReviewVerdict: "REQUEST_CHANGES",
		Blockers: []string{"Documentation requires correction before approval"},
		Findings: []core.DispatchFinding{{Severity: "HIGH", Title: "Correct stale documentation",
			File: "internal/app.go", Lines: "1", Evidence: []string{"test-report"}}},
		Acceptance: []core.DispatchAcceptance{{Criterion: flow.Contract.Acceptance[0], Status: "FAIL", Evidence: []string{"test-report"}}},
	}))
	now := time.Now().UTC()
	worker.turn = runtime.SavedTurn{
		SessionID: dispatch.RuntimeSessionID, Payload: payload, SubmittedAt: now, FinishedAt: now,
		HistoryHash: "sha256:provider-history",
		Events:      []runtime.Event{anchor, {Type: runtime.EventOutput, Payload: payload}},
		UsageTotals: []core.DispatchUsage{usage},
	}
	if checkpointOnly {
		worker.turn.UsageCheckpoints = []core.UsageCheckpoint{{
			UsageTotals: []core.DispatchUsage{usage}, TotalNanoAIU: &cost, AICreditsStatus: &complete,
			Watermarks: map[string]int64{dispatch.RuntimeSessionID: 1},
		}}
	}
	recovery := RecoveryRequest{
		FlowID: flow.ID, ExpectedRunnerID: "exited-runner", PreviousProcessStopped: true,
		Actor: "controller", Reason: "reconcile the observed ended session", Evidence: []string{"process-exit:confirmed"},
		FinalizeSavedResult: true, ValidationDeadline: now.Add(time.Minute),
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].RunnerID = recovery.ExpectedRunnerID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	invalidTurns := []func(){
		func() { worker.turn.SessionID = "another-session" },
		func() { worker.turn.SubmittedAt = flow.Deadline.Add(time.Second) },
		func() { worker.turn.Events[0].Payload = "unrelated execution" },
		func() { recovery.ExpectedRunnerID = "another-runner" },
	}
	if checkpointOnly {
		for _, change := range []string{"lower-cost", "higher-cost", "lower-tokens"} {
			invalidTurns = append(invalidTurns, func() {
				total := usage
				switch change {
				case "lower-cost":
					value := cost - 1
					total.TotalNanoAIU = &value
				case "higher-cost":
					value := cost + 1
					total.TotalNanoAIU = &value
				case "lower-tokens":
					value := input - 1
					total.InputTokens = &value
				}
				worker.turn.UsageTotals = []core.DispatchUsage{total}
			})
		}
	}
	for _, invalid := range invalidTurns {
		originalTurn, originalRecovery := worker.turn, recovery
		originalTurn.Events = append([]runtime.Event(nil), worker.turn.Events...)
		before, _ := db.LoadWorkspace()
		invalid()
		if _, err := control.RecoverSavedReview(t.Context(), recovery); err == nil {
			t.Fatal("unbound saved result was accepted")
		}
		after, _ := db.LoadWorkspace()
		if !reflect.DeepEqual(before, after) {
			t.Fatal("rejected saved observation changed durable state")
		}
		worker.turn, recovery = originalTurn, originalRecovery
	}
	if phase == "validation-unknown" {
		if err := control.observeStageIfFlow(dispatch.ID, "validation-starting", "process exited before a result receipt"); err != nil {
			t.Fatal(err)
		}
		before, _ := db.LoadWorkspace()
		if _, err := control.RecoverSavedReview(t.Context(), recovery); err == nil {
			t.Fatal("unknown host validation was silently replayed")
		}
		after, _ := db.LoadWorkspace()
		count, _ := os.ReadFile(counter)
		if !reflect.DeepEqual(before, after) || string(count) != "x" {
			t.Fatal("unknown host validation changed state or re-executed commands")
		}
		return
	}
	if phase != "native-only" && !checkpointOnly {
		worker.closeErr = errors.New("simulated process loss after durable validation")
		if _, err := control.Finish(t.Context(), dispatch.ID, payload); !errors.Is(err, worker.closeErr) {
			t.Fatal("expected interruption at runtime close, got", err)
		}
		worker.closeErr = nil
		state, _ = db.LoadWorkspace()
		dispatch, _ = state.Graph.GraphDispatch(dispatch.ID)
		pending, err := savedReviewFinalization(state, dispatch)
		if err != nil || pending == nil || pending.RuntimeClosed {
			t.Fatal("validated result was not durable before runtime close", err)
		}
		if phase == "runtime-closed" || phase == "worktree-released" {
			if err := control.recordReviewFinalization(dispatch, pending.Result, true); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "worktree-released" {
			if err := gitproof.ReleaseReviewWorktree(dispatch.ReviewWorktreeSource, dispatch, pending.Result); err != nil {
				t.Fatal(err)
			}
		}
	}
	if phase == "native-only" || phase == "validated-shutdown-usage" || phase == "validated-shutdown-usage-retry" {
		input, cost = 15, 150
	}
	reads := worker.reads
	// The provider completed on time, but the operator observes the orphan
	// after the old execution window. Only host validation gets a new window.
	oldDeadline := now.Add(1100 * time.Millisecond)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].Deadline = oldDeadline
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(oldDeadline) + 10*time.Millisecond)
	if phase == "validated-shutdown-usage-retry" {
		worker.closeErr = errors.New("simulated repeated interruption after shutdown usage reconciliation")
		if _, err := control.RecoverSavedReview(t.Context(), recovery); !errors.Is(err, worker.closeErr) {
			t.Fatal("shutdown usage prevented resuming the saved validated result", err)
		}
		worker.closeErr = nil
	}
	recovered, err := control.RecoverSavedReview(t.Context(), recovery)
	if err != nil || recovered.RunnerID != "" || recovered.MaxRepairs != 0 ||
		!recovered.Deadline.Equal(oldDeadline) {
		t.Fatalf("same finalizer did not recover the saved review: %+v %v", recovered, err)
	}
	state, _ = db.LoadWorkspace()
	saved, _ := state.Graph.GraphDispatch(dispatch.ID)
	if saved.Status != core.DispatchSucceeded || saved.Result == nil ||
		saved.Result.ReviewVerdict != "REQUEST_CHANGES" || !saved.Result.GitEvidence.WorktreeReleased ||
		validateResultReceipt(&state, saved) != nil {
		t.Fatal("review did not pass through formal result validation and worktree release")
	}
	var nativeCost float64
	for _, usage := range saved.RuntimeUsage {
		if usage.TotalNanoAIU != nil {
			nativeCost += *usage.TotalNanoAIU
		}
	}
	if checkpointOnly {
		summary, err := core.SummarizeUsage(saved.RuntimeSessionID, saved.RuntimeUsage, saved.RuntimeUsageCheckpoints)
		if err != nil || summary.NanoAIU == nil || *summary.NanoAIU != cost ||
			summary.CreditsStatus != "complete" || len(saved.RuntimeUsage) != 0 || len(saved.RuntimeUsageCheckpoints) != 1 {
			t.Fatal("saved checkpoint was lost or converted into a synthetic call", err)
		}
		probe := saved
		probe.RuntimeUsageCheckpoints = nil
		if _, err := savedReviewFinalization(state, probe); err == nil {
			t.Fatal("closed finalization did not bind checkpoint observations")
		}
	} else if nativeCost != cost {
		t.Fatal("saved usage was reset or missing shutdown consumption", nativeCost, cost)
	}
	if !checkpointOnly {
		changedUsage := append([]core.DispatchUsage(nil), saved.RuntimeUsage...)
		changedUsage[0].Model = "changed-history"
		for _, invalidUsage := range [][]core.DispatchUsage{
			changedUsage, saved.RuntimeUsage[:0],
			append(append([]core.DispatchUsage(nil), saved.RuntimeUsage...), core.DispatchUsage{Model: "after-close"}),
		} {
			probe := saved
			probe.RuntimeUsage = invalidUsage
			if _, err := savedReviewFinalization(state, probe); err == nil {
				t.Fatal("closed usage accepted a changed prefix, truncation or later append")
			}
		}
	}
	count, _ := os.ReadFile(counter)
	if string(count) != "xx" {
		t.Fatal("input validation was replayed or result validation was omitted", string(count))
	}
	if (phase == "runtime-closed" || phase == "worktree-released") && worker.reads != reads {
		t.Fatal("durable closed result unnecessarily reattached to the provider")
	}
	if _, err := control.RecoverSavedReview(t.Context(), recovery); err == nil {
		t.Fatal("recovery was executed twice")
	}
	stopped, err := AdvanceReviewFlow(db, ledger, flow.ID)
	if !errors.Is(err, ErrFlowStopped) || stopped.Status != "STOPPED" || stopped.MaxRepairs != 0 ||
		len(stopped.DispatchIDs) != 1 {
		t.Fatal("saved findings silently authorized a repair", stopped, err)
	}
}

func TestSavedReviewShutdownUsageReconciliationKeepsEarlierReceipts(t *testing.T) {
	input, cost := int64(10), float64(100)
	totalInput, totalCost := int64(15), float64(150)
	dispatch := core.GraphDispatch{RuntimeUsage: []core.DispatchUsage{{
		Model: "native-model", InputTokens: &input, TotalNanoAIU: &cost,
	}}}
	totals := []core.DispatchUsage{{Model: "native-model", InputTokens: &totalInput, TotalNanoAIU: &totalCost}}
	for range 2 {
		if err := reconcileSavedUsage(&dispatch, totals, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(dispatch.RuntimeUsage) != 2 || *dispatch.RuntimeUsage[0].InputTokens != 10 ||
		*dispatch.RuntimeUsage[1].InputTokens != 5 || *dispatch.RuntimeUsage[1].TotalNanoAIU != 50 ||
		totalInput != 15 || totalCost != 150 {
		t.Fatal("shutdown reconciliation changed old receipts, doubled usage or mutated the native totals")
	}
	totalInput = 9
	if err := reconcileSavedUsage(&dispatch, totals, time.Now()); err == nil {
		t.Fatal("provider totals silently reduced saved consumption")
	}
}

func TestRecoveryRequiresStoppedExecutionAndExactOwner(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].RunnerID = "interrupted-runner"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recovery := RecoveryRequest{
		FlowID: flow.ID, ExpectedRunnerID: "interrupted-runner", PreviousProcessStopped: true,
		Actor: "human", Reason: "Original process ended; reconcile recorded execution",
		Evidence: []string{"process-stop:checked"},
	}
	if _, err := RecoverReviewFlow(db, recovery); err == nil {
		t.Fatal("recovery released owner of an active Dispatch")
	}
	dispatchID := flow.DispatchIDs[0]
	control, err := New(db, &fakeRuntime{role: "CODER", hash: "test", worktree: flow.Contract.Worktree})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), dispatchID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), dispatchID, "execute"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.TransitionDispatch(dispatchID, core.DispatchCancelRequested,
			"runtime", "cancel requested before process exit", false, false, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	worker := &fakeRuntime{role: "CODER", hash: "test", worktree: flow.Contract.Worktree}
	control, err = New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverReviewFlow(db, recovery); err == nil {
		t.Fatal("CANCEL_REQUESTED was accepted as confirmed stop")
	}
	if err := control.Resume(t.Context(), dispatchID); err == nil {
		t.Fatal("cancel request authorized normal execution resume")
	}
	if err := control.Stop(t.Context(), dispatchID); err != nil || worker.resumes != 1 || worker.stops != 1 {
		t.Fatalf("restart did not attach solely for safe stop: %v %+v", err, worker)
	}
	for _, change := range []func(*RecoveryRequest){
		func(r *RecoveryRequest) { r.ExpectedRunnerID = "stale-runner" },
		func(r *RecoveryRequest) { r.PreviousProcessStopped = false },
		func(r *RecoveryRequest) { r.Actor = "" },
		func(r *RecoveryRequest) { r.Reason = "" },
		func(r *RecoveryRequest) { r.Evidence = nil },
		func(r *RecoveryRequest) { r.Evidence = []string{"same", "same"} },
		func(r *RecoveryRequest) { r.Evidence = []string{" "} },
	} {
		invalid := recovery
		change(&invalid)
		if _, err := RecoverReviewFlow(db, invalid); err == nil {
			t.Fatal("invalid recovery confirmation accepted")
		}
	}
	recovery.Evidence = append(recovery.Evidence, "runtime-stop:sdk-session")
	recovered, err := RecoverReviewFlow(db, recovery)
	if err != nil || recovered.RunnerID != "" || recovered.Status != "RUNNING" ||
		len(recovered.Recoveries) != 1 || recovered.Recoveries[0].RunnerID != recovery.ExpectedRunnerID ||
		!reflect.DeepEqual(recovered.Recoveries[0].Evidence, recovery.Evidence) {
		t.Fatalf("ownership not recovered with provenance: %+v %v", recovered, err)
	}
	recovery.Evidence[0] = "mutated"
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := state.Graph.ReviewFlow(flow.ID)
	if saved.Recoveries[0].Evidence[0] != "process-stop:checked" {
		t.Fatal("saved recovery evidence aliased the input")
	}
	saved.Recoveries[0].Evidence[0] = "mutated snapshot"
	again, _ := state.Graph.ReviewFlow(flow.ID)
	if again.Recoveries[0].Evidence[0] != "process-stop:checked" {
		t.Fatal("recovery snapshot aliased the graph")
	}
	if _, err := RecoverReviewFlow(db, recovery); err == nil {
		t.Fatal("stale recovery was replayed")
	}
	// Releasing ownership does not authorize retry of cancelled work.
	stopped, err := AdvanceReviewFlow(db, ledger, flow.ID)
	if !errors.Is(err, ErrFlowStopped) || stopped.Status != "STOPPED" || len(stopped.DispatchIDs) != 1 {
		t.Fatalf("cancelled execution restarted after recovery: %+v %v", stopped, err)
	}
}

func TestRecoveryRevalidatesCoderGitAfterRestart(t *testing.T) {
	for _, change := range []string{"head", "dirty", "origin", "missing", "evidence"} {
		t.Run(change, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			result := executeFlowDispatch(t, db, flow, "", true)
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				state.Graph.ReviewFlows[0].RunnerID = "interrupted-after-result"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "head":
				gitTest(t, flow.Contract.Worktree, "commit", "--allow-empty", "-m", "chore: external change")
			case "dirty":
				if err := os.WriteFile(filepath.Join(flow.Contract.Worktree, "internal/app.go"), []byte("uncommitted"), 0600); err != nil {
					t.Fatal(err)
				}
			case "origin":
				gitTest(t, flow.Contract.Worktree, "remote", "set-url", "origin", "https://github.com/other/repo.git")
			case "missing":
				if err := os.Rename(flow.Contract.Worktree, flow.Contract.Worktree+"-moved"); err != nil {
					t.Fatal(err)
				}
			case "evidence":
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					state.Graph.Dispatches[0].Result.GitEvidence.ParentSHAs = []string{result.Result.OutputSHA}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			recovered, err := RecoverReviewFlow(db, RecoveryRequest{
				FlowID: flow.ID, ExpectedRunnerID: "interrupted-after-result", PreviousProcessStopped: true,
				Actor: "human", Reason: "Reconcile recorded result after restart", Evidence: []string{"process-stop:checked"},
			})
			if err == nil || recovered.RunnerID != "interrupted-after-result" {
				t.Fatalf("changed Git evidence accepted: %+v %v", recovered, err)
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before.Graph, after.Graph) {
				t.Fatalf("failed recovery changed graph: %v", err)
			}
			stopped, err := advanceReviewFlow(db, ledger, flow.ID, "interrupted-after-result", core.ApprovalConfiguration{})
			if err == nil || stopped.Status != "STOPPED" || len(stopped.DispatchIDs) != 1 {
				t.Fatalf("handoff accepted changed Git evidence: %+v %v", stopped, err)
			}
		})
	}
}

func TestRecoveryPreservesSuccessfulResultForHandoff(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	result := executeFlowDispatch(t, db, flow, "", true)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].RunnerID = "interrupted-after-result"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := RecoverReviewFlow(db, RecoveryRequest{
		FlowID: flow.ID, ExpectedRunnerID: "interrupted-after-result", PreviousProcessStopped: true,
		Actor: "human", Reason: "Result persisted before process exit",
		Evidence: []string{"process-stop:checked", "dispatch:" + result.ID},
	}); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 || len(flow.Recoveries) != 1 {
		t.Fatalf("successful handoff lost after recovery: %+v %v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	review, _ := state.Graph.GraphDispatch(flow.DispatchIDs[1])
	if review.Recipient != "REVIEWER" || review.Instruction.TargetSHA != result.Result.OutputSHA {
		t.Fatal("recovery bypassed exact-SHA independent review")
	}
	saved, _ := state.Graph.GraphDispatch(result.ID)
	if !reflect.DeepEqual(saved.Result, result.Result) {
		t.Fatal("revalidation changed original Coder result")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].Recoveries[0].Evidence = nil
		return nil
	}); !errors.Is(err, core.ErrGraphRejected) {
		t.Fatal("invalid recovery history persisted", err)
	}
}
