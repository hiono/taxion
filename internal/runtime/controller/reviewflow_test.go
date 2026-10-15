package controller

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/tools"
)

func TestFlowTokenAccountingUsesPerCallInputAndOutputOnly(t *testing.T) {
	zero, two, three, seven, maximum := int64(0), int64(2), int64(3), int64(7), int64(math.MaxInt64)
	for _, test := range []struct {
		name   string
		status core.DispatchStatus
		usage  []core.DispatchUsage
		tokens int64
		known  bool
	}{
		{"active before usage", core.DispatchRunning, nil, 0, true},
		{"terminal without usage", core.DispatchSucceeded, nil, 0, false},
		{"reported zero", core.DispatchSucceeded, []core.DispatchUsage{{InputTokens: &zero, OutputTokens: &zero}}, 0, true},
		{"missing output", core.DispatchRunning, []core.DispatchUsage{{InputTokens: &two}}, 2, false},
		{"multiple calls", core.DispatchSucceeded, []core.DispatchUsage{
			{InputTokens: &two, OutputTokens: &three},
			{InputTokens: &three, OutputTokens: &seven},
		}, 15, true},
		{"cache details not added twice", core.DispatchSucceeded, []core.DispatchUsage{
			{InputTokens: &three, OutputTokens: &two, CacheReadTokens: &three, CacheWriteTokens: &seven},
		}, 5, true},
		{"overflow saturates", core.DispatchRunning, []core.DispatchUsage{{InputTokens: &maximum, OutputTokens: &two}}, math.MaxInt64, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph := core.TaskGraph{Dispatches: []core.GraphDispatch{
				{ID: "included", Status: test.status, RuntimeUsage: test.usage},
				{ID: "unrelated", Status: core.DispatchSucceeded, RuntimeUsage: []core.DispatchUsage{{InputTokens: &maximum}}},
			}}
			tokens, known := flowTokens(graph, core.ReviewFlow{DispatchIDs: []string{"included"}})
			if tokens != test.tokens || known != test.known {
				t.Fatalf("tokens=%d known=%v; want %d, %v", tokens, known, test.tokens, test.known)
			}
		})
	}
}

func reviewFlowFixture(t *testing.T, decisions ...core.TaskDecision) (*store.Store, string, ReviewFlowRequest) {
	return reviewFlowFixtureWithOutputMode(t, "", decisions...)
}

func reviewFlowFixtureWithOutputMode(t *testing.T, outputMode string,
	decisions ...core.TaskDecision) (*store.Store, string, ReviewFlowRequest) {
	return reviewFlowFixtureWithPolicy(t, outputMode, nil, decisions...)
}

func reviewFlowFixtureWithPolicy(t *testing.T, outputMode string, guidance *core.BudgetGuidancePolicy,
	decisions ...core.TaskDecision) (*store.Store, string, ReviewFlowRequest) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "init", "-b", "work/task")
	gitTest(t, root, "config", "user.name", "Taxion Test")
	gitTest(t, root, "config", "user.email", "taxion-test@example.com")
	gitTest(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "README.md")
	gitTest(t, root, "commit", "-m", "baseline")
	mem, err := memory.Open(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := mem.Put(memory.KindDecision, memory.Entry{
		Title: "Preserve acceptance", Body: "Repair the existing criterion and rerun independent review.",
		SourceRefs: []string{"human:1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(); err != nil {
		t.Fatal(err)
	}
	graph := core.TaskGraph{Version: 1}
	task := core.GraphTask{
		ID: "implementation", Title: "Implement application", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Issue: "Issue #53", Owner: "CODER", Reviewer: "REVIEWER", Status: core.Draft,
		Objective: "Implement application", Scope: []string{"internal"}, OutOfScope: []string{"README.md"},
		Acceptance: []string{"application tested"}, RequiredEvidence: []string{"test-report"},
		ValidationCommands: []string{"test -s internal/app.go"}, ExitCondition: "verified application",
		Branch: "work/task", Worktree: root, BaselineSHA: gitTest(t, root, "rev-parse", "HEAD"),
		CreatedFrom: "human:1", AuthorizationID: "implementation-authorization",
		OutputMode:     outputMode,
		Decisions:      []core.TaskDecision{{ID: "implementation-authorization", Source: "human:1", Text: "authorize implementation"}},
		MemoryRefs:     []string{"decision:" + strconv.FormatInt(id, 10)},
		FileAccess:     &core.FileAccessPolicy{Level: core.FileAccessAll},
		BudgetGuidance: core.CloneBudgetGuidance(guidance),
	}
	task.Decisions = append(task.Decisions, decisions...)
	if err := graph.Track(task, "human", "authorize Task"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.InitializeWorkspace(store.Workspace{
		HasGraph: true, Graph: graph,
		Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}},
	}); err != nil {
		t.Fatal(err)
	}
	repairs := 1
	return db, filepath.Join(dir, "ledger.json"), ReviewFlowRequest{
		ID: "flow", TaskID: task.ID, Repository: task.Repo, ReviewRoot: filepath.Join(dir, "reviews"),
		MaxRepairs: &repairs, MaxTokens: 10000, TimeoutSeconds: 600,
		Authorization: core.TaskDecision{ID: "flow-authorization", Source: "human:1", Text: "authorize one repair and independent re-review within saved ceilings"},
	}
}

func mustFlowJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func executeFlowDispatch(t *testing.T, db *store.Store, flow core.ReviewFlow, verdict string, usage bool, selected ...config.Resolution) core.GraphDispatch {
	t.Helper()
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	if dispatch.Recipient == "REVIEWER" {
		if _, err := PrepareReview(db, dispatch.ID, flow.Contract.Worktree); err != nil {
			t.Fatal(err)
		}
	}
	base := &fakeRuntime{
		role: dispatch.Recipient, worktree: dispatch.Instruction.Worktree,
		hash: "config:" + dispatch.ID, sessionID: "session:" + dispatch.ID,
		bound: dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot,
	}
	base.toolPolicy, err = tools.ResolveForTask(state.Graph, dispatch.Instruction.Decisions, dispatch.TaskID, dispatch.Recipient)
	if err != nil {
		t.Fatal(err)
	}
	var control *Controller
	if len(selected) != 0 {
		if err := BindConfiguration(db, dispatch.ID, selected[0]); err != nil {
			t.Fatal(err)
		}
		control, err = New(db, &configuredRuntime{
			fakeRuntime: base, selection: mustFlowJSON(t, selected[0].Config),
		})
	} else {
		control, err = New(db, base)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), dispatch.ID); err != nil {
		t.Fatal(err)
	}
	prompt, err := core.RenderDispatchInstruction(dispatch)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), dispatch.ID, prompt); err != nil {
		t.Fatal(err)
	}
	if usage {
		input, output := int64(10), int64(5)
		if err := control.RecordUsage(dispatch.ID, core.DispatchUsage{Model: "test-model", InputTokens: &input, OutputTokens: &output}); err != nil {
			t.Fatal(err)
		}
	}
	if verdict == "FAILED" {
		if err := control.Fail(dispatch.ID, "observed execution failure"); err != nil {
			t.Fatal(err)
		}
	} else if verdict == "CANCELLED" {
		if err := control.Stop(t.Context(), dispatch.ID); err != nil {
			t.Fatal(err)
		}
	} else {
		head := dispatch.ExpectedStartSHA()
		result := core.DispatchResult{
			DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient, Repository: dispatch.Repository,
			Status: core.DispatchSucceeded, StartedSHA: head, OutputSHA: head,
			ChangedFiles: []string{}, Evidence: []string{"test-report"}, NextTransition: core.WaitingForReview,
		}
		if dispatch.Recipient == "CODER" {
			if err := os.MkdirAll(filepath.Join(dispatch.Instruction.Worktree, "internal"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dispatch.Instruction.Worktree, "internal/app.go"), []byte(dispatch.ID), 0600); err != nil {
				t.Fatal(err)
			}
			result.ChangedFiles = []string{"internal/app.go"}
			if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
				for _, path := range dispatch.Instruction.TargetChangedFiles {
					if !slices.Contains(result.ChangedFiles, path) {
						result.ChangedFiles = append(result.ChangedFiles, path)
					}
				}
				slices.Sort(result.ChangedFiles)
			}
			if dispatch.Instruction.OutputMode != core.OutputModeWorktreeTreeSnapshot {
				gitTest(t, dispatch.Instruction.Worktree, "add", "internal/app.go")
				gitTest(t, dispatch.Instruction.Worktree, "commit", "-m", "feat(app): "+dispatch.ID, "-m", "intent(app): satisfy the approved acceptance criterion")
				result.OutputSHA = gitTest(t, dispatch.Instruction.Worktree, "rev-parse", "HEAD")
			}
		} else {
			changed := false
			result.ReviewVerdict, result.WorktreeChanged, result.Findings = verdict, &changed, []core.DispatchFinding{}
			status := "PASS"
			if verdict != "APPROVE" {
				status = "FAIL"
				result.Findings = []core.DispatchFinding{{Severity: "HIGH", Title: "Add missing acceptance test", File: "internal/app.go", Lines: "1", Evidence: []string{"test-report"}}}
			}
			result.Acceptance = []core.DispatchAcceptance{{Criterion: flow.Contract.Acceptance[0], Status: status, Evidence: []string{"test-report"}}}
		}
		payload := mustFlowJSON(t, result)
		if base.bound {
			var model map[string]json.RawMessage
			if err := json.Unmarshal(payload, &model); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"dispatchId", "taskId", "recipient", "repository", "startedSha", "outputSha"} {
				delete(model, field)
			}
			payload = mustFlowJSON(t, model)
		}
		if _, err := control.Finish(t.Context(), dispatch.ID, string(payload)); err != nil {
			t.Fatal(err)
		}
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ = state.Graph.GraphDispatch(dispatch.ID)
	return dispatch
}

func TestReviewFlowCompletesForUncommittedTreeSnapshot(t *testing.T) {
	db, ledger, request := reviewFlowFixtureWithOutputMode(
		t, core.OutputModeWorktreeTreeSnapshot)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	if coder.Result.OutputSHA != coder.ExpectedStartSHA() ||
		coder.Result.OutputTreeSHA == "" ||
		coder.Result.GitEvidence.SnapshotRef == "" {
		t.Fatalf("Coder snapshot result is incomplete: %+v", coder.Result)
	}
	if head := gitTest(t, coder.Instruction.Worktree, "rev-parse", "HEAD"); head != coder.Result.OutputSHA {
		t.Fatalf("snapshot flow created a commit: %s", head)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 {
		t.Fatalf("snapshot Reviewer was not issued: %+v %v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _ := state.Graph.GraphDispatch(flow.DispatchIDs[1])
	if reviewer.Instruction.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
		reviewer.Instruction.TargetTreeSHA != coder.Result.OutputTreeSHA ||
		!slices.Equal(reviewer.Instruction.TargetChangedFiles, coder.Result.ChangedFiles) {
		t.Fatalf("Reviewer did not receive the exact Coder tree: %+v", reviewer.Instruction)
	}
	executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 3 {
		t.Fatalf("snapshot repair was not issued: %+v %v", flow, err)
	}
	repaired := executeFlowDispatch(t, db, flow, "", true)
	if repaired.Result.OutputTreeSHA == coder.Result.OutputTreeSHA ||
		repaired.Result.OutputSHA != coder.Result.OutputSHA {
		t.Fatalf("repair did not create a new uncommitted tree on the same baseline: %+v", repaired.Result)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 4 {
		t.Fatalf("snapshot re-review was not issued: %+v %v", flow, err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	reviewAgain, _ := state.Graph.GraphDispatch(flow.DispatchIDs[3])
	if reviewAgain.Instruction.TargetTreeSHA != repaired.Result.OutputTreeSHA ||
		reviewAgain.Instruction.TargetTreeSHA == reviewer.Instruction.TargetTreeSHA {
		t.Fatalf("re-review did not target the repaired tree: %+v", reviewAgain.Instruction)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatalf("tree review did not complete: %+v %v", flow, err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask(flow.TaskID)
	if task.Status != core.Completed || task.TargetSHA != repaired.Result.OutputSHA ||
		task.TargetTreeSHA != repaired.Result.OutputTreeSHA {
		t.Fatalf("Task completed without exact tree approval: %+v", task)
	}
}

func TestReviewFlowRepairsAndReviewsNewSHAAfterRestart(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || !reflect.DeepEqual(flow, again) {
		t.Fatal("active Dispatch duplicated", err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 {
		t.Fatalf("review not issued: %+v %v", flow, err)
	}
	review := executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Repairs != 1 || len(flow.DispatchIDs) != 3 {
		t.Fatalf("repair not restored: %+v %v", flow, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	repair, _ := state.Graph.GraphDispatch(flow.DispatchIDs[2])
	contract, err := RenderDispatchContext(db, repair, false)
	if err != nil || !strings.Contains(contract, "Add missing acceptance test") ||
		!strings.Contains(contract, review.ID) || !strings.Contains(contract, review.Result.OutputSHA) ||
		repair.ExpectedStartSHA() != coder.Result.OutputSHA ||
		!slices.Equal(repair.Instruction.Acceptance, coder.Instruction.Acceptance) ||
		!slices.Equal(repair.Instruction.ForbiddenActions, coder.Instruction.ForbiddenActions) ||
		repair.Instruction.MemoryContext[0].Body != coder.Instruction.MemoryContext[0].Body {
		t.Fatalf("repair lost its contract and sourced findings: %v\n%s", err, contract)
	}
	fixed := executeFlowDispatch(t, db, flow, "", true)
	if fixed.Result.OutputSHA == coder.Result.OutputSHA {
		t.Fatal("repair did not produce a new SHA")
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	reviewAgain, _ := state.Graph.GraphDispatch(flow.DispatchIDs[3])
	if reviewAgain.Instruction.TargetSHA != fixed.Result.OutputSHA ||
		reviewAgain.Instruction.Worktree == review.Instruction.Worktree {
		t.Fatal("re-review reused previous SHA or worktree")
	}
	contract, err = RenderDispatchContext(db, reviewAgain, false)
	if err != nil || !strings.Contains(contract, review.ID) || !strings.Contains(contract, "Add missing acceptance test") {
		t.Fatal("re-review lost the original findings", err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatalf("approval not completed: %+v %v", flow, err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask(flow.TaskID)
	if task.Status != core.Completed || task.TargetSHA != fixed.Result.OutputSHA {
		t.Fatal("completed without exact-SHA approval")
	}
	again, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || !reflect.DeepEqual(flow, again) {
		t.Fatal("completed flow reexecuted", err)
	}
}

func TestReviewFlowStopsAtCeilingsAndFailedRuns(t *testing.T) {
	for _, failure := range []string{"repairs", "tokens", "unknown-usage", "deadline", "FAILED", "CANCELLED", "BLOCKED", "scope", "memory", "new-decision"} {
		t.Run(failure, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			if failure == "repairs" {
				zero := 0
				request.MaxRepairs = &zero
			}

			if failure == "tokens" {
				request.MaxTokens = 14
			}
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			verdict := ""
			if failure == "FAILED" || failure == "CANCELLED" {
				verdict = failure
			}
			executeFlowDispatch(t, db, flow, verdict, failure != "unknown-usage")
			if failure == "repairs" || failure == "BLOCKED" {
				flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
				if err != nil {
					t.Fatal(err)
				}
				verdict := "REQUEST_CHANGES"
				if failure == "BLOCKED" {
					verdict = failure
				}
				executeFlowDispatch(t, db, flow, verdict, true)
			}
			if failure == "memory" {
				mem, err := memory.Open(memory.PathForLedger(ledger))
				if err != nil {
					t.Fatal(err)
				}
				if err := mem.Revoke(memory.KindDecision, 1, "human", "reconsider Memory decision"); err != nil {
					t.Fatal(err)
				}
				if err := mem.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "deadline" || failure == "scope" || failure == "new-decision" {
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					if failure == "deadline" {
						state.Graph.ReviewFlows[0].StartedAt = time.Now().Add(-time.Hour)
						state.Graph.ReviewFlows[0].Deadline = time.Now().Add(-time.Minute)
						return nil
					}
					task, _ := state.Graph.GraphTask(flow.TaskID)
					if failure == "new-decision" {
						task.Decisions = append(task.Decisions, core.TaskDecision{
							ID: "human-new-choice", Source: "human:2", Text: "Reconsider the implementation",
						})
					} else {
						task.Scope = []string{"different-scope"}
					}
					return state.Graph.Track(task, "human", "change scope")
				}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
			if err == nil || failure != "memory" && !errors.Is(err, ErrFlowStopped) || flow.Status != "STOPPED" || flow.Reason == "" {
				t.Fatalf("flow continued: %+v %v", flow, err)
			}
			if failure == "tokens" && flow.Reason != "recorded token usage 15 against ceiling 14 (over by 1); Human approval of a new budget is required" {
				t.Fatalf("token ceiling stop did not report observed usage and overage: %q", flow.Reason)
			}
			after, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Graph.Dispatches) != len(before.Graph.Dispatches) {
				t.Fatal("ceiling or failure issued another Dispatch")
			}
			if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, ErrFlowStopped) {
				t.Fatal("stopped flow resumed")
			}
		})
	}
}

func TestReviewFlowRejectsInvalidConfigurationAndStoredState(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	for _, change := range []func(*ReviewFlowRequest){
		func(r *ReviewFlowRequest) { negative := -1; r.MaxRepairs = &negative },
		func(r *ReviewFlowRequest) { r.MaxRepairs = nil },
		func(r *ReviewFlowRequest) { r.MaxTokens = 0 },
		func(r *ReviewFlowRequest) { r.TimeoutSeconds = 0 },
		func(r *ReviewFlowRequest) { r.ReviewRoot = "relative" },
		func(r *ReviewFlowRequest) { r.Repository = "other/repo" },
		func(r *ReviewFlowRequest) { r.Authorization.Source = "" },
	} {
		invalid := request
		change(&invalid)
		if _, err := StartReviewFlow(db, ledger, invalid); err == nil {
			t.Fatal("invalid authorization or ceiling accepted")
		}
	}
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StartReviewFlow(db, ledger, request); err == nil {
		t.Fatal("duplicate flow created")
	}
	for _, change := range []func(*core.ReviewFlow){
		func(f *core.ReviewFlow) { f.MaxTokens = 0 },
		func(f *core.ReviewFlow) { f.Repairs = 1 },
		func(f *core.ReviewFlow) { f.Status = "COMPLETED"; f.Reason = "forged completion" },
		func(f *core.ReviewFlow) { f.Contract.Acceptance = []string{"weakened acceptance"} },
		func(f *core.ReviewFlow) { f.DispatchIDs = append(f.DispatchIDs, f.DispatchIDs[0]) },
	} {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			changed, _ := state.Graph.ReviewFlow(flow.ID)
			change(&changed)
			storeFlow(state, changed)
			return nil
		}); err == nil {
			t.Fatal("inconsistent persisted review state accepted")
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := state.Graph.ReviewFlow(flow.ID)
	if !reflect.DeepEqual(flow, saved) {
		t.Fatal("invalid update escaped rollback")
	}
}
