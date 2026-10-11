package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func TestFlowRunnerObservesInterruptAndPreservesUnconfirmedStopOnFailure(t *testing.T) {
	for _, stopFails := range []bool{false, true} {
		t.Run(fmt.Sprint(stopFails), func(t *testing.T) {
			db, ledger, start := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, start)
			if err != nil {
				t.Fatal(err)
			}
			var worker *scriptedRuntime
			factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
				worker = &scriptedRuntime{fakeRuntime: fakeRuntime{
					role: dispatch.Recipient, hash: "interrupt-runtime", worktree: dispatch.Instruction.Worktree,
				}, events: make(chan runtime.Event, 1)}
				if stopFails {
					worker.stopErr = errors.New("stop unavailable")
				}
				worker.script = func() error {
					if err := os.WriteFile(filepath.Join(dispatch.Instruction.Worktree, "partial.txt"), []byte("retained"), 0600); err != nil {
						return err
					}
					observation, err := ObserveFeedback(db, flow.ID)
					if err != nil {
						return err
					}
					if _, err := RequestFlowInterrupt(db, InterruptRequest{
						FlowID: flow.ID, ID: "running-interrupt", Target: observation.Target,
						ExpectedChangeID: observation.ExpectedChangeID, Actor: "human", Source: "human:terminal", Reason: "stop current role",
					}); err != nil {
						return err
					}
					worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"inputTokens":1,"outputTokens":1}`}
					return nil
				}
				return worker, nil
			}
			flow, err = RunReviewFlow(context.Background(), db, ledger, flow.ID, factory, io.Discard)
			if err == nil || flow.Status != "STOPPED" || flow.RunnerID != "" || worker.stops == 0 {
				t.Fatal("runner ignored the interruption or silently reclaimed ownership", err)
			}
			state, _ := db.LoadWorkspace()
			dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
			if stopFails && (dispatch.Status != core.DispatchCancelRequested || dispatch.Result != nil) ||
				!stopFails && (dispatch.Status != core.DispatchCancelled || dispatch.Result == nil) {
				t.Fatal("runner confused stop request and actual stop confirmation")
			}
			if data, err := os.ReadFile(filepath.Join(flow.Contract.Worktree, "partial.txt")); err != nil || string(data) != "retained" {
				t.Fatal("interruption rolled back partial work", err)
			}
		})
	}
}
func TestTargetedInterruptRetainsRequestUntilRuntimeStopAndNeverCancelsTask(t *testing.T) {
	db, ledger, start := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, start)
	if err != nil {
		t.Fatal(err)
	}
	worker := &fakeRuntime{role: "CODER", hash: "runtime-config", worktree: flow.Contract.Worktree}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	id := flow.DispatchIDs[0]
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(context.Background(), id, "Execute the saved instruction"); err != nil {
		t.Fatal(err)
	}
	observed, err := ObserveFeedback(db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := InterruptRequest{FlowID: flow.ID, ID: "interrupt", ExpectedChangeID: observed.ExpectedChangeID,
		Target: observed.Target, Actor: "human", Source: "human:terminal", Reason: "Stop current execution, retain its partial work"}
	if _, err := RequestFlowInterrupt(db, request); err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	dispatch, _ := state.Graph.GraphDispatch(id)
	task, _ := state.Graph.GraphTask(flow.TaskID)
	if dispatch.Status != core.DispatchCancelRequested || dispatch.Result != nil || task.Status != core.Active ||
		worker.stops != 0 || !state.Graph.ReviewFlows[0].HandoffHeld() {
		t.Fatal("interruption request was confused with stop confirmation or Task cancellation")
	}
	if _, err := RequestFlowInterrupt(db, request); err != nil {
		t.Fatal("exact interruption replay changed state", err)
	}
	replayed, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(state, replayed) {
		t.Fatal("duplicate interruption was applied twice")
	}
	if err := control.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	state, _ = db.LoadWorkspace()
	dispatch, _ = state.Graph.GraphDispatch(id)
	task, _ = state.Graph.GraphTask(flow.TaskID)
	if dispatch.Status != core.DispatchCancelled || dispatch.Result == nil ||
		!strings.HasPrefix(dispatch.Result.Evidence[0], "runtime-stop:") || task.Status == core.Cancelled {
		t.Fatal("runtime stop evidence or distinction from Task cancellation was lost")
	}
	if _, err := RequestFlowInterrupt(db, request); err != nil {
		t.Fatal("old interruption replay affected a terminal result", err)
	}
	altered := request
	altered.Reason = "different"
	if _, err := RequestFlowInterrupt(db, altered); err == nil {
		t.Fatal("same interruption ID was reused with different content")
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err == nil || flow.Status != "STOPPED" || len(flow.DispatchIDs) != 1 {
		t.Fatal("interruption implicitly resumed or dispatched work", err)
	}
}

func TestInterruptOfUnstartedDispatchHoldsWithoutInventingStopEvidence(t *testing.T) {
	db, ledger, start := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, start)
	if err != nil {
		t.Fatal(err)
	}
	observation, _ := ObserveFeedback(db, flow.ID)
	request := InterruptRequest{FlowID: flow.ID, ID: "interrupt", Target: observation.Target,
		Actor: "human", Source: "human:terminal", Reason: "Do not start"}
	before, _ := db.LoadWorkspace()
	stale := request
	stale.Target.InstructionHash = "forged"
	if _, err := RequestFlowInterrupt(db, stale); err == nil {
		t.Fatal("stale interruption affected another instruction")
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("stale interruption partially saved its hold")
	}
	if _, err := RequestFlowInterrupt(db, request); err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	if dispatch.Status != core.DispatchIssued || dispatch.Result != nil ||
		state.Graph.CheckHandoffStart(dispatch.ID) == nil || !state.Graph.ReviewFlows[0].HandoffHeld() {
		t.Fatal("unstarted interruption invented runtime stop or allowed execution")
	}
}
