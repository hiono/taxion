package controller

import (
	"context"
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

func TestFailedReviewSupplementPreservesFailureAndNeverReplaysValidation(t *testing.T) {
	for _, mode := range []string{"normal", "artifact"} {
		t.Run(mode, func(t *testing.T) { testFailedReviewSupplement(t, mode) })
	}
}

func testFailedReviewSupplement(t *testing.T, mode string) {
	counter := filepath.Join(t.TempDir(), "validations")
	db, ledger, start, selected := artifactFixture(t, []string{"printf x >> " + counter}, mode == "normal", "CODER")
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
		SubmittedAt: now, FinishedAt: now, HistoryHash: "sha256:" + strings.Repeat("a", 64),
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
	before, _ := db.LoadWorkspace()
	initialTurn, initialRequest, initialAnswer := worker.turn, request, answer
	for name, change := range map[string]func(){
		"wrong-session":   func() { worker.turn.SessionID = "another-session" },
		"late":            func() { worker.turn.FinishedAt = flow.Deadline.Add(time.Second) },
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
			if _, err := control.RecordFailedReview(t.Context(), request, true); err == nil {
				t.Fatal("invalid negative answer was accepted")
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected observation changed state")
			}
			worker.turn, request, answer = initialTurn, initialRequest, initialAnswer
		})
	}
	preview, err := control.RecordFailedReview(t.Context(), request, true)
	if err != nil || preview.StateChanged || preview.QualityAccepted || preview.ExecutionAuthorized ||
		preview.AccountingReconciled || preview.DispatchStatus != core.DispatchFailed {
		t.Fatal("preview failed or claimed quality/execution/accounting", preview, err)
	}
	afterPreview, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, afterPreview) {
		t.Fatal("preview changed workspace")
	}
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
			s.Graph.Dispatches[len(s.Graph.Dispatches)-1].Result.Blockers = []string{"rewritten failure"}
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
}
