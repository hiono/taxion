package controller

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func completedFixture(t *testing.T) (*store.Store, string, core.ReviewFlow) {
	t.Helper()
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatalf("%+v %v", flow, err)
	}
	return db, ledger, flow
}

func TestCompletedCheckpointRechecksEvidenceAfterRestart(t *testing.T) {
	for _, change := range []string{"none", "head", "review-path", "receipt", "usage", "result", "missing-receipt"} {
		t.Run(change, func(t *testing.T) {
			db, ledger, flow := completedFixture(t)
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			review, _ := state.Graph.GraphDispatch(flow.DispatchIDs[1])
			switch change {
			case "head":
				gitTest(t, flow.Contract.Worktree, "commit", "--allow-empty", "-m", "chore: changed checkpoint")
			case "review-path":
				if err := os.MkdirAll(review.Instruction.Worktree, 0700); err != nil {
					t.Fatal(err)
				}
			case "receipt", "usage", "result", "missing-receipt":
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					switch change {
					case "receipt":
						state.Records.Events[0].Payload = "changed transcript"
					case "usage":
						changed := int64(99)
						state.Graph.Dispatches[0].RuntimeUsage[0].InputTokens = &changed
					case "result":
						state.Graph.Dispatches[1].Result.Evidence = append(state.Graph.Dispatches[1].Result.Evidence, "changed evidence")
					case "missing-receipt":
						state.Records.Events = nil
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			_, err = AdvanceReviewFlow(db, ledger, flow.ID)
			if (change == "none") != (err == nil) {
				t.Fatalf("change %s: %v", change, err)
			}
			after, loadErr := db.LoadWorkspace()
			if loadErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("checkpoint recheck changed state: %v", loadErr)
			}
		})
	}
}

func TestRuntimeReceiptReplayAndCancelledResult(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	worker := &fakeRuntime{role: "CODER", hash: "test", worktree: flow.Contract.Worktree}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	id := flow.DispatchIDs[0]
	if _, err := control.Start(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), id, "duplicate execution"); err == nil || len(worker.sends) != 1 {
		t.Fatal("running execution was resent", err)
	}
	event := runtime.Event{Type: runtime.EventUsage, Sequence: 1, Payload: `{"model":"fixture","inputTokens":2,"outputTokens":1}`}
	for range 2 {
		if err := control.recordEvent(id, "stream", event); err != nil {
			t.Fatal(err)
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.Events) != 1 || len(state.Graph.Dispatches[0].RuntimeUsage) != 1 {
		t.Fatalf("duplicate receipt/usage: %+v %v", state.Records, err)
	}
	event.Payload = `{"model":"fixture","inputTokens":9,"outputTokens":1}`
	if err := control.recordEvent(id, "stream", event); err == nil {
		t.Fatal("conflicting replay accepted")
	}
	event.Payload = `{"model":"fixture","inputTokens":2,"outputTokens":1}`
	if err := control.recordEvent(id, "reconnected-stream", event); err == nil {
		t.Fatal("uncertain reconnect usage was adopted twice")
	}
	if err := control.Stop(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.recordEvent(id, "stream", runtime.Event{Type: runtime.EventTurnFinished, Sequence: 2}); err == nil {
		t.Fatal("late event after stop accepted")
	}
	if _, err := control.Finish(t.Context(), id, `{}`); err == nil {
		t.Fatal("late result after stop accepted")
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowStopped) {
		t.Fatal(err)
	}
}
