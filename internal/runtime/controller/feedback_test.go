package controller

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func feedbackRequest(t *testing.T, db *store.Store, id string) FeedbackRequest {
	t.Helper()
	observed, err := ObserveFeedback(db, "flow")
	if err != nil {
		t.Fatal(err)
	}
	return FeedbackRequest{
		FlowID: observed.FlowID, FlowFeedback: core.FlowFeedback{
			ID: id, ExpectedChangeID: observed.ExpectedChangeID, Target: observed.Target,
			Actor: "human", Source: "human:feedback", Reason: "Inspect the observed result before handoff",
			Text:     "Compatibility was not investigated. Explain the evidence before continuing.",
			Evidence: []string{"human:compatibility-question"},
		},
	}
}

func TestFeedbackObservationRecordingAndReplayPreserveTargetAndAuthority(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	input := feedbackRequest(t, db, "coder-feedback")
	afterRead, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, afterRead) {
		t.Fatal("feedback observation changed authority", err)
	}
	if input.Target.OutputSHA != coder.Result.OutputSHA || !strings.HasPrefix(input.Target.ResultHash, "sha256:") {
		t.Fatal("feedback target lost exact result SHA or hash")
	}
	flow, err = RecordFeedback(db, input)
	if err != nil || !flow.HandoffHeld() || len(flow.Feedback) != 1 || len(flow.HandoffChanges) != 1 {
		t.Fatalf("feedback and hold not saved together: %+v %v", flow, err)
	}
	record := flow.Feedback[0]
	if record.Target != input.Target || record.Text != input.Text || record.Source != input.Source ||
		record.HoldChangeID != "feedback:coder-feedback" || record.At.IsZero() {
		t.Fatalf("feedback provenance lost: %+v", record)
	}
	input.Evidence[0] = "caller mutation"
	if record.Evidence[0] != "human:compatibility-question" {
		t.Fatal("saved feedback aliases request evidence")
	}
	input.Evidence[0] = record.Evidence[0]
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reopened, err := RecordFeedback(db, input)
	if err != nil || !reflect.DeepEqual(flow, reopened) {
		t.Fatal("restart lost feedback or replay created another hold", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Graph.Dispatches, state.Graph.Dispatches) ||
		!reflect.DeepEqual(before.Graph.Tasks, state.Graph.Tasks) ||
		!reflect.DeepEqual(before.Graph.History, state.Graph.History) ||
		!reflect.DeepEqual(before.Ledger, state.Ledger) {
		t.Fatal("feedback changed results, instructions, approvals or Task history")
	}
	plan, err := Plan(db, ledger, request.Repository)
	if err != nil || len(plan.ReviewFlows[0].Feedback) != 1 || plan.Tasks[0].Action != "HOLD_HANDOFF" {
		t.Fatal("follow-up planning lost feedback", err)
	}
	owned, _ := state.Graph.ReviewFlow(flow.ID)
	owned.Feedback[0].Evidence[0] = "mutation"
	saved, _ := state.Graph.ReviewFlow(flow.ID)
	if saved.Feedback[0].Evidence[0] != record.Evidence[0] {
		t.Fatal("read-only flow aliases feedback evidence")
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", record.HoldChangeID, "RELEASE")); err == nil {
		t.Fatal("unresolved feedback was released")
	}
	if _, err := ResolveFeedback(db, resolutionRequest(t, db, "coder-feedback", "coder-resolution")); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", record.HoldChangeID, "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 {
		t.Fatal("release failed to preserve normal review", err)
	}
	replayed, err := RecordFeedback(db, input)
	if err != nil || replayed.HandoffHeld() || len(replayed.Feedback) != 1 || len(replayed.DispatchIDs) != 2 {
		t.Fatal("old feedback replay held a new reviewer", err)
	}
	late := input
	late.ID = "late-coder-feedback"
	if _, err := RecordFeedback(db, late); !errors.Is(err, core.ErrFeedbackStale) {
		t.Fatal("old Coder result was silently applied to Reviewer", err)
	}
	review := executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	objection := feedbackRequest(t, db, "review-objection")
	objection.Text = "I disagree with this finding; keep the original verdict and explain it."
	held, err := RecordFeedback(db, objection)
	if err != nil || len(held.Feedback) != 2 || !held.HandoffHeld() {
		t.Fatal("review feedback not recorded", err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	savedReview, _ := state.Graph.GraphDispatch(review.ID)
	if !reflect.DeepEqual(review.Result, savedReview.Result) || savedReview.Result.ReviewVerdict != "REQUEST_CHANGES" {
		t.Fatal("Human objection changed reviewer verdict or findings")
	}
	changedReview := review
	resultCopy := *review.Result
	resultCopy.Findings = append([]core.DispatchFinding{}, resultCopy.Findings...)
	resultCopy.Findings[0].Title = "Different finding on the same SHA"
	changedReview.Result = &resultCopy
	changedTarget, err := core.ObserveFeedbackTarget(changedReview)
	if err != nil || changedTarget.OutputSHA != objection.Target.OutputSHA ||
		changedTarget.ResultHash == objection.Target.ResultHash {
		t.Fatal("result hash did not distinguish findings on the same SHA", err)
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, core.ErrHandoffHeld) {
		t.Fatal("review objection failed to hold repair", err)
	}
}

func TestFeedbackRejectsChangedTargetsAndInvalidInputsWithoutPartialHold(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	input := feedbackRequest(t, db, "feedback")
	worker, err := New(db, &fakeRuntime{role: "CODER", hash: "hash", worktree: flow.Contract.Worktree})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Start(t.Context(), flow.DispatchIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err := worker.Send(t.Context(), flow.DispatchIDs[0], "execute saved contract"); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordFeedback(db, input); !errors.Is(err, core.ErrFeedbackStale) {
		t.Fatal("changed lifecycle accepted as original target", err)
	}
	input = feedbackRequest(t, db, "feedback")
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(*FeedbackRequest){
		func(r *FeedbackRequest) { r.ID = "" },
		func(r *FeedbackRequest) { r.Actor = " human" },
		func(r *FeedbackRequest) { r.Source = "" },
		func(r *FeedbackRequest) { r.Text = " " },
		func(r *FeedbackRequest) { r.Evidence = []string{"duplicate", "duplicate"} },
		func(r *FeedbackRequest) { r.At = time.Now() },
		func(r *FeedbackRequest) { r.HoldChangeID = "supplied" },
		func(r *FeedbackRequest) { r.Target.InstructionHash = "wrong" },
		func(r *FeedbackRequest) { r.Target.OutputSHA = strings.Repeat("a", 40) },
		func(r *FeedbackRequest) { r.Target.ResultHash = "sha256:" + strings.Repeat("a", 64) },
		func(r *FeedbackRequest) { r.ExpectedChangeID = "stale" },
	} {
		bad := input
		mutation(&bad)
		if _, err := RecordFeedback(db, bad); err == nil {
			t.Fatal("invalid feedback accepted")
		}
		after, err := db.LoadWorkspace()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("rejected feedback partially recorded hold or input", err)
		}
	}
	flow, err = RecordFeedback(db, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Events(t.Context(), flow.DispatchIDs[0]); err != nil {
		t.Fatal("active observation was blocked by feedback", err)
	}
	if err := worker.Fail(flow.DispatchIDs[0], "observed failure"); err != nil {
		t.Fatal("feedback blocked active result persistence", err)
	}
	if _, err := RecordFeedback(db, input); err != nil {
		t.Fatal("replay should preserve originally observed running target", err)
	}
	if _, err := ResolveFeedback(db, resolutionRequest(t, db, "feedback", "resolution")); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "feedback:feedback", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	input.Text = "rewritten request"
	if _, err := RecordFeedback(db, input); err == nil {
		t.Fatal("duplicate ID with changed text accepted")
	}
}

func TestFeedbackOnExistingHoldAndResultHashValidation(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
		t.Fatal(err)
	}
	first := feedbackRequest(t, db, "first")
	flow, err = RecordFeedback(db, first)
	if err != nil || len(flow.HandoffChanges) != 1 || flow.Feedback[0].HoldChangeID != "hold" {
		t.Fatal("existing hold not reused", err)
	}
	second := feedbackRequest(t, db, "second")
	flow, err = RecordFeedback(db, second)
	if err != nil || len(flow.Feedback) != 2 || len(flow.HandoffChanges) != 1 {
		t.Fatal("second feedback created another hold", err)
	}
	for _, mutation := range []func(*core.FlowFeedback){
		func(f *core.FlowFeedback) { f.Target.ResultHash = "sha256:" + strings.Repeat("z", 64) },
		func(f *core.FlowFeedback) { f.Target.ResultHash = "sha256:" + strings.Repeat("a", 64) },
		func(f *core.FlowFeedback) { f.Target.OutputSHA = strings.Repeat("a", 40) },
		func(f *core.FlowFeedback) { f.HoldChangeID = "lost" },
		func(f *core.FlowFeedback) { f.Target.TaskID = "different" },
	} {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			mutation(&state.Graph.ReviewFlows[0].Feedback[0])
			return nil
		}); err == nil {
			t.Fatal("invalid feedback target persisted")
		}
	}
	if _, err := ObserveFeedback(nil, flow.ID); err == nil {
		t.Fatal("observation without workspace accepted")
	}
	if _, err := ObserveFeedback(db, "missing"); err == nil {
		t.Fatal("observation without flow accepted")
	}
	if _, err := RecordFeedback(nil, first); err == nil {
		t.Fatal("feedback without workspace accepted")
	}
}
