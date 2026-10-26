package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type quotaScriptedRuntime struct {
	*configuredScriptedRuntime
	quota *quotaRuntime
}

func (s *quotaScriptedRuntime) ReadAccountQuota(ctx context.Context, account string) (runtime.QuotaSnapshot, error) {
	return s.quota.ReadAccountQuota(ctx, account)
}

func TestRunnerAccountBudgetStopPreservesPartialWork(t *testing.T) {
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
		ID: resolved.Config.Execution.ApprovalRef, Source: "human:quota-test",
		Text: string(mustFlowJSON(t, config.Approval{
			Kind: config.ApprovalKind, TaskID: "implementation", Reason: "authorize quota monitoring",
			Allowed: []config.Config{resolved.Config},
		})),
	}
	db, ledger, request := reviewFlowFixtureWithPolicy(t, "", budgetPolicy(t, "account"), decision)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	var worker *quotaScriptedRuntime
	partial := filepath.Join(flow.Contract.Worktree, "internal", "partial.go")
	factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		if err := BindConfiguration(db, dispatch.ID, resolved); err != nil {
			return nil, err
		}
		base := &scriptedRuntime{
			fakeRuntime: fakeRuntime{role: dispatch.Recipient, hash: "quota-test", worktree: dispatch.Instruction.Worktree},
			events:      make(chan runtime.Event, 2),
		}
		base.script = func() error {
			if err := os.MkdirAll(filepath.Dir(partial), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(partial, []byte("partial implementation\n"), 0600); err != nil {
				return err
			}
			base.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":1,"outputTokens":0,"totalNanoAIU":0,"aiCreditsStatus":"complete"}`}
			base.events <- runtime.Event{Type: runtime.EventTurnFinished}
			return nil
		}
		reader := &quotaRuntime{quota: observedQuota()}
		reader.onRead = func() {
			if reader.reads > 1 {
				remaining := 5.0
				reader.quota.RemainingPercentage = &remaining
			}
		}
		worker = &quotaScriptedRuntime{
			configuredScriptedRuntime: &configuredScriptedRuntime{scriptedRuntime: base, selection: mustFlowJSON(t, resolved.Config)},
			quota:                     reader,
		}
		return worker, nil
	}
	flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, factory, io.Discard)
	var stopped *budgetStopError
	if !errors.As(err, &stopped) || flow.StopCode != "BUDGET_STOP" || flow.Status != "STOPPED" ||
		!flow.HandoffHeld() || flow.RunnerID != "" || worker == nil || worker.stops != 1 || worker.closes != 1 {
		t.Fatalf("quota stop was not preserved: %+v err=%v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	if len(state.Graph.Dispatches) != 1 || saved.Status != core.DispatchCancelled ||
		saved.Result == nil || saved.Result.PartialGitEvidence == nil || len(state.Records.Budgets) == 0 ||
		state.Records.Budgets[len(state.Records.Budgets)-1].Kind != "BUDGET_STOP" {
		t.Fatal("quota stop lost partial work or issued a following role")
	}
	if body, err := os.ReadFile(partial); err != nil || string(body) != "partial implementation\n" {
		t.Fatal("partial work was modified or removed", err)
	}
}

func (s *configuredScriptedRuntime) ExecutionConfiguration() json.RawMessage          { return s.selection }
func (s *configuredScriptedRuntime) CheckServiceAccess(context.Context, string) error { return nil }
func (s *configuredScriptedRuntime) CheckRepositoryAccess(context.Context) error      { return nil }
func (s *configuredScriptedRuntime) CheckAuthentication(_ context.Context, account string) error {
	s.accounts = append(s.accounts, account)
	return nil
}

func TestContinuousFlowRunsRepairAndNewSHAReview(t *testing.T) {
	testFlowRunsRepairAndNewSHAReview(t, false)
}

func TestSteppedFlowWaitsForHumanBetweenRoles(t *testing.T) {
	testFlowRunsRepairAndNewSHAReview(t, true)
}

func TestReviewBudgetReportsEffectiveDeadlineWithoutChangingContract(t *testing.T) {
	flow := core.ReviewFlow{ID: "flow", Deadline: time.Now().UTC().Add(time.Hour)}
	dispatch := core.GraphDispatch{ID: "review", Recipient: "REVIEWER"}
	for _, source := range []string{"flow", "caller", "expired"} {
		t.Run(source, func(t *testing.T) {
			ctx := t.Context()
			deadline := flow.Deadline
			wantSource := "flow"
			if source != "flow" {
				deadline, wantSource = time.Now().Add(time.Minute), "caller"
				if source == "expired" {
					deadline = time.Now().Add(-time.Second)
				}
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, deadline)
				defer cancel()
			}
			var out bytes.Buffer
			before := flow
			if err := writeReviewBudget(ctx, &out, flow, dispatch); err != nil {
				t.Fatal(err)
			}
			var event runtime.Event
			if err := json.Unmarshal(out.Bytes(), &event); err != nil || event.Type != "review_budget" {
				t.Fatal("missing versioned review-budget event", err)
			}
			var body struct {
				Version               int       `json:"version"`
				FlowID                string    `json:"flowId"`
				DispatchID            string    `json:"dispatchId"`
				ObservedAt            time.Time `json:"observedAt"`
				FlowDeadline          time.Time `json:"flowDeadline"`
				EffectiveDeadline     time.Time `json:"effectiveDeadline"`
				DeadlineSource        string    `json:"deadlineSource"`
				RemainingMilliseconds int64     `json:"remainingMilliseconds"`
			}
			if err := json.Unmarshal([]byte(event.Payload), &body); err != nil ||
				body.Version != 1 || body.FlowID != flow.ID || body.DispatchID != dispatch.ID ||
				!body.FlowDeadline.Equal(flow.Deadline) || !body.EffectiveDeadline.Equal(deadline) ||
				body.DeadlineSource != wantSource ||
				body.RemainingMilliseconds != max(deadline.Sub(body.ObservedAt).Milliseconds(), 0) ||
				!flow.Deadline.Equal(before.Deadline) {
				t.Fatalf("incorrect effective deadline: %+v err=%v", body, err)
			}
		})
	}
	if err := writeReviewBudget(t.Context(), failingWriter{}, flow, dispatch); err == nil {
		t.Fatal("review budget output failure was ignored")
	}
}

func TestReviewBudgetOutputFailureDoesNotStartReviewer(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	calls := 0
	flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, func(core.GraphDispatch) (runtime.Identity, error) {
		calls++
		return nil, errors.New("unexpected runtime creation")
	}, failingWriter{})
	if err == nil || !strings.Contains(err.Error(), "write review budget") || calls != 0 || flow.Status != "STOPPED" || flow.RunnerID != "" {
		t.Fatalf("output failure started a Reviewer or left its runner claimed: %+v calls=%d err=%v", flow, calls, err)
	}
}

func TestCreditStatusDoesNotInferUnavailableNanoAIUOrAccountBalance(t *testing.T) {
	var output bytes.Buffer
	dispatch := core.GraphDispatch{ID: "dispatch-1", Recipient: "PLANNER"}
	if err := writeCreditStatus(t.Context(), json.NewEncoder(&output), dispatch, runtime.Event{
		Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":10}`,
	}, nil); err != nil {
		t.Fatal(err)
	}
	var event runtime.Event
	if err := json.NewDecoder(&output).Decode(&event); err != nil {
		t.Fatal(err)
	}
	var status struct {
		DispatchID             string                `json:"dispatchId"`
		Role                   string                `json:"role"`
		TurnNanoAIU            *float64              `json:"turnNanoAIU"`
		AICreditsStatus        string                `json:"aiCreditsStatus"`
		RemainingCreditBalance string                `json:"remainingCreditBalance"`
		UsedPercentage         string                `json:"usedPercentage"`
		RemainingPercentage    string                `json:"remainingPercentage"`
		AccountQuota           runtime.QuotaSnapshot `json:"accountQuota"`
		Reason                 string                `json:"reason"`
	}
	if event.Type != "credit_status" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if err := json.Unmarshal([]byte(event.Payload), &status); err != nil {
		t.Fatal(err)
	}
	if status.DispatchID != dispatch.ID || status.Role != dispatch.Recipient ||
		status.TurnNanoAIU != nil || status.AICreditsStatus != "unavailable" ||
		status.RemainingCreditBalance != "unavailable" || status.UsedPercentage != "unavailable" ||
		status.RemainingPercentage != "unavailable" || status.AccountQuota.State != "unavailable" ||
		status.AccountQuota.Unit != "requests" || !strings.Contains(status.Reason, "nano-AIU is unavailable") {
		t.Fatalf("incomplete usage was inferred: %+v", status)
	}
}

func TestCreditStatusReportsApprovedRequestQuotaSeparatelyFromCredits(t *testing.T) {
	var output bytes.Buffer
	quota := observedQuota()
	remaining := int64(38000)
	quota.Remaining = &remaining
	worker := &quotaRuntime{
		configuredRuntime: &configuredRuntime{fakeRuntime: &fakeRuntime{}},
		quota:             quota,
	}
	dispatch := core.GraphDispatch{
		ID: "dispatch-1", Recipient: "CODER",
		Instruction: core.DispatchInstruction{BudgetGuidance: budgetPolicy(t, "account")},
		Configuration: &core.DispatchConfiguration{
			Selection: json.RawMessage(`{"runtime":{"provider":"copilot","account":"ai-user"}}`),
		},
	}
	if err := writeCreditStatus(t.Context(), json.NewEncoder(&output), dispatch, runtime.Event{
		Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":10}`,
	}, worker); err != nil {
		t.Fatal(err)
	}
	var event runtime.Event
	if err := json.NewDecoder(&output).Decode(&event); err != nil {
		t.Fatal(err)
	}
	var status struct {
		RemainingCreditBalance string                `json:"remainingCreditBalance"`
		AccountQuota           runtime.QuotaSnapshot `json:"accountQuota"`
		AccountUsedPercentage  *float64              `json:"accountUsedPercentage"`
	}
	if event.Type != "credit_status" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if err := json.Unmarshal([]byte(event.Payload), &status); err != nil {
		t.Fatal(err)
	}
	if worker.reads != 1 || status.RemainingCreditBalance != "unavailable" ||
		status.AccountQuota.State != "available" || status.AccountQuota.Resource != "premium_interactions" ||
		status.AccountQuota.Unit != "requests" || status.AccountQuota.Remaining == nil ||
		*status.AccountQuota.Remaining != remaining || status.AccountQuota.RemainingPercentage == nil ||
		*status.AccountQuota.RemainingPercentage != 76 || status.AccountUsedPercentage == nil ||
		*status.AccountUsedPercentage != 24 {
		t.Fatalf("request quota was not reported separately from AI Credits: %+v reads=%d", status, worker.reads)
	}
}

func testFlowRunsRepairAndNewSHAReview(t *testing.T, stepped bool) {
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
					prompt, err := RenderDispatchContext(db, dispatch, false)
					if err != nil || !strings.Contains(prompt, "Correct acceptance coverage") {
						t.Fatal("re-review lost original findings", err)
					}
				}
			}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: "intermediate explanation"}
			usage := `{"model":"test","inputTokens":10,"outputTokens":5,"totalNanoAIU":123.5,"aiCreditsStatus":"complete"}`
			worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: usage}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(mustFlowJSON(t, result))}
			worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
			return nil
		}
		configured := &configuredScriptedRuntime{scriptedRuntime: worker, selection: mustFlowJSON(t, resolved.Config)}
		configuredWorkers = append(configuredWorkers, configured)
		return configured, nil
	}
	var out bytes.Buffer
	if stepped {
		for turn := range 5 {
			priorWorkers := len(workers)
			flow, err = StepReviewFlow(t.Context(), db, ledger, flow.ID, factory, &out)
			if err != nil {
				t.Fatal(err)
			}
			if flow.Status == "COMPLETED" {
				break
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if len(workers) != priorWorkers+1 || len(state.Graph.Dispatches) != len(workers) || flow.RunnerID != "" {
				t.Fatal("step issued a subsequent role or retained its runner claim", turn)
			}
		}
	} else {
		flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, factory, &out)
	}
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
			t.Fatal("continuous flow modified the original user configuration approval")
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
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var creditStatuses int
	for {
		var event runtime.Event
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal("decode streamed runtime event:", err)
		}
		if event.Type != "credit_status" {
			continue
		}
		var status struct {
			TurnNanoAIU            *float64 `json:"turnNanoAIU"`
			AICreditsStatus        string   `json:"aiCreditsStatus"`
			RemainingCreditBalance string   `json:"remainingCreditBalance"`
			UsedPercentage         string   `json:"usedPercentage"`
			RemainingPercentage    string   `json:"remainingPercentage"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &status); err != nil {
			t.Fatal("decode per-turn credit status:", err)
		}
		if status.RemainingCreditBalance != "unavailable" || status.UsedPercentage != "unavailable" ||
			status.RemainingPercentage != "unavailable" {
			t.Fatalf("incorrect per-turn credit status: %+v", status)
		}
		if status.TurnNanoAIU == nil || *status.TurnNanoAIU != 123.5 || status.AICreditsStatus != "complete" {
			t.Fatalf("incorrect measured usage: %+v", status)
		}
		creditStatuses++
	}
	if creditStatuses != len(workers) {
		t.Fatalf("got %d statuses for %d calls", creditStatuses, len(workers))
	}
	testCompactPlanningContext(t, db, ledger, state, review, decision)
}

func TestRunnerStopsExecutionForDeadlinesAndStreamFailures(t *testing.T) {
	for _, failure := range []string{"deadline", "stream", "usage", "unknown-usage", "output", "cancel", "stop-failure", "claim", "contract"} {
		t.Run(failure, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "deadline" {
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					state.Graph.ReviewFlows[0].Deadline = time.Now().Add(5 * time.Second)
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
					case "deadline":
					case "stream":
						close(worker.events)
					case "usage":
						worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: "malformed"}
					case "unknown-usage":
						worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":10}`}
						close(worker.events)
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

func TestFailedStreamCapturesPartialWorkAndHoldsForRecovery(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	var worker *scriptedRuntime
	factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		worker = &scriptedRuntime{
			fakeRuntime: fakeRuntime{
				role: dispatch.Recipient, hash: "budget-runtime", worktree: dispatch.Instruction.Worktree,
			},
			events: make(chan runtime.Event, 1),
		}
		worker.script = func() error {
			partialFile := filepath.Join(dispatch.Instruction.Worktree, "internal", "partial.go")
			if err := os.MkdirAll(filepath.Dir(partialFile), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(partialFile, []byte("partial implementation\n"), 0600); err != nil {
				return err
			}
			worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":1,"outputTokens":0,"accounting":{"sourceSessionId":"sdk-session","sequence":1,"usageId":"call-1"}}`}
			close(worker.events)
			return nil
		}
		return worker, nil
	}
	flow, err = RunReviewFlow(context.Background(), db, ledger, flow.ID, factory, io.Discard)
	if err == nil || flow.Status != "STOPPED" || flow.RunnerID != "" || !flow.HandoffHeld() {
		t.Fatalf("failed stream did not leave a held stopped flow: %+v %v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	tokens, known := flowTokens(state.Graph, flow)
	if dispatch.Status != core.DispatchCancelled || dispatch.Result == nil ||
		dispatch.Result.PartialGitEvidence == nil ||
		len(dispatch.Result.PartialGitEvidence.ChangedFiles) != 1 ||
		dispatch.Result.PartialGitEvidence.ChangedFiles[0] != "internal/partial.go" ||
		!known || tokens != 1 || len(state.Graph.Dispatches) != 1 {
		t.Fatalf("failed stream lost accounting or partial Git evidence: dispatch=%+v tokens=%d known=%v", dispatch, tokens, known)
	}
}

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
