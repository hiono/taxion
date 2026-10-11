package controller

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func resolutionRequest(t *testing.T, db *store.Store, feedbackID, id string) FeedbackResolutionRequest {
	t.Helper()
	observation, err := ObserveFeedback(db, "flow")
	if err != nil {
		t.Fatal(err)
	}
	return FeedbackResolutionRequest{
		FlowID: "flow", FeedbackID: feedbackID, FeedbackResolution: core.FeedbackResolution{
			ID: id, ExpectedChangeID: observation.ExpectedChangeID, Target: observation.Target,
			Action: "KEEP_CONTRACT", Actor: "human", Source: "human:clarification",
			Reason: "After considering the feedback, retain the approved execution contract",
		},
	}
}

func TestFeedbackResolutionGatesReleaseAndPreservesRecordsAfterRestart(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	for _, id := range []string{"first", "second"} {
		if _, err := RecordFeedback(db, feedbackRequest(t, db, id)); err != nil {
			t.Fatal(err)
		}
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	first := resolutionRequest(t, db, "first", "first-resolution")
	resolved, err := ResolveFeedback(db, first)
	if err != nil || len(resolved.PendingFeedbackIDs()) != 1 || resolved.PendingFeedbackIDs()[0] != "second" {
		t.Fatal("resolution did not identify remaining feedback", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "feedback:first", "RELEASE")); err == nil {
		t.Fatal("partial resolution allowed release")
	}
	second := resolutionRequest(t, db, "second", "second-resolution")
	duplicate := second
	duplicate.ID = first.ID
	if _, err := ResolveFeedback(db, duplicate); err == nil {
		t.Fatal("resolution ID was reused for another feedback")
	}
	resolved, err = ResolveFeedback(db, second)
	if err != nil || len(resolved.PendingFeedbackIDs()) != 0 || !resolved.HandoffHeld() {
		t.Fatal("resolution implicitly released hold or lost status", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	replay, err := ResolveFeedback(db, first)
	if err != nil || !reflect.DeepEqual(resolved, replay) {
		t.Fatal("restart or replay changed resolution", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "feedback:first", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 {
		t.Fatal("resolved feedback could not proceed under original contract", err)
	}
	if _, err := ResolveFeedback(db, first); err != nil {
		t.Fatal("accepted resolution replay became stale after progression", err)
	}
	if _, err := RecordFeedback(db, feedbackRequest(t, db, "third")); err != nil {
		t.Fatal(err)
	}
	replay, err = ResolveFeedback(db, first)
	if err != nil || !replay.HandoffHeld() || len(replay.PendingFeedbackIDs()) != 1 {
		t.Fatal("old replay disposed of new feedback or released a later hold", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("later-release", "feedback:third", "RELEASE")); err == nil {
		t.Fatal("old resolution authorized a later handoff")
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before.Graph.Dispatches[0], after.Graph.Dispatches[0]) {
		t.Fatal("resolution changed observed result/instruction", err)
	}
	saved, _ := after.Graph.ReviewFlow(flow.ID)
	owned, _ := after.Graph.ReviewFlow(flow.ID)
	owned.Feedback[0].Resolution.Reason = "caller mutation"
	if saved.Feedback[0].Resolution.Reason == "caller mutation" {
		t.Fatal("resolution aliases stored record")
	}
	first.Reason = "rewrite"
	if _, err := ResolveFeedback(db, first); err == nil {
		t.Fatal("existing disposition was rewritten")
	}
}

func TestFeedbackResolutionRechecksLatestTargetWithoutRebindingOriginalFeedback(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := New(db, &fakeRuntime{role: "CODER", hash: "hash", worktree: flow.Contract.Worktree})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Start(t.Context(), flow.DispatchIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err := worker.Send(t.Context(), flow.DispatchIDs[0], "execute"); err != nil {
		t.Fatal(err)
	}
	input := feedbackRequest(t, db, "feedback")
	if _, err := RecordFeedback(db, input); err != nil {
		t.Fatal(err)
	}
	stale := resolutionRequest(t, db, "feedback", "resolution")
	if err := worker.Fail(flow.DispatchIDs[0], "observed failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveFeedback(db, stale); !errors.Is(err, core.ErrFeedbackStale) {
		t.Fatal("stale resolution accepted", err)
	}
	current := resolutionRequest(t, db, "feedback", "resolution")
	for _, mutation := range []func(*FeedbackResolutionRequest){
		func(r *FeedbackResolutionRequest) { r.Source = "" },
		func(r *FeedbackResolutionRequest) { r.Action = "APPROVE" },
		func(r *FeedbackResolutionRequest) { r.Action = "APPLY" },
		func(r *FeedbackResolutionRequest) { r.FeedbackID = "missing" },
		func(r *FeedbackResolutionRequest) { r.ExpectedChangeID = "stale" },
		func(r *FeedbackResolutionRequest) { r.At = time.Now() },
	} {
		bad := current
		mutation(&bad)
		if _, err := ResolveFeedback(db, bad); err == nil {
			t.Fatal("invalid disposition accepted")
		}
	}
	resolved, err := ResolveFeedback(db, current)
	if err != nil || resolved.Feedback[0].Target != input.Target ||
		resolved.Feedback[0].Resolution.Target != current.Target {
		t.Fatal("fresh resolution lost original observation", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].Feedback[0].Resolution.Target.ResultHash = "sha256:invalid"
		return nil
	}); err == nil {
		t.Fatal("corrupt resolution result accepted")
	}
	if _, err := ResolveFeedback(nil, current); err == nil {
		t.Fatal("missing workspace accepted")
	}
}

func TestFeedbackResolutionDoesNotRestoreSafetyStoppedAuthorization(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordFeedback(db, feedbackRequest(t, db, "pending")); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].StartedAt = time.Now().Add(-time.Hour)
		state.Graph.ReviewFlows[0].Deadline = time.Now().Add(-time.Minute)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if !errors.Is(err, ErrFlowStopped) || flow.Status != "STOPPED" {
		t.Fatal("pending feedback suppressed safety stop", err)
	}
	flow, err = ChangeHandoff(db, handoffRequest("release", "feedback:pending", "RELEASE"))
	if err != nil || flow.Status != "STOPPED" || flow.HandoffHeld() || len(flow.PendingFeedbackIDs()) != 1 {
		t.Fatal("stopped release changed authority or disposed of feedback", err)
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowStopped) {
		t.Fatal("release restarted stopped flow", err)
	}
}
