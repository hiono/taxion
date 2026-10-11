package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type scriptedRuntime struct {
	fakeRuntime
	dispatch core.GraphDispatch
	events   chan runtime.Event
	script   func() error
	stopErr  error
}

func (s *scriptedRuntime) Send(context.Context, string, string) error { return s.script() }
func (s *scriptedRuntime) EventCursor(string) (uint64, error)         { return 0, nil }
func (s *scriptedRuntime) EventsAfter(context.Context, string, uint64) (<-chan runtime.Event, error) {
	return s.events, nil
}
func (s *scriptedRuntime) Stop(context.Context, string) error {
	s.stops++
	return s.stopErr
}

type configuredScriptedRuntime struct {
	*scriptedRuntime
	selection json.RawMessage
	accounts  []string
}

func (s *configuredScriptedRuntime) ExecutionConfiguration() json.RawMessage     { return s.selection }
func (s *configuredScriptedRuntime) CheckRepositoryAccess(context.Context) error { return nil }
func (s *configuredScriptedRuntime) CheckAuthentication(_ context.Context, account string) error {
	s.accounts = append(s.accounts, account)
	return nil
}

func TestContinuousFlowRunsRepairAndNewSHAReview(t *testing.T) {
	resolved, err := config.Resolve([]config.Layer{{Source: "user", Input: []byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: configuration-approval}
`)}})
	if err != nil {
		t.Fatal(err)
	}
	decision := core.TaskDecision{
		ID: resolved.Config.Execution.ApprovalRef, Source: "human:named-review-configuration",
		Text: string(mustFlowJSON(t, config.Approval{
			Kind: config.ApprovalKind, TaskID: "implementation", Reason: "authorize both exact reviews",
			AdditionalTaskIDs: []string{"flow-review-0", "flow-review-1"}, Allowed: []config.Config{resolved.Config},
		})),
	}
	db, ledger, request := reviewFlowFixture(t, decision)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	var workers []*scriptedRuntime
	var configuredWorkers []*configuredScriptedRuntime
	factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowClaimed) {
			t.Fatal("manual advance bypassed runner ownership", err)
		}
		if err := BindConfiguration(db, dispatch.ID, resolved); err != nil {
			return nil, err
		}
		worker := &scriptedRuntime{
			fakeRuntime: fakeRuntime{
				role: dispatch.Recipient, hash: "config:" + dispatch.ID,
				worktree: dispatch.Instruction.Worktree, sessionID: "sdk:" + dispatch.ID,
			},
			dispatch: dispatch, events: make(chan runtime.Event, 8),
		}
		workers = append(workers, worker)
		worker.script = func() error {
			result := core.DispatchResult{
				DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
				Repository: dispatch.Repository, Status: core.DispatchSucceeded,
				StartedSHA: dispatch.ExpectedStartSHA(), OutputSHA: dispatch.ExpectedStartSHA(),
				ChangedFiles: []string{}, Evidence: []string{"test-report"}, NextTransition: core.WaitingForReview,
			}
			if dispatch.Recipient == "CODER" {
				root := dispatch.Instruction.Worktree
				if err := os.MkdirAll(filepath.Join(root, "internal"), 0755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(root, "internal/app.go"), []byte(dispatch.ID), 0600); err != nil {
					return err
				}
				gitTest(t, root, "add", "internal/app.go")
				gitTest(t, root, "commit", "-m", "feat(app): "+dispatch.ID, "-m", "intent(app): satisfy the approved acceptance criterion")
				result.OutputSHA = gitTest(t, root, "rev-parse", "HEAD")
				result.ChangedFiles = []string{"internal/app.go"}
			} else {
				changed := false
				result.WorktreeChanged = &changed
				result.ReviewVerdict = "APPROVE"
				result.Findings = []core.DispatchFinding{}
				result.Acceptance = []core.DispatchAcceptance{{Criterion: "application tested", Status: "PASS", Evidence: []string{"test-report"}}}
				if len(workers) == 2 {
					result.ReviewVerdict = "REQUEST_CHANGES"
					result.Acceptance[0].Status = "FAIL"
					result.Findings = []core.DispatchFinding{{
						Severity: "HIGH", Title: "Correct acceptance coverage", Evidence: []string{"test-report"},
					}}
				} else {
					prompt, err := core.RenderDispatchInstruction(dispatch)
					if err != nil || !strings.Contains(prompt, "Correct acceptance coverage") {
						t.Fatal("re-review lost original findings", err)
					}
				}
			}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: "intermediate explanation"}
			worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":10,"outputTokens":5}`}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(mustFlowJSON(t, result))}
			worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
			return nil
		}
		configured := &configuredScriptedRuntime{scriptedRuntime: worker, selection: mustFlowJSON(t, resolved.Config)}
		configuredWorkers = append(configuredWorkers, configured)
		return configured, nil
	}
	var out bytes.Buffer
	flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, factory, &out)
	if err != nil || flow.Status != "COMPLETED" || len(workers) != 4 || flow.RunnerID != "" {
		t.Fatalf("continuous flow did not complete: %+v %v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for _, worker := range workers {
		if worker.closes == 0 || worker.stops != 0 {
			t.Fatalf("unclosed runtime or unnecessary stop: %+v", worker.fakeRuntime)
		}
		saved, _ := state.Graph.GraphDispatch(worker.dispatch.ID)
		if saved.Status != core.DispatchSucceeded || len(saved.RuntimeUsage) != 1 {
			t.Fatal("final results or usage not persisted")
		}
		if saved.Configuration == nil {
			t.Fatal("continuous flow failed to persist configuration")
		}
		found := false
		for _, recorded := range saved.Instruction.Decisions {
			found = found || recorded == decision
		}
		if !found {
			t.Fatal("continuous flow modified the original Human configuration approval")
		}
	}
	for _, worker := range configuredWorkers {
		if len(worker.accounts) != 2 || worker.accounts[0] != "ai-user" || worker.accounts[1] != "ai-user" {
			t.Fatal("continuous flow bypassed start/send authentication", worker.accounts)
		}
	}
	original, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	fixed, _ := state.Graph.GraphDispatch(flow.DispatchIDs[2])
	review, _ := state.Graph.GraphDispatch(flow.DispatchIDs[3])
	if original.Result.OutputSHA == fixed.Result.OutputSHA || review.Result.OutputSHA != fixed.Result.OutputSHA ||
		!review.Result.GitEvidence.WorktreeReleased || !strings.Contains(out.String(), "intermediate explanation") {
		t.Fatal("new-SHA review or intermediate event separation failed")
	}
}

func TestRunnerStopsExecutionForLimitsAndStreamFailures(t *testing.T) {
	for _, failure := range []string{"tokens", "deadline", "stream", "usage", "unknown-usage", "output", "cancel", "stop-failure", "claim", "contract"} {
		t.Run(failure, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "deadline" {
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					state.Graph.ReviewFlows[0].Deadline = time.Now().Add(100 * time.Millisecond)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			var worker *scriptedRuntime
			factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
				worker = &scriptedRuntime{
					fakeRuntime: fakeRuntime{
						role: dispatch.Recipient, hash: "runner-config", worktree: dispatch.Instruction.Worktree,
					}, events: make(chan runtime.Event, 4),
				}
				if failure == "stop-failure" {
					worker.stopErr = errors.New("cannot confirm SDK stop")
				}
				worker.script = func() error {
					switch failure {
					case "tokens":
						worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: fmt.Sprintf(`{"model":"test","inputTokens":%d,"outputTokens":0}`, request.MaxTokens)}
					case "deadline":
					case "stream":
						close(worker.events)
					case "usage":
						worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: "malformed"}
					case "unknown-usage":
						worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":10}`}
					case "output":
						worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: "unwritable"}
					case "claim", "contract":
						if err := db.UpdateWorkspace(func(state *store.Workspace) error {
							if failure == "claim" {
								state.Graph.ReviewFlows[0].RunnerID = "other-runner"
								return nil
							}
							return state.Graph.Transition(flow.TaskID, core.Blocked, "human", "reconsider contract", core.TransitionProof{})
						}); err != nil {
							return err
						}
						worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
					default:
						worker.events <- runtime.Event{Type: runtime.EventCancelled, Payload: "cancel requested"}
					}
					return nil
				}
				return worker, nil
			}
			var out io.Writer = io.Discard
			if failure == "output" {
				out = failingWriter{}
			}
			flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, factory, out)
			if err == nil || worker == nil || worker.stops == 0 || worker.closes == 0 {
				t.Fatalf("runtime not safely stopped/closed: %+v %v", flow, err)
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			saved, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
			if failure == "stop-failure" {
				if saved.Status != core.DispatchCancelRequested {
					t.Fatal("failed stop reported cancellation")
				}
			} else if saved.Status != core.DispatchCancelled || state.Ledger.Dispatches[0].Active {
				t.Fatalf("cancellation not persisted: %+v", saved)
			}
			if failure != "claim" && (flow.Status != "STOPPED" || flow.RunnerID != "") {
				t.Fatalf("failed flow did not retain stop reason: %+v", flow)
			}
			if len(state.Graph.Dispatches) != 1 {
				t.Fatal("failed runtime launched another Dispatch")
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestRunnerRefusesInterruptedOwnershipAndPersistsSetupFailure(t *testing.T) {
	for _, failure := range []string{"claim", "factory"} {
		t.Run(failure, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "claim" {
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					state.Graph.ReviewFlows[0].RunnerID = "interrupted-process"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			_, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, func(core.GraphDispatch) (runtime.Identity, error) {
				calls++
				return nil, errors.New("SDK configuration unavailable")
			}, io.Discard)
			if err == nil {
				t.Fatal("runner accepted interrupted claim or setup failure")
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			saved, _ := state.Graph.ReviewFlow(flow.ID)
			if failure == "claim" {
				if calls != 0 || saved.RunnerID != "interrupted-process" || saved.Status != "RUNNING" {
					t.Fatal("runner took over an interrupted execution")
				}
			} else if calls != 1 || saved.Status != "STOPPED" || saved.RunnerID != "" ||
				!strings.Contains(saved.Reason, "SDK configuration unavailable") {
				t.Fatalf("setup failure not persisted: %+v", saved)
			}
		})
	}
}
