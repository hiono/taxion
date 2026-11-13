package controller

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func TestProgressWindowBoundsAndResets(t *testing.T) {
	now := time.Now().UTC()
	w := progressWindow{since: now}
	d := core.GraphDispatch{ID: "coder"}
	d.Instruction.Objective = "repair the supplied failures"
	for range 25 {
		w.observe(runtime.Event{Type: runtime.EventExecution, Payload: strings.Repeat("x", 600) + " token=secret"})
	}

	if w.checkpoint(now.Add(9*time.Minute), d, now.Add(time.Hour), false) != nil {
		t.Fatal("checkpoint emitted before interval")
	}
	event := w.checkpoint(now.Add(10*time.Minute), d, now.Add(time.Hour), false)
	var report struct {
		Counts             map[string]int  `json:"eventCounts"`
		Excerpts           []runtime.Event `json:"excerpts"`
		Truncated          bool            `json:"truncated"`
		AssessmentRequired bool            `json:"assessmentRequired"`
		Warnings           []string        `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(event.Payload), &report); err != nil {
		t.Fatal(err)
	}
	if report.Counts[runtime.EventExecution] != 25 || len(report.Excerpts) != 20 ||
		!report.Truncated || !report.AssessmentRequired || len(report.Excerpts[0].Payload) != 512 {
		t.Fatalf("unbounded or misleading report: %+v", report)
	}
	if w.counts != nil || len(w.excerpts) != 0 {
		t.Fatal("previous interval retained")
	}
	event = w.checkpoint(now.Add(11*time.Minute), d, now.Add(time.Hour), true)
	if !strings.Contains(event.Payload, "insufficient-content") || !strings.Contains(event.Payload, `"finalWindow":true`) {
		t.Fatal("empty final interval did not warn")
	}
	secret := "ghp_" + strings.Repeat("a", 36)
	text, _ := progressExcerpt(secret, 512)
	if strings.Contains(text, secret) {
		t.Fatal("credential was not redacted")
	}
}

func TestProgressMonitorPersistsOnlyWhenSelected(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		db, ledger, request := reviewFlowFixture(t)
		flow, err := StartReviewFlow(db, ledger, request)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			for index := range 100 {
				payload := strings.Repeat("historical execution evidence ", 100)
				state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
					DispatchID: "old", SessionID: "old", StreamID: "old",
					Sequence: uint64(index + 1), Type: runtime.EventOutput, Payload: payload,
					Hash: receiptHash(payload), At: time.Now().UTC(),
				})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
			worker := &scriptedRuntime{
				fakeRuntime: fakeRuntime{role: dispatch.Recipient, hash: "monitor-test", worktree: dispatch.Instruction.Worktree},
				events:      make(chan runtime.Event, 302),
			}
			worker.script = func() error {
				before, err := db.LoadWorkspace()
				if err != nil {
					return err
				}
				running, _ := before.Graph.ReviewFlow(flow.ID)
				started := time.Now()
				for range 300 {
					unchanged, err := observeRunningFlow(db, ledger, flow.ID, running.RunnerID)
					if err != nil || !unchanged {
						t.Fatalf("active observation changed flow: %v %v", unchanged, err)
					}
				}
				after, err := db.LoadWorkspace()
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("polling rewrote state or receipts", err)
				}
				t.Logf("300 active polls with 100 historical receipts: %s", time.Since(started))
				for index := range 300 {
					worker.events <- runtime.Event{Sequence: uint64(index + 1), Type: runtime.EventOutputDelta, Payload: "small delta"}
				}
				worker.events <- runtime.Event{Type: runtime.EventExecution, Payload: "read failed test"}
				worker.events <- runtime.Event{Type: runtime.EventCancelled, Payload: "operator cancellation"}
				return nil
			}
			return worker, nil
		}
		ctx := t.Context()
		if enabled {
			ctx = WithProgressMonitor(ctx)
		}
		var output bytes.Buffer
		if _, err := RunReviewFlow(ctx, db, ledger, flow.ID, factory, &output); err == nil {
			t.Fatal("cancellation lost")
		}
		state, err := db.LoadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		history := 0
		for _, receipt := range state.Records.Events {
			if receipt.DispatchID == "old" {
				history++
				if receipt.Hash != receiptHash(receipt.Payload) {
					t.Fatal("historical receipt changed")
				}
			}
			if receipt.Type == "progress_checkpoint" {
				count++
				if receipt.Hash != receiptHash(receipt.Payload) || !strings.Contains(receipt.Payload, "read failed test") {
					t.Fatal("checkpoint lost evidence binding")
				}
				if history != 100 || strings.Count(output.String(), `"Type":"output_delta"`) != 300 {
					t.Fatal("history or small deltas lost")
				}
			}
		}
		if (count == 1) != enabled || strings.Contains(output.String(), "progress_checkpoint") != enabled {
			t.Fatalf("enabled=%v checkpoint count=%d", enabled, count)
		}
	}
}
