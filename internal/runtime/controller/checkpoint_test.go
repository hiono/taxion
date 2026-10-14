package controller

import (
	"encoding/json"
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

func TestSuspendedResultCanBeCancelledWithoutLosingReceiptHistory(t *testing.T) {
	for _, mode := range []string{core.OutputModeCommit, core.OutputModeWorktreeTreeSnapshot} {
		t.Run(mode, func(t *testing.T) {
			db, ledger, request := reviewFlowFixtureWithOutputMode(t, mode)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			worker := &fakeRuntime{role: "CODER", hash: "fixture", worktree: flow.Contract.Worktree}
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
			if err := os.Mkdir(filepath.Join(flow.Contract.Worktree, "internal"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(flow.Contract.Worktree, "internal/app.go"), []byte("partial work"), 0600); err != nil {
				t.Fatal(err)
			}
			input, output := int64(10), int64(5)
			if err := control.RecordUsage(id, core.DispatchUsage{Model: "fixture", InputTokens: &input, OutputTokens: &output}); err != nil {
				t.Fatal(err)
			}
			result := core.DispatchResult{
				DispatchID: id, TaskID: flow.TaskID, Recipient: "CODER", Repository: flow.Contract.Repo,
				Status: core.DispatchSuspended, StartedSHA: flow.Contract.BaselineSHA,
				Evidence: []string{"partial work"}, ChangedFiles: []string{"internal/app.go"},
				ResumeCondition: "resolve missing tool", NextTransition: core.Blocked,
			}
			if _, err := control.Finish(t.Context(), id, string(mustFlowJSON(t, result))); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if err := control.Stop(t.Context(), id); err != nil {
				t.Fatalf("suspension receipt prevented safe cancellation: %v", err)
			}
			after, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			stopped, _ := after.Graph.GraphDispatch(id)
			if stopped.Status != core.DispatchCancelled || stopped.Result.PartialGitEvidence == nil ||
				!reflect.DeepEqual(stopped.RuntimeUsage, before.Graph.Dispatches[0].RuntimeUsage) ||
				len(after.Records.Events) != 2 ||
				!reflect.DeepEqual(after.Records.Events[0], before.Records.Events[0]) ||
				after.Records.Events[1].Sequence != 1 || len(worker.sends) != 1 || worker.starts != 1 || worker.stops != 1 {
				t.Fatal("stop lost suspension evidence or repeated execution")
			}
			if err := validateResultReceipt(&after, stopped); err != nil {
				t.Fatal("cancellation history cannot be revalidated", err)
			}
			for _, change := range []string{"missing-first", "reordered", "historical-payload", "historical-identity", "historical-usage"} {
				t.Run(change, func(t *testing.T) {
					state, err := db.LoadWorkspace()
					if err != nil {
						t.Fatal(err)
					}
					switch change {
					case "missing-first":
						state.Records.Events = state.Records.Events[1:]
					case "reordered":
						state.Records.Events[0], state.Records.Events[1] = state.Records.Events[1], state.Records.Events[0]
					case "historical-payload":
						state.Records.Events[0].Payload += " "
					default:
						var body struct {
							Result *core.DispatchResult
							Usage  []core.DispatchUsage
						}
						if err := json.Unmarshal([]byte(state.Records.Events[0].Payload), &body); err != nil {
							t.Fatal(err)
						}
						if change == "historical-identity" {
							body.Result.TaskID = "another-task"
						} else {
							*body.Usage[0].InputTokens++
						}
						state.Records.Events[0].Payload = string(mustFlowJSON(t, body))
						state.Records.Events[0].Hash = receiptHash(state.Records.Events[0].Payload)
					}
					if err := validateResultReceipt(&state, stopped); err == nil {
						t.Fatal("altered historical receipt was accepted")
					}
				})
			}
			if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowStopped) {
				t.Fatal("cancelled work did not stop the flow", err)
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
	event := runtime.Event{Type: runtime.EventUsage, Sequence: 1, Payload: `{"model":"fixture","inputTokens":2,"outputTokens":1,"accounting":{"sourceSessionId":"sdk-session","sequence":1,"usageId":"api-call-1"}}`}
	for range 2 {
		if err := control.recordEvent(id, "stream", event); err != nil {
			t.Fatal(err)
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.Events) != 1 || len(state.Graph.Dispatches[0].RuntimeUsage) != 1 {
		t.Fatalf("duplicate receipt/usage: %+v %v", state.Records, err)
	}
	event.Sequence = 2
	if err := control.recordEvent(id, "stream", event); err != nil {
		t.Fatal("duplicate SDK accounting identity was not idempotent", err)
	}
	event = runtime.Event{Type: runtime.EventUsage, Sequence: 3, Payload: `{"model":"fixture","inputTokens":3,"outputTokens":4,"accounting":{"sourceSessionId":"sdk-session","sequence":2,"usageId":"api-call-2"}}`}
	if err := control.recordEvent(id, "stream", event); err != nil {
		t.Fatal(err)
	}
	state, err = db.LoadWorkspace()
	if err != nil || len(state.Records.Events) != 3 || len(state.Graph.Dispatches[0].RuntimeUsage) != 2 {
		t.Fatalf("per-call usage was not retained exactly once: %+v %v", state.Records, err)
	}
	tokens, known := flowTokens(state.Graph, flow)
	if !known || tokens != 10 {
		t.Fatalf("distinct per-call tokens = %d, known=%v; want 10, true", tokens, known)
	}
	event = runtime.Event{Type: runtime.EventUsage, Sequence: 4, Payload: `{"model":"fixture","inputTokens":9,"outputTokens":1,"accounting":{"sourceSessionId":"sdk-session","sequence":1,"usageId":"api-call-1"}}`}
	if err := control.recordEvent(id, "stream", event); err == nil {
		t.Fatal("reused SDK accounting identity with different metrics was accepted")
	}
	event.Payload = `{"model":"fixture","inputTokens":9,"outputTokens":1}`
	event.Sequence = 1
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
