package controller

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func handoffRequest(id, expected, action string) HandoffRequest {
	return HandoffRequest{
		FlowID: "flow", HandoffChange: core.HandoffChange{
			ID: id, ExpectedChangeID: expected, Action: action,
			Actor: "human", Source: "human:intervention", Reason: "Inspect compatibility before handoff",
		},
	}
}

func TestHandoffReplayAndConcurrentChanges(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for index, connection := range []*store.Store{db, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := []string{"first", "second"}[index]
			_, err := ChangeHandoff(connection, handoffRequest(id, "", "HOLD"))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes > 1 {
		t.Fatalf("concurrent compare-and-swap accepted %d changes", successes)
	}
	// SQLite can reject both competing deferred transactions with busy errors.
	// A fresh retry after contention must still compare against durable state.
	if successes == 0 {
		if _, err := ChangeHandoff(db, handoffRequest("first", "", "HOLD")); err != nil {
			t.Fatal("hold could not be recorded after contention ended", err)
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	held, _ := state.Graph.ReviewFlow(flow.ID)
	first := held.HandoffChanges[0].ID
	if !held.HandoffHeld() || len(held.HandoffChanges) != 1 {
		t.Fatal("hold not durable")
	}
	independent := state.Graph
	if err := independent.Track(core.GraphTask{
		ID: "unrelated", Title: "Independent investigation", Repo: request.Repository,
		Type: "INVESTIGATION", Status: core.Draft, Issue: "Issue #73", CreatedFrom: "human:separate",
	}, "human", "independent work"); err != nil {
		t.Fatal(err)
	}
	if err := independent.CheckHandoff("unrelated"); err != nil {
		t.Fatal("hold applied to unrelated work", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("stale-release", "", "RELEASE")); err == nil {
		t.Fatal("stale release accepted")
	}
	release := handoffRequest("release", first, "RELEASE")
	if _, err := ChangeHandoff(db, release); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("hold-again", "release", "HOLD")); err != nil {
		t.Fatal(err)
	}
	replayed, err := ChangeHandoff(db, release)
	if err != nil || !replayed.HandoffHeld() || len(replayed.HandoffChanges) != 3 {
		t.Fatalf("replayed release lifted a later hold: %+v %v", replayed, err)
	}
	release.Reason = "different input"
	if _, err := ChangeHandoff(db, release); err == nil {
		t.Fatal("request ID reuse accepted")
	}
	for _, mutation := range []func(*HandoffRequest){
		func(r *HandoffRequest) { r.ID = "" },
		func(r *HandoffRequest) { r.Actor = " human" },
		func(r *HandoffRequest) { r.Source = "" },
		func(r *HandoffRequest) { r.Reason = " " },
		func(r *HandoffRequest) { r.Action = "CANCEL" },
		func(r *HandoffRequest) { r.At = replayed.HandoffChanges[0].At },
	} {
		bad := handoffRequest("new", "hold-again", "RELEASE")
		mutation(&bad)
		if _, err := ChangeHandoff(db, bad); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	owned, _ := state.Graph.ReviewFlow(flow.ID)
	owned.HandoffChanges[0].Reason = "caller mutation"
	original, _ := state.Graph.ReviewFlow(flow.ID)
	if original.HandoffChanges[0].Reason == "caller mutation" {
		t.Fatal("handoff record aliases stored history")
	}
	if err := db.UpdateWorkspace(func(current *store.Workspace) error {
		current.Graph.ReviewFlows[0].HandoffChanges[1].ExpectedChangeID = "wrong"
		return nil
	}); err == nil {
		t.Fatal("malformed handoff history persisted")
	}
}

func TestHandoffBlocksUnstartedExecutionAndRaces(t *testing.T) {
	for _, when := range []string{"before-start", "during-start", "before-send", "during-resume"} {
		t.Run(when, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			base := &fakeRuntime{role: "CODER", hash: "hash", worktree: flow.Contract.Worktree}
			worker := &configuredRuntime{fakeRuntime: base}
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			hold := func() {
				if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
					t.Fatal(err)
				}
			}
			switch when {
			case "before-start":
				hold()
			case "during-start":
				worker.onStart = hold
			}
			_, startErr := control.Start(t.Context(), flow.DispatchIDs[0])
			if when == "before-start" || when == "during-start" {
				if !errors.Is(startErr, core.ErrHandoffHeld) || len(base.sends) != 0 ||
					when == "before-start" && base.starts != 0 || when == "during-start" && base.stops != 1 {
					t.Fatalf("start bypassed hold or leaked session: %+v %v", base, startErr)
				}
			} else {
				if startErr != nil {
					t.Fatal(startErr)
				}
				if when == "before-send" {
					hold()
				} else {
					worker.onResume = hold
				}
				if err := control.Send(t.Context(), flow.DispatchIDs[0], "execute"); !errors.Is(err, core.ErrHandoffHeld) {
					t.Fatal("send bypassed hold", err)
				}
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
			if dispatch.Status != core.DispatchIssued || dispatch.ExecutionStarted || len(base.sends) != 0 {
				t.Fatal("hold cancelled or started pending execution")
			}
			if err := state.Graph.CheckExecutable(request.TaskID, request.Repository); err == nil {
				t.Fatal("active held task was executable")
			}
			if err := db.UpdateWorkspace(func(current *store.Workspace) error {
				if err := current.Graph.TransitionDispatch(dispatch.ID, core.DispatchAcknowledged, "runtime", "accepted", true, false, nil); err != nil {
					return err
				}
				return current.Graph.TransitionDispatch(dispatch.ID, core.DispatchRunning, "runtime", "start", true, true, nil)
			}); !errors.Is(err, core.ErrHandoffHeld) {
				t.Fatal("direct lifecycle bypassed hold", err)
			}
		})
	}
}

func TestHandoffKeepsActiveResultAndRunnerResumable(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	var worker *scriptedRuntime
	factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		worker = &scriptedRuntime{
			fakeRuntime: fakeRuntime{role: dispatch.Recipient, hash: "hash", worktree: dispatch.Instruction.Worktree},
			events:      make(chan runtime.Event, 3),
		}
		worker.script = func() error {
			if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
				return err
			}
			control, err := New(db, worker)
			if err != nil {
				return err
			}
			if err := control.Resume(t.Context(), dispatch.ID); err != nil {
				return err
			}
			if err := control.Send(t.Context(), dispatch.ID, "another turn"); !errors.Is(err, core.ErrHandoffHeld) {
				t.Fatal("held active session accepted a new turn", err)
			}
			if err := os.MkdirAll(filepath.Join(dispatch.Instruction.Worktree, "internal"), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dispatch.Instruction.Worktree, "internal/app.go"), []byte("completed"), 0600); err != nil {
				return err
			}
			gitTest(t, dispatch.Instruction.Worktree, "add", "internal/app.go")
			gitTest(t, dispatch.Instruction.Worktree, "commit", "-m", "feat(app): implement application", "-m", "intent(app): satisfy approved acceptance")
			result := core.DispatchResult{
				DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
				Repository: dispatch.Repository, Status: core.DispatchSucceeded,
				StartedSHA: dispatch.ExpectedStartSHA(), OutputSHA: gitTest(t, dispatch.Instruction.Worktree, "rev-parse", "HEAD"),
				ChangedFiles: []string{"internal/app.go"}, Evidence: []string{"test-report"}, NextTransition: core.WaitingForReview,
			}
			worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":10,"outputTokens":5}`}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(mustFlowJSON(t, result))}
			worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
			return nil
		}
		return worker, nil
	}
	flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, factory, &bytes.Buffer{})
	if !errors.Is(err, core.ErrHandoffHeld) || flow.Status != "RUNNING" || flow.RunnerID != "" ||
		len(flow.DispatchIDs) != 1 || worker.stops != 0 {
		t.Fatalf("hold lost result or cancelled active work: %+v %v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	result := state.Graph.Dispatches[0].Result
	if result == nil || result.GitEvidence == nil || state.Graph.Dispatches[0].Status != core.DispatchSucceeded {
		t.Fatal("held Coder result not persisted")
	}
	snapshot, err := Plan(db, ledger, request.Repository)
	if err != nil || snapshot.Tasks[0].Action != "HOLD_HANDOFF" || !snapshot.ReviewFlows[0].HandoffHeld() {
		t.Fatal("observation did not show hold", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	again, err := AdvanceReviewFlow(db, ledger, flow.ID)
	if !errors.Is(err, core.ErrHandoffHeld) || len(again.DispatchIDs) != 1 {
		t.Fatal("restart lost hold", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 {
		t.Fatal("release did not permit exact-SHA review", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("review-hold", "release", "HOLD")); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(current *store.Workspace) error {
		review, _ := current.Graph.GraphTask("flow-review-0")
		if err := current.Graph.Transition(review.ID, core.Blocked, "human", "inspect pending review", core.TransitionProof{}); err != nil {
			return err
		}
		if err := current.Graph.Transition(review.ID, core.Ready, "human", "same approved review", core.TransitionProof{}); err != nil {
			return err
		}
		_, err := core.NewGraphDispatch(current.Graph, "bypass", review.ID, request.Repository, core.RelationshipContinues,
			flow.DispatchIDs[1], "try a direct dispatch")
		return err
	}); !errors.Is(err, core.ErrHandoffHeld) {
		t.Fatal("held reviewer bypassed direct dispatch gate", err)
	}
	if _, err := PrepareReview(db, flow.DispatchIDs[1], flow.Contract.Worktree); !errors.Is(err, core.ErrHandoffHeld) {
		t.Fatal("held reviewer prepared worktree", err)
	}
	if _, err := os.Stat(filepath.Join(request.ReviewRoot, "flow-review-0")); !os.IsNotExist(err) {
		t.Fatal("held preparation created a worktree", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("review-release", "review-hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	review := executeFlowDispatch(t, db, flow, "APPROVE", true)
	if review.Result.OutputSHA != result.OutputSHA {
		t.Fatal("review lost held result SHA")
	}
	if _, err := ChangeHandoff(db, handoffRequest("report-hold", "review-release", "HOLD")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if !errors.Is(err, core.ErrHandoffHeld) || flow.Status != "RUNNING" {
		t.Fatal("hold bypassed final review discussion", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("report-release", "report-hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatal("release lost completion evidence", err)
	}
	final, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(final.Graph.Dispatches[0].Result, result) {
		t.Fatal("release rewrote held Coder result", err)
	}
}

type handoffHookRuntime struct {
	*scriptedRuntime
	onResume func()
}

func (w *handoffHookRuntime) Resume(ctx context.Context, id string) error {
	err := w.fakeRuntime.Resume(ctx, id)
	if w.onResume != nil {
		hook := w.onResume
		w.onResume = nil
		hook()
	}
	return err
}

func TestHandoffRunnerReattachesUnstartedBoundSessionAfterRelease(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	workers := []*handoffHookRuntime{}
	factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		worker := &handoffHookRuntime{scriptedRuntime: &scriptedRuntime{
			fakeRuntime: fakeRuntime{role: dispatch.Recipient, hash: "same-hash", worktree: dispatch.Instruction.Worktree},
			events:      make(chan runtime.Event, 1),
		}}
		worker.script = func() error {
			worker.events <- runtime.Event{Type: runtime.EventCancelled, Payload: "test ended"}
			return nil
		}
		if len(workers) == 0 {
			worker.onResume = func() {
				if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
					t.Fatal(err)
				}
			}
		}
		workers = append(workers, worker)
		return worker, nil
	}
	flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, factory, &bytes.Buffer{})
	if !errors.Is(err, core.ErrHandoffHeld) || flow.Status != "RUNNING" || workers[0].stops != 0 {
		t.Fatalf("hold cancelled the unstarted binding: %+v %v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch := state.Graph.Dispatches[0]
	if dispatch.RuntimeSessionID == "" || dispatch.ExecutionStarted || dispatch.Status != core.DispatchIssued {
		t.Fatal("unstarted binding was lost")
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, factory, &bytes.Buffer{})
	if err == nil || len(workers) != 2 || workers[1].starts != 0 || workers[1].resumes != 3 || flow.Status != "STOPPED" {
		t.Fatalf("released runner did not reattach the original session: %+v %v", flow, err)
	}
}

func TestHandoffDoesNotSuppressDeadlineOrRestoreAuthorization(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
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
		t.Fatal("hold suppressed expired budget", err)
	}
	flow, err = ChangeHandoff(db, handoffRequest("release", "hold", "RELEASE"))
	if err != nil || flow.Status != "STOPPED" || flow.HandoffHeld() {
		t.Fatal("release restored stopped authorization", err)
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowStopped) {
		t.Fatal("released stopped flow resumed execution", err)
	}
}
