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

func draftRequest(t *testing.T, db *store.Store, id string) InstructionDraftRequest {
	t.Helper()
	observation, err := ObserveFeedback(db, "flow")
	if err != nil {
		t.Fatal(err)
	}
	return InstructionDraftRequest{FlowID: "flow", InstructionDraft: core.InstructionDraft{
		ID: id, ExpectedDraftID: observation.ExpectedDraftID,
		ExpectedChangeID: observation.ExpectedChangeID, Target: observation.Target,
		NextRole: "REVIEWER", Operation: "Review compatibility evidence",
		Text:  "Check compatibility evidence against the approved acceptance criteria.",
		Actor: "human", Source: "human:clarification", Reason: "Clarify the next handoff before issuance",
		Evidence: []string{"human:compatibility"}, Unconfirmed: []string{"Compatibility coverage is not yet confirmed"},
		FeedbackIDs: []string{},
	}}
}

func discardRequest(t *testing.T, db *store.Store, id string) DraftDiscardRequest {
	t.Helper()
	observation, err := ObserveFeedback(db, "flow")
	if err != nil {
		t.Fatal(err)
	}
	return DraftDiscardRequest{FlowID: "flow", DraftDiscard: core.DraftDiscard{
		ID: id, DraftID: observation.ExpectedDraftID, ExpectedChangeID: observation.ExpectedChangeID,
		Target: observation.Target, Actor: "human", Source: "human:draft-decision",
		Reason: "Do not apply the proposal; retain the unchanged approved contract",
	}}
}

func TestInstructionDraftRevisionDiscardRestartAndReplay(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	if _, err := RecordFeedback(db, feedbackRequest(t, db, "feedback")); err != nil {
		t.Fatal(err)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	first := draftRequest(t, db, "draft-1")
	first.FeedbackIDs = []string{"feedback"}
	flow, err = RecordInstructionDraft(db, first)
	if err != nil || len(flow.InstructionDrafts) != 1 || flow.InstructionDrafts[0].Version != 1 ||
		flow.PendingDraftID() != first.ID || !flow.HandoffHeld() {
		t.Fatal("draft was not recorded as a held, versioned proposal", err)
	}
	first.Evidence[0] = "caller mutation"
	first.FeedbackIDs[0] = "caller mutation"
	if flow.InstructionDrafts[0].Evidence[0] == "caller mutation" || flow.InstructionDrafts[0].FeedbackIDs[0] == "caller mutation" {
		t.Fatal("draft aliases caller input")
	}
	first.Evidence[0], first.FeedbackIDs[0] = "human:compatibility", "feedback"
	second := draftRequest(t, db, "draft-2")
	second.Text = "Recheck compatibility with the saved evidence before independent review."
	flow, err = RecordInstructionDraft(db, second)
	if err != nil || len(flow.InstructionDrafts) != 2 || flow.InstructionDrafts[1].Version != 2 ||
		flow.InstructionDrafts[0].Text != first.Text {
		t.Fatal("revision replaced history or lost version", err)
	}
	if _, err := ResolveFeedback(db, resolutionRequest(t, db, "feedback", "resolution")); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "feedback:feedback", "RELEASE")); err == nil {
		t.Fatal("pending draft was silently ignored")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	flow, err = RecordInstructionDraft(db, first)
	if err != nil || len(flow.InstructionDrafts) != 2 || flow.PendingDraftID() != second.ID {
		t.Fatal("old proposal replay restored a superseded draft", err)
	}
	discard := discardRequest(t, db, "discard-2")
	flow, err = DiscardInstructionDraft(db, discard)
	if err != nil || flow.PendingDraftID() != "" || !flow.HandoffHeld() ||
		flow.InstructionDrafts[1].Discard == nil {
		t.Fatal("discard applied a proposal or released handoff", err)
	}
	replay, err := RecordInstructionDraft(db, second)
	if err != nil || !reflect.DeepEqual(flow, replay) {
		t.Fatal("replay reopened discarded proposal", err)
	}
	plan, err := Plan(db, ledger, flow.Contract.Repo)
	if err != nil || len(plan.ReviewFlows[0].InstructionDrafts) != 2 {
		t.Fatal("shared plan lost instruction draft history", err)
	}
	plan.ReviewFlows[0].InstructionDrafts[0].Unconfirmed[0] = "caller mutation"
	plan.ReviewFlows[0].InstructionDrafts[1].Discard.Reason = "caller mutation"
	saved, err := db.LoadWorkspace()
	if err != nil || saved.Graph.ReviewFlows[0].InstructionDrafts[0].Unconfirmed[0] == "caller mutation" ||
		saved.Graph.ReviewFlows[0].InstructionDrafts[1].Discard.Reason == "caller mutation" {
		t.Fatal("readout aliases draft history", err)
	}
	if !reflect.DeepEqual(before.Graph.Tasks, saved.Graph.Tasks) ||
		!reflect.DeepEqual(before.Graph.Dispatches, saved.Graph.Dispatches) ||
		!reflect.DeepEqual(before.Graph.History, saved.Graph.History) ||
		!reflect.DeepEqual(before.Ledger, saved.Ledger) {
		t.Fatal("draft changed authority, results, Task history or issued instruction")
	}
	owned, _ := saved.Graph.ReviewFlow(flow.ID)
	owned.InstructionDrafts[0].Evidence[0] = "caller mutation"
	owned.InstructionDrafts[1].Discard.Reason = "caller mutation"
	original, _ := saved.Graph.ReviewFlow(flow.ID)
	if original.InstructionDrafts[0].Evidence[0] == "caller mutation" ||
		original.InstructionDrafts[1].Discard.Reason == "caller mutation" {
		t.Fatal("graph readout aliases proposal data")
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "feedback:feedback", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 {
		t.Fatal("discard did not allow original review handoff", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("next-hold", "release", "HOLD")); err != nil {
		t.Fatal(err)
	}
	third := draftRequest(t, db, "draft-3")
	third.NextRole, third.Operation = "CODER", "Propose repair after observing the review"
	flow, err = RecordInstructionDraft(db, third)
	if err != nil || flow.InstructionDrafts[2].Version != 3 {
		t.Fatal(err)
	}
	replay, err = DiscardInstructionDraft(db, discard)
	if err != nil || replay.PendingDraftID() != third.ID || !replay.HandoffHeld() {
		t.Fatal("old discard replay cleared a later proposal or hold", err)
	}
}

func TestInstructionDraftRejectsInvalidStaleAndCorruptRecordsAtomically(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	input := draftRequest(t, db, "draft")
	if _, err := RecordInstructionDraft(db, input); err == nil {
		t.Fatal("draft accepted without explicit hold")
	}
	if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
		t.Fatal(err)
	}
	input = draftRequest(t, db, "draft")
	for _, mutate := range []func(*InstructionDraftRequest){
		func(r *InstructionDraftRequest) { r.Actor = "" },
		func(r *InstructionDraftRequest) { r.Source = " " },
		func(r *InstructionDraftRequest) { r.NextRole = "HUMAN" },
		func(r *InstructionDraftRequest) { r.Text = "" },
		func(r *InstructionDraftRequest) { r.Operation = "" },
		func(r *InstructionDraftRequest) { r.Evidence = nil },
		func(r *InstructionDraftRequest) { r.Unconfirmed = nil },
		func(r *InstructionDraftRequest) { r.FeedbackIDs = []string{"unknown"} },
		func(r *InstructionDraftRequest) { r.ExpectedDraftID = "stale" },
		func(r *InstructionDraftRequest) { r.ExpectedChangeID = "stale" },
		func(r *InstructionDraftRequest) { r.Version = 1 },
		func(r *InstructionDraftRequest) { r.At = time.Now() },
		func(r *InstructionDraftRequest) { r.Discard = &core.DraftDiscard{} },
	} {
		bad := input
		mutate(&bad)
		if _, err := RecordInstructionDraft(db, bad); err == nil {
			t.Fatal("invalid instruction proposal accepted")
		}
	}
	flow, err = RecordInstructionDraft(db, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].HandoffChanges = append(state.Graph.ReviewFlows[0].HandoffChanges, core.HandoffChange{
			ID: "forged-release", ExpectedChangeID: "hold", Action: "RELEASE",
			Actor: "human", Source: "human:direct-input", Reason: "Skip proposal",
			At: time.Now().UTC(),
		})
		return nil
	}); err == nil {
		t.Fatal("saved graph bypassed pending-proposal release gate")
	}
	stale := draftRequest(t, db, "draft-2")
	discard := discardRequest(t, db, "discard")
	if _, err := ChangeHandoff(db, handoffRequest("stopped-release", "hold", "RELEASE")); err == nil {
		t.Fatal("pending proposal released")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].Status, state.Graph.ReviewFlows[0].Reason = "STOPPED", "Human stopped the workflow"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordInstructionDraft(db, stale); err == nil {
		t.Fatal("new proposal revived stopped flow")
	}
	if _, err := DiscardInstructionDraft(db, discard); err != nil {
		t.Fatal("could not discard proposal while safely stopped", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("stopped-release", "hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowStopped) {
		t.Fatal("discard restored authorization", err)
	}
	for _, mutate := range []func(*core.InstructionDraft){
		func(d *core.InstructionDraft) { d.Version = 4 },
		func(d *core.InstructionDraft) { d.Target.InstructionHash = "invalid" },
		func(d *core.InstructionDraft) { d.Target.ResultHash = "sha256:invalid" },
		func(d *core.InstructionDraft) { d.ExpectedChangeID = "missing" },
		func(d *core.InstructionDraft) { d.FeedbackIDs = []string{"missing"} },
		func(d *core.InstructionDraft) { d.Discard.DraftID = "different" },
	} {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			mutate(&state.Graph.ReviewFlows[0].InstructionDrafts[0])
			return nil
		}); err == nil {
			t.Fatal("corrupt saved proposal accepted")
		}
	}
	if _, err := RecordInstructionDraft(nil, input); err == nil {
		t.Fatal("missing workspace accepted")
	}
}

func TestInstructionDraftRechecksResultAndCompetingRevision(t *testing.T) {
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
	if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
		t.Fatal(err)
	}
	first := draftRequest(t, db, "draft-1")
	if _, err := RecordInstructionDraft(db, first); err != nil {
		t.Fatal(err)
	}
	stale := draftRequest(t, db, "stale")
	staleDiscard := discardRequest(t, db, "stale-discard")
	if err := worker.Fail(flow.DispatchIDs[0], "observed execution failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordInstructionDraft(db, stale); !errors.Is(err, core.ErrFeedbackStale) {
		t.Fatal("proposal accepted against changed result", err)
	}
	if _, err := DiscardInstructionDraft(db, staleDiscard); !errors.Is(err, core.ErrFeedbackStale) {
		t.Fatal("discard accepted without observing changed result", err)
	}
	left, right := draftRequest(t, db, "left"), draftRequest(t, db, "right")
	results := make(chan error, 2)
	for _, input := range []InstructionDraftRequest{left, right} {
		go func() {
			_, err := RecordInstructionDraft(db, input)
			results <- err
		}()
	}
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("competing revisions both committed or both failed", successes)
	}
	observation, err := ObserveFeedback(db, "flow")
	if err != nil || observation.ExpectedDraftID == first.ID || observation.PendingDraftID != observation.ExpectedDraftID {
		t.Fatal("proposal observation lost latest version", err)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	conflict := first
	conflict.Text = "rewrite history"
	if _, err := RecordInstructionDraft(db, conflict); err == nil {
		t.Fatal("proposal history rewritten")
	}
	discard := discardRequest(t, db, "discard")
	discard.DraftID = first.ID
	if _, err := DiscardInstructionDraft(db, discard); err == nil {
		t.Fatal("superseded proposal discarded as if latest")
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected revision or discard partially changed state", err)
	}
}

func TestInstructionDraftDoesNotSuppressDeadlineOrAuthorizeStoppedFlow(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordInstructionDraft(db, draftRequest(t, db, "draft")); err != nil {
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
	if !errors.Is(err, ErrFlowStopped) || flow.Status != "STOPPED" || flow.PendingDraftID() != "draft" {
		t.Fatal("pending draft suppressed deadline or lost history", err)
	}
	flow, err = ChangeHandoff(db, handoffRequest("release", "hold", "RELEASE"))
	if err != nil || flow.HandoffHeld() || flow.Status != "STOPPED" || flow.PendingDraftID() != "draft" {
		t.Fatal("stopped release applied or erased proposal", err)
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowStopped) {
		t.Fatal("release reauthorized stopped execution", err)
	}
}
