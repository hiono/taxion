package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

type extensionRuntime struct {
	*configuredScriptedRuntime
	send func(context.Context) error
}

func (w *extensionRuntime) Send(ctx context.Context, _, _ string) error { return w.send(ctx) }

func TestLiveReviewerExtensionKeepsSessionReceiptAndUsageAcrossOriginalDeadline(t *testing.T) {
	for _, extensionCase := range []struct {
		name        string
		extraNative float64
	}{
		{name: "time-only"},
		{name: "time-and-cost", extraNative: 100},
	} {
		t.Run(extensionCase.name, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "validations")
			db, ledger, request, selected := artifactFixture(t, []string{"printf x >> " + counter}, false)
			request.StageBudget.ReviewSeconds, request.StageBudget.ValidationSeconds = 1, 1
			refreshArtifactPlan(t, db, &request)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			// Shorten only this fixture so the real runner must outlive the old limit.
			oldDeadline := time.Now().UTC().Add(10 * time.Second)
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				state.Graph.ReviewFlows[0].Deadline = oldDeadline
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var worker *extensionRuntime
			factories := 0
			var output bytes.Buffer
			completed, err := RunReviewFlow(t.Context(), db, ledger, flow.ID, func(d core.GraphDispatch) (runtime.Identity, error) {
				factories++
				if err := BindConfiguration(db, d.ID, selected); err != nil {
					return nil, err
				}
				base := &scriptedRuntime{
					fakeRuntime: fakeRuntime{role: d.Recipient, hash: "test", worktree: d.Instruction.Worktree},
					events:      make(chan runtime.Event, 3),
				}
				worker = &extensionRuntime{configuredScriptedRuntime: &configuredScriptedRuntime{
					scriptedRuntime: base, selection: mustFlowJSON(t, selected.Config),
				}}
				worker.send = func(ctx context.Context) error {
					state, err := db.LoadWorkspace()
					if err != nil {
						return err
					}
					before, _ := state.Graph.ReviewFlow(flow.ID)
					current, _ := state.Graph.GraphDispatch(d.ID)
					extension := FlowExtensionRequest{
						FlowID: flow.ID, PreviousDeadline: oldDeadline, Deadline: oldDeadline.Add(3 * time.Hour),
						PreviousMaxNanoAIU: before.NativeLimit(), MaxNanoAIU: before.NativeLimit() + extensionCase.extraNative,
						Actor: "human", Source: "human:" + extensionCase.name, Reason: "finish the same saved review without interruption",
					}
					preview, err := ExtendReviewFlow(db, extension, true)
					if err != nil {
						return err
					}
					after, _ := db.LoadWorkspace()
					if !reflect.DeepEqual(state, after) {
						return fmt.Errorf("preview changed live state")
					}
					// A real usage update between preview and consent is not a new contract.
					control, _ := New(db, worker)
					used, complete := 3.0, "complete"
					if err := control.RecordUsage(d.ID, core.DispatchUsage{Model: "test", TotalNanoAIU: &used, AICreditsStatus: &complete}); err != nil {
						return err
					}
					extension.ExpectedHash = preview.Hash
					applied, err := ExtendReviewFlow(db, extension, false)
					if err != nil || !applied.StateChanged {
						return fmt.Errorf("formal live extension failed: %w", err)
					}
					if _, err := ExtendReviewFlow(db, extension, false); err == nil {
						return fmt.Errorf("duplicate extension replay accepted")
					}
					select {
					case <-ctx.Done():
						return fmt.Errorf("old deadline cancelled the approved turn: %w", ctx.Err())
					case <-time.After(time.Until(oldDeadline) + 100*time.Millisecond):
					}
					if ctx.Err() != nil {
						return ctx.Err()
					}
					saved, err := db.LoadWorkspace()
					if err != nil {
						return err
					}
					live, _ := saved.Graph.GraphDispatch(d.ID)
					if live.RuntimeSessionID != current.RuntimeSessionID || len(live.RuntimeUsage) != 1 {
						return fmt.Errorf("extension lost session or cumulative usage")
					}
					unchanged := false
					result := core.DispatchResult{
						DispatchID: d.ID, TaskID: d.TaskID, Recipient: d.Recipient, Repository: d.Repository,
						Status: core.DispatchSucceeded, StartedSHA: d.ExpectedStartSHA(), OutputSHA: d.ExpectedStartSHA(),
						ChangedFiles:    []string{},
						WorktreeChanged: &unchanged, Evidence: []string{"test-report"}, NextTransition: core.Completed,
						ReviewVerdict: "APPROVE", Findings: []core.DispatchFinding{},
						Acceptance: []core.DispatchAcceptance{{Criterion: "application tested", Status: "PASS", Evidence: []string{"test-report"}}},
					}
					body, _ := json.Marshal(result)
					base.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(body)}
					base.events <- runtime.Event{Type: runtime.EventTurnFinished}
					return nil
				}
				return worker, nil
			}, &output, request.Configuration)
			if err != nil || completed.Status != "COMPLETED" || factories != 1 || worker == nil || worker.stops != 0 ||
				!completed.Deadline.Equal(oldDeadline) || !completed.ExecutionDeadline().Equal(oldDeadline.Add(3*time.Hour)) ||
				completed.NativeLimit() != completed.Budget.MaxNanoAIU+extensionCase.extraNative {
				t.Fatalf("same review did not complete across original deadline: status=%s factories=%d err=%v", completed.Status, factories, err)
			}
			count, _ := os.ReadFile(counter)
			if string(count) != "xx" {
				t.Fatal("input validation was replayed or result acceptance omitted", string(count))
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			before := state.Graph.ReviewFlows[0]
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				state.Graph.ReviewFlows[0].Extensions = nil
				return nil
			}); err == nil {
				t.Fatal("removing formal extension escaped immutable audit")
			}
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				state.Graph.ReviewFlows[0].Extensions[0].Deadline = oldDeadline.Add(2 * time.Hour)
				return nil
			}); err == nil {
				t.Fatal("rewriting formal consent escaped immutable audit")
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, after.Graph.ReviewFlows[0]) {
				t.Fatal("rejected extension tampering changed state")
			}
		})
	}
}

func TestLiveExtensionRejectsDriftExpiredStageAndKeepsCallerDeadline(t *testing.T) {
	db, ledger, request := stageFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}

	extension := FlowExtensionRequest{FlowID: flow.ID, PreviousDeadline: flow.Deadline, Deadline: flow.Deadline.Add(time.Minute),
		PreviousMaxNanoAIU: flow.NativeLimit(), MaxNanoAIU: flow.NativeLimit(), Actor: "human", Source: "human:extension", Reason: "finish within extended time"}
	preview, err := ExtendReviewFlow(db, extension, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"request", "prior", "missing", "secret"} {
		bad := extension
		bad.ExpectedHash = preview.Hash
		switch mutation {
		case "request":
			bad.Deadline = bad.Deadline.Add(time.Second)
		case "prior":
			bad.PreviousDeadline = bad.PreviousDeadline.Add(time.Second)
		case "missing":
			bad.ExpectedHash = ""
		case "secret":
			bad.Reason = "token=ghp_abcdefghijklmnopqrstuvwxyz0123456789"
		}
		if _, err := ExtendReviewFlow(db, bad, false); err == nil {
			t.Fatal("invalid extension accepted", mutation)
		}
	}
	parent, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	bounded, end, err := ReviewFlowContext(parent, db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	extension.ExpectedHash = preview.Hash
	if _, err := ExtendReviewFlow(db, extension, false); err != nil {
		t.Fatal(err)
	}
	<-bounded.Done()
	if bounded.Err() != context.DeadlineExceeded {
		t.Fatal("extension bypassed independent caller deadline", bounded.Err())
	}
	state, _ := db.LoadWorkspace()
	dispatch, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
	now := stageDeadline(flow, dispatch, false)
	if _, err := previewFlowExtension(state, extension, now.Add(time.Hour)); err == nil {
		t.Fatal("late consent revived expired stage")
	}
	event, _, err := resourceWarning(state, dispatch, now.Add(-time.Second))
	if err != nil || event == nil || !strings.Contains(event.Payload, "ask once now") {
		t.Fatal("missing pre-stop combined decision request", event, err)
	}
}

func TestLiveExtensionStillExpiresAtNewHardDeadline(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(300 * time.Millisecond)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].Deadline = old
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := ReviewFlowContext(t.Context(), db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	requestExtension := FlowExtensionRequest{
		FlowID: flow.ID, PreviousDeadline: old, Deadline: old.Add(400 * time.Millisecond),
		PreviousMaxNanoAIU: flow.NativeLimit(), MaxNanoAIU: flow.NativeLimit(),
		Actor: "human", Source: "human:one-decision", Reason: "finish the same execution",
	}
	preview, err := ExtendReviewFlow(db, requestExtension, true)
	if err != nil {
		t.Fatal(err)
	}
	requestExtension.ExpectedHash = preview.Hash
	if _, err := ExtendReviewFlow(db, requestExtension, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		if time.Now().Before(requestExtension.Deadline) || ctx.Err() != context.DeadlineExceeded {
			t.Fatal("wrong extended hard boundary", ctx.Err())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("extended execution failed to stop at the new hard boundary")
	}
}
