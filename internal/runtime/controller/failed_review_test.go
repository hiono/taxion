package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func TestFailedReviewSupplementPreservesFailureAndNeverReplaysValidation(t *testing.T) {
	for _, mode := range []string{"normal", "artifact"} {
		t.Run(mode, func(t *testing.T) { testFailedReviewSupplement(t, mode) })
	}
}

func testFailedReviewSupplement(t *testing.T, mode string, publicCLI ...bool) {
	counter := filepath.Join(t.TempDir(), "validations")
	owners := []string{"CODER"}
	if len(publicCLI) != 0 {
		owners = append(owners, "native-cli")
	}
	db, ledger, start, selected := artifactFixture(t, []string{"printf x >> " + counter}, mode == "normal", owners...)
	if mode == "normal" {
		start.ArtifactSource = ""
		start.StageBudget.ReviewNanoAIU = 1
		state, _ := db.LoadWorkspace()
		plan, err := PreviewStagePlan(state.Graph, start)
		if err != nil {
			t.Fatal(err)
		}
		start.StagePlanHash = plan.Plan.Hash
	}
	flow, err := StartReviewFlow(db, ledger, start)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "artifact" {
		flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, start.Configuration)
	} else {
		executeFlowDispatch(t, db, flow, "", true, selected)
		flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	}
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
		fakeRuntime: &fakeRuntime{role: "REVIEWER", hash: "saved-review", worktree: dispatch.Instruction.Worktree, bound: true},
		selection:   mustFlowJSON(t, selected.Config),
	}}
	if len(publicCLI) != 0 {
		worker.fakeRuntime.hash = strings.Repeat("a", 64)
	}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), dispatch.ID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), dispatch.ID, "execute review"); err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	dispatch, _ = state.Graph.GraphDispatch(dispatch.ID)
	anchor := runtime.Event{Type: runtime.EventExecution, Payload: `{"phase":"tool-complete","callId":"read-original","success":true}`, Sequence: 1}
	if err := control.recordEvent(dispatch.ID, "original-stream", anchor); err != nil {
		t.Fatal(err)
	}
	input, totalInput := int64(10), int64(15)
	if err := control.RecordUsage(dispatch.ID, core.DispatchUsage{Model: "native-model", InputTokens: &input}); err != nil {
		t.Fatal(err)
	}
	unchanged := false
	answer := core.DispatchResult{
		Status: core.DispatchSucceeded, Evidence: []string{"test-report"}, WorktreeChanged: &unchanged,
		ChangedFiles: []string{}, NextTransition: core.WaitingForReview, ReviewVerdict: "REQUEST_CHANGES",
		Findings:   []core.DispatchFinding{{Severity: "HIGH", Title: "Correct documentation", Evidence: []string{"test-report"}}},
		Acceptance: []core.DispatchAcceptance{{Criterion: flow.Contract.Acceptance[0], Status: "FAIL", Evidence: []string{"test-report"}}},
	}
	now := time.Now().UTC()
	worker.turn = runtime.SavedTurn{
		SessionID: dispatch.RuntimeSessionID, Payload: string(mustFlowJSON(t, answer)),
		SubmittedAt: now, FinishedAt: now, ShutdownAt: now, HistoryHash: "sha256:" + strings.Repeat("a", 64),
		Events: []runtime.Event{anchor}, UsageTotals: []core.DispatchUsage{{Model: "native-model", InputTokens: &totalInput}},
	}
	if err := control.Fail(dispatch.ID, "ECONNRESET after saved routine shutdown"); err != nil {
		t.Fatal(err)
	}
	// Even an interrupted host validation must not be replayed by this operation.
	if err := control.observeStageIfFlow(dispatch.ID, "validation-starting", "unknown outcome"); err != nil {
		t.Fatal(err)
	}
	request := FailedReviewRequest{
		FlowID: flow.ID, DispatchID: dispatch.ID, PreviousProcessStopped: true,
		Actor: "human", Source: "human:negative-answer-approval", Reason: "retain original findings without accepting quality",
		Evidence: []string{"process-exit:confirmed"},
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		other, _ := state.Graph.GraphTask(dispatch.TaskID)
		other.ID, other.Status, other.CreatedFrom = "unrelated-task", core.Draft, "human:unrelated-work"
		other.Worktree = filepath.Join(t.TempDir(), "unrelated-review")
		other.DeferredReason, other.ParentID, other.DependsOn, other.HumanDecision = "", "", nil, nil
		other.MemoryRefs = nil
		if err := state.Graph.Track(other, "human", "separate authorized work"); err != nil {
			return err
		}
		if err := state.Graph.Transition(other.ID, core.Ready, "human", "separate work ready", core.TransitionProof{}); err != nil {
			return err
		}
		unrelated, err := core.NewGraphDispatch(state.Graph, "unrelated-dispatch", other.ID, other.Repo,
			core.RelationshipNone, "", "", nil)
		if err != nil {
			return err
		}
		if err := state.Graph.IssueDispatch(unrelated, "controller", "separate authorized work"); err != nil {
			return err
		}
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
			ID: other.ID, Title: other.Title, Repo: other.Repo, CreatedAt: time.Now().UTC(),
		})
		state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{
			ID: unrelated.ID, TaskID: other.ID, Repo: other.Repo, Active: true, CreatedAt: time.Now().UTC(),
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := db.LoadWorkspace()
	failed, _ := before.Graph.GraphDispatch(dispatch.ID)
	failureAt, err := before.Records.FailedReviewReceiptAt(failed)
	if err != nil {
		t.Fatal(err)
	}
	initialTurn, initialRequest, initialAnswer := worker.turn, request, answer
	if len(publicCLI) != 0 {
		testFailedReviewPublicCLI(t, db, ledger, flow, dispatch, request, worker.turn)
		if count, err := os.ReadFile(counter); err != nil || string(count) != "x" {
			t.Fatal("public CLI replayed product validation", string(count), err)
		}
		return
	}
	for name, change := range map[string]func(){
		"wrong-session":         func() { worker.turn.SessionID = "another-session" },
		"late":                  func() { worker.turn.FinishedAt = flow.Deadline.Add(time.Second) },
		"missing-shutdown-time": func() { worker.turn.ShutdownAt = time.Time{} },
		"shutdown-before-end":   func() { worker.turn.ShutdownAt = now.Add(-time.Second) },
		"turn-after-failure": func() {
			worker.turn.FinishedAt = failureAt.Add(time.Nanosecond)
			worker.turn.ShutdownAt = worker.turn.FinishedAt
		},
		"future-shutdown": func() { worker.turn.ShutdownAt = time.Now().Add(time.Minute) },
		"late-shutdown":   func() { worker.turn.ShutdownAt = flow.Deadline.Add(time.Second) },
		"wrong-history":   func() { worker.turn.Events = []runtime.Event{{Type: runtime.EventExecution, Payload: "unrelated"}} },
		"missing-history": func() { worker.turn.HistoryHash = "" },
		"lower-usage": func() {
			lower := int64(9)
			worker.turn.UsageTotals = []core.DispatchUsage{{Model: "native-model", InputTokens: &lower}}
		},
		"credential-in-answer": func() { worker.turn.Payload = `{"workReport":"Authorization: Bearer private-token"}` },
		"approve": func() {
			answer.ReviewVerdict = "APPROVE"
			worker.turn.Payload = string(mustFlowJSON(t, answer))
		},
		"caller-identity": func() {
			answer.DispatchID = "other-dispatch"
			worker.turn.Payload = string(mustFlowJSON(t, answer))
		},
		"caller-proof": func() {
			answer.GitEvidence = &core.DispatchGitEvidence{}
			worker.turn.Payload = string(mustFlowJSON(t, answer))
		},
		"missing-criterion": func() {
			answer.Acceptance = nil
			worker.turn.Payload = string(mustFlowJSON(t, answer))
		},
		"no-stop-confirmation": func() { request.PreviousProcessStopped = false },
		"wrong-owner":          func() { request.ExpectedRunnerID = "another-owner" },
		"missing-source":       func() { request.Source = "" },
	} {
		t.Run(name, func(t *testing.T) {
			change()
			_, err := control.RecordFailedReview(t.Context(), request, true)
			if err == nil {
				t.Fatal("invalid negative answer was accepted")
			}
			if name == "turn-after-failure" {
				var diagnosis *FailedReviewTimingError
				if !errors.As(err, &diagnosis) || diagnosis.FailedResultSource != "verified-result.at" ||
					!diagnosis.Timing.FailedResultAt.Equal(failureAt) || diagnosis.DeadlineSource != "flow.deadline" ||
					!diagnosis.Timing.SubmissionDeadline.Equal(stageDeadline(flow, failed, false)) ||
					diagnosis.RemainingEligibility != "NOT_EVALUATED" {
					t.Fatal("missing exact non-secret timing diagnosis", err)
				}
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected observation changed state")
			}
			worker.turn, request, answer = initialTurn, initialRequest, initialAnswer
		})
	}
	worker.turn.ShutdownAt = failureAt.Add(time.Nanosecond)
	preview, err := control.RecordFailedReview(t.Context(), request, true)
	if err != nil || preview.StateChanged || preview.QualityAccepted || preview.ExecutionAuthorized ||
		preview.AccountingReconciled || preview.DispatchStatus != core.DispatchFailed {
		t.Fatal("preview failed or claimed quality/execution/accounting", preview, err)
	}
	alternateTimeoutPreview, err := control.RecordFailedReviewWithTimeout(t.Context(), request, true, time.Minute)
	if err != nil || alternateTimeoutPreview.Hash != preview.Hash {
		t.Fatal("observation timeout changed the legacy approval hash", alternateTimeoutPreview.Hash, preview.Hash, err)
	}
	originalRuntime := control.runtime
	blocking := &blockingFailedReviewRuntime{configuredFailedReviewIdentity: worker}
	control.runtime = blocking
	_, timeoutErr := control.RecordFailedReviewWithTimeout(t.Context(), request, true, time.Minute)
	control.runtime = originalRuntime
	var timeoutFailure *FailedReviewOperationError
	if !errors.Is(timeoutErr, context.DeadlineExceeded) || !errors.As(timeoutErr, &timeoutFailure) ||
		timeoutFailure.Phase != "native-read" || timeoutFailure.Persistence != "NOT_WRITTEN" ||
		timeoutFailure.ObservationTimeout != "1m0s" || timeoutFailure.EffectiveDeadline.IsZero() {
		t.Fatalf("native read timeout did not produce safe phase diagnostics: failure=%+v err=%v", timeoutFailure, timeoutErr)
	}
	control.runtime = blocking
	parentCtx, cancelParent := context.WithTimeout(t.Context(), 30*time.Second)
	parentDeadline, _ := parentCtx.Deadline()
	_, timeoutErr = control.RecordFailedReviewWithTimeout(parentCtx, request, true, time.Minute)
	cancelParent()
	if !errors.Is(timeoutErr, context.DeadlineExceeded) || !blocking.deadline.Equal(parentDeadline) {
		t.Fatal("operation timeout overrode the earlier caller deadline", timeoutErr)
	}
	control.runtime = originalRuntime
	afterPreview, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, afterPreview) {
		t.Fatal("preview changed workspace")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		other, _ := state.Graph.GraphTask("unrelated-task")
		other.ID, other.Status, other.CreatedFrom = "unrelated-followup", core.Draft, "human:unrelated-followup"
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
			ID: other.ID, Title: other.Title, Repo: other.Repo, CreatedAt: time.Now().UTC(),
		})
		return state.Graph.Track(other, "human", "independent planning")
	}); err != nil {
		t.Fatal(err)
	}
	afterIndependentProgress, err := control.RecordFailedReview(t.Context(), request, true)
	if err != nil || afterIndependentProgress.Hash != preview.Hash {
		t.Fatal("unrelated progress changed negative-evidence consent", err)
	}
	before, _ = db.LoadWorkspace()
	request.ExpectedHash = strings.Repeat("b", 64)
	if _, err := control.RecordFailedReview(t.Context(), request, false); err == nil {
		t.Fatal("stale preview was applied")
	}
	request.ExpectedHash = preview.Hash
	request.Reason += " changed after preview"
	if _, err := control.RecordFailedReview(t.Context(), request, false); err == nil {
		t.Fatal("changed approval request reused an old preview")
	}
	request.Reason = initialRequest.Reason
	worker.turn.HistoryHash = "sha256:" + strings.Repeat("c", 64)
	if _, err := control.RecordFailedReview(t.Context(), request, false); err == nil {
		t.Fatal("changed native history reused an old preview")
	}
	worker.turn.HistoryHash = initialTurn.HistoryHash
	unexpected := filepath.Join(dispatch.Instruction.Worktree, "unexpected.txt")
	if err := os.WriteFile(unexpected, []byte("external mutation"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := control.RecordFailedReview(t.Context(), request, true); err == nil {
		t.Fatal("changed review candidate accepted")
	}
	if err := os.Remove(unexpected); err != nil {
		t.Fatal(err)
	}
	starts, sends, stops := worker.starts, len(worker.sends), worker.stops
	applied, err := control.RecordFailedReview(t.Context(), request, false)
	if err != nil || !applied.StateChanged {
		t.Fatal("valid supplement was not saved", err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || len(after.Records.FailedReviewSupplements) != 1 {
		t.Fatal("supplement did not survive reload", err)
	}
	for _, invalid := range []string{"completion-after-failure", "shutdown-after-record", "missing-failure-receipt", "wrong-failure-receipt"} {
		records := after.Records
		records.Events = append([]store.RuntimeReceipt(nil), records.Events...)
		records.FailedReviewSupplements = append([]store.FailedReviewSupplement(nil), records.FailedReviewSupplements...)
		supplement := &records.FailedReviewSupplements[0]
		if invalid == "completion-after-failure" {
			supplement.FinishedAt = failureAt.Add(time.Nanosecond)
			supplement.ShutdownAt = supplement.FinishedAt
			supplement.At = failureAt.Add(time.Second)
		} else if invalid == "shutdown-after-record" {
			supplement.ShutdownAt = supplement.At.Add(time.Nanosecond)
		} else {
			var events []store.RuntimeReceipt
			for _, receipt := range records.Events {
				if receipt.DispatchID == dispatch.ID && receipt.Type == "verified-result" {
					if invalid == "missing-failure-receipt" {
						continue
					}
					receipt.Payload, receipt.Hash = "{}", receiptHash("{}")
				}
				events = append(events, receipt)
			}
			records.Events = events
			supplement.ReceiptHash, _ = records.DispatchReceiptsHash(dispatch.ID)
		}
		if err := records.VerifyFlowAudits(after.Graph); err == nil {
			t.Fatal("durable verifier accepted invalid original completion proof", invalid)
		}
	}
	before.Records.FailedReviewSupplements = after.Records.FailedReviewSupplements
	if !reflect.DeepEqual(before, after) || worker.starts != starts || len(worker.sends) != sends || worker.stops != stops {
		t.Fatal("supplement rewrote history or executed model work")
	}
	count, _ := os.ReadFile(counter)
	if string(count) != "x" {
		t.Fatal("supplement replayed product validation", string(count))
	}
	if _, err := control.RecordFailedReview(t.Context(), request, false); err == nil {
		t.Fatal("duplicate supplement accepted")
	}
	for _, mutate := range []func(*store.Workspace){
		func(s *store.Workspace) { s.Records.FailedReviewSupplements = nil },
		func(s *store.Workspace) { s.Records.Events = nil },
		func(s *store.Workspace) { s.Records.FailedReviewSupplements[0].Reason = "changed" },
		func(s *store.Workspace) {
			for index := range s.Graph.Dispatches {
				if s.Graph.Dispatches[index].ID == dispatch.ID {
					s.Graph.Dispatches[index].Result.Blockers = []string{"rewritten failure"}
				}
			}
		},
	} {
		if err := db.UpdateWorkspace(func(s *store.Workspace) error { mutate(s); return nil }); err == nil {
			t.Fatal("immutable supplement or original failure was rewritten")
		}
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := control.RecordFailedReview(cancelled, request, true); err == nil {
		t.Fatal("cancelled processing succeeded")
	}
	for _, invalidTimeout := range []time.Duration{0, MaxFailedReviewObservationTimeout + time.Nanosecond} {
		if _, err := control.RecordFailedReviewWithTimeout(t.Context(), request, true, invalidTimeout); err == nil {
			t.Fatal("invalid observation timeout accepted", invalidTimeout)
		} else {
			var failure *FailedReviewOperationError
			if !errors.As(err, &failure) || failure.Phase != "configuration" || failure.Persistence != "NOT_WRITTEN" {
				t.Fatal("timeout configuration failure lacks safe diagnostics", err)
			}
		}
	}
	control.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := control.RecordFailedReviewWithTimeout(t.Context(), request, true, 20*time.Millisecond)
		done <- err
	}()
	time.Sleep(40 * time.Millisecond)
	control.mu.Unlock()
	err = <-done
	var failure *FailedReviewOperationError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &failure) ||
		failure.Phase != "controller-lock" || failure.Persistence != "NOT_WRITTEN" {
		t.Fatal("contended Controller lock did not preserve timeout identity and phase", err)
	}
	cancelledGit, cancelGit := context.WithCancel(t.Context())
	cancelGit()
	if err := gitproof.VerifyPreparedReviewWorktreeContext(cancelledGit, core.GraphDispatch{}, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("prepared-Git verifier obscured cancellation identity", err)
	}
}

type configuredFailedReviewIdentity interface {
	runtime.Identity
	runtime.ConfigurationIdentity
}

type blockingFailedReviewRuntime struct {
	configuredFailedReviewIdentity
	deadline time.Time
}

func (r *blockingFailedReviewRuntime) ReadSavedTurn(ctx context.Context, _ string) (runtime.SavedTurn, error) {
	r.deadline, _ = ctx.Deadline()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	<-ctx.Done()
	return runtime.SavedTurn{}, ctx.Err()
}
