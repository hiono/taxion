package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

type requestFixtureRuntime struct {
	role      string
	worktree  string
	id        string
	selection json.RawMessage
	events    chan runtime.Event
	send      func(string) error
}

type requestQuotaFixtureRuntime struct{ *requestFixtureRuntime }

func (r *requestQuotaFixtureRuntime) ReadAccountQuota(context.Context, string) (runtime.QuotaSnapshot, error) {
	remaining := 75.0
	reset := time.Now().UTC().Add(8 * 24 * time.Hour).Truncate(time.Hour)
	return runtime.QuotaSnapshot{
		Provider: "copilot", Account: "ai", Resource: "premium_interactions", Unit: "requests",
		Scope: "account", Source: "fixture", State: "available", ObservedAt: time.Now().UTC(),
		RemainingPercentage: &remaining, ResetAt: &reset,
	}, nil
}

func TestRuntimeRoleVerificationDoesNotDependOnModelDiversity(t *testing.T) {
	for _, prompt := range []string{requestCoderPrompt, requestReviewerPrompt, "Custom role prompt"} {
		for _, submission := range []bool{false, true} {
			rendered := runtimeSubmissionPrompt(prompt, "saved-contract", submission)
			for _, required := range []string{
				"same model", "independently derive expected behavior",
				"unverified claims", "Coder conversation history",
				"boundary cases", "failure paths and regressions",
				"real file, line range, failure mechanism",
				"Do not manufacture", "report BLOCKED",
				"do not grant extra tools", "saved-contract",
			} {
				if !strings.Contains(rendered, required) {
					t.Errorf("submission=%v: role contract omits %q", submission, required)
				}
			}
		}
	}
}

func TestDisplayRequestUsageSeparatesCallUsageFromAccountBalance(t *testing.T) {
	amount, status := 123.5, "complete"
	var output bytes.Buffer
	if err := displayRequestUsage(&output, []core.DispatchUsage{{
		TotalNanoAIU: &amount, AICreditsStatus: &status,
	}}, 30, nil); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"123.5 nano-AIU", "AI-credit status: complete", "session soft ceiling: 30 AI credits",
		"AI-credit account balance and percentages: unavailable",
		"premium-interactions request quota: unavailable",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("usage output lacks %q: %s", expected, output.String())
		}
	}

	output.Reset()
	if err := displayRequestUsage(&output, nil, 30, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Planner turn usage: unavailable") ||
		!strings.Contains(output.String(), "AI-credit account balance and percentages: unavailable") ||
		!strings.Contains(output.String(), "premium-interactions request quota: unavailable") {
		t.Fatalf("missing usage was not reported explicitly: %s", output.String())
	}

	output.Reset()
	tokens := int64(10)
	if err := displayRequestUsage(&output, []core.DispatchUsage{{InputTokens: &tokens}}, 30, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "usage: unavailable (AI-credit status: unavailable)") ||
		!strings.Contains(output.String(), "AI-credit account balance and percentages: unavailable") {
		t.Fatalf("partial usage was not reported explicitly: %s", output.String())
	}

	output.Reset()
	used, entitlement, remaining, percent := int64(12300), int64(50000), int64(37700), 75.4
	reset := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	quota := &runtime.QuotaSnapshot{
		State: "available", Resource: "premium_interactions", Unit: "requests",
		Used: &used, Entitlement: &entitlement, Remaining: &remaining,
		RemainingPercentage: &percent, ResetAt: &reset,
	}
	if err := displayRequestUsage(&output, []core.DispatchUsage{{InputTokens: &tokens}}, 30, quota); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"premium-interactions request quota: 12300/50000 used",
		"24.6% used, 75.4% remaining", "37700 remaining", "resets 2026-10-31T00:00:00Z",
		"AI-credit account balance and percentages: unavailable",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("request quota output lacks %q: %s", expected, output.String())
		}
	}
}

func (r *requestFixtureRuntime) Role() string                                      { return r.role }
func (r *requestFixtureRuntime) Worktree() string                                  { return r.worktree }
func (r *requestFixtureRuntime) ConfigHash() string                                { return "fixture:" + r.id }
func (r *requestFixtureRuntime) ExecutionConfiguration() json.RawMessage           { return r.selection }
func (r *requestFixtureRuntime) CheckAuthentication(context.Context, string) error { return nil }
func (r *requestFixtureRuntime) CheckRepositoryAccess(context.Context) error       { return nil }
func (r *requestFixtureRuntime) CheckServiceAccess(context.Context, string) error  { return nil }
func (r *requestFixtureRuntime) Start(context.Context, string) (string, error)     { return r.id, nil }
func (r *requestFixtureRuntime) Resume(context.Context, string) error              { return nil }
func (r *requestFixtureRuntime) Stop(context.Context, string) error                { return nil }
func (r *requestFixtureRuntime) Close() error                                      { return nil }
func (r *requestFixtureRuntime) EventCursor(string) (uint64, error)                { return 0, nil }
func (r *requestFixtureRuntime) Events(context.Context, string) (<-chan runtime.Event, error) {
	return r.events, nil
}
func (r *requestFixtureRuntime) EventsAfter(context.Context, string, uint64) (<-chan runtime.Event, error) {
	return r.events, nil
}
func (r *requestFixtureRuntime) Send(_ context.Context, _, text string) error { return r.send(text) }

func requestFixture(t *testing.T) (string, []string) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = old })
	if err := initCmd(); err != nil {
		t.Fatal(err)
	}
	root, _ := createGitWorktree(t, "work/request", "https://github.com/owner/repo.git")
	commitGitFile(t, root, "go.mod", "module example.com/request-fixture\n\ngo 1.26.0\n")
	if err := os.MkdirAll(".taxion", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".taxion/config.yaml", []byte(`version: 1
project: {id: request-fixture}
repository: {provider: github, host: github.com, path: owner/repo, account: scm, credentialRef: scm}
runtime: {provider: copilot, account: ai, credentialRef: ai, contractRef: fixture}
execution: {requester: human, worker: local, approvalRef: configured-approval}
`), 0600); err != nil {
		t.Fatal(err)
	}
	return root, []string{"--repo", "owner/repo", "--worktree", root,
		"--timeout", "1m", "--max-ai-credits", "30", "--max-task-nano-aiu", "1000000000",
		"--max-repairs", "0", "--context-path", "README.md"}
}

func requestFixtureAnswer(questions []string) controller.QuestionAnswer {
	answer := controller.QuestionAnswer{
		Recommendation: "HOLD",
		Confirmed:      []controller.QuestionClaim{{Text: "検索機能の依頼を受け取りました。", Sources: []string{"/planner/task/decisions"}}},
		Assumptions:    []string{}, Unconfirmed: []string{}, Questions: questions,
	}
	if len(questions) == 0 {
		answer.Proposal = &controller.RequestProposal{
			Title: "検索機能", Objective: "部分一致の検索を追加する",
			Scope: []string{"src"}, OutOfScope: []string{},
			Acceptance: []string{"大文字小文字を区別せず部分一致で検索する"}, ValidationCommands: []string{"go test ./src"},
		}
	} else {
		answer.Recommendation = "HUMAN_REQUIRED"
	}
	return answer
}

func TestRequestPathInventoryRetainsEveryPathAndReducesRepeatedDirectories(t *testing.T) {
	paths := []string{"README.md", "same.md", "docs/same.md", "docs/deep/same.md",
		"docs/日本語.md", "docs/space and \"quote\".md"}
	for i := range 400 {
		paths = append(paths, fmt.Sprintf("internal/runtime/controller/planning/input-%03d.go", i))
	}
	original, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(requestPathInventory(paths))
	if err != nil {
		t.Fatal(err)
	}
	if len(compact)*100 > len(original)*70 {
		t.Fatalf("directory grouping saved less than 30%%: %d versus %d bytes", len(compact), len(original))
	}
	var directories map[string][]string
	if err := json.Unmarshal(compact, &directories); err != nil {
		t.Fatal(err)
	}
	var restored []string
	for directory, names := range directories {
		for _, name := range names {
			restored = append(restored, path.Join(directory, name))
		}
	}
	slices.Sort(paths)
	slices.Sort(restored)
	if !slices.Equal(paths, restored) || !slices.Contains(directories["."], "README.md") {
		t.Fatal("directory grouping changed or omitted committed paths")
	}
}

func fixturePlan(t *testing.T, answer controller.QuestionAnswer, accountOnly ...bool) requestPlanner {
	t.Helper()
	return func(ctx context.Context, db *store.Store, selected config.Config, task core.GraphTask,
		text string, _ float64, _, _ string) (controller.QuestionResult, error) {
		body, err := json.Marshal(answer)
		if err != nil {
			return controller.QuestionResult{}, err
		}
		selection, _ := json.Marshal(selected)
		worker := &requestFixtureRuntime{role: "ORCHESTRATOR", worktree: task.Worktree,
			id: "planner:" + task.ID, selection: selection, events: make(chan runtime.Event, 3)}
		worker.send = func(sent string) error {
			var wire struct {
				Question string                     `json:"question"`
				Context  map[string]json.RawMessage `json:"context"`
			}
			if err := json.NewDecoder(strings.NewReader(sent)).Decode(&wire); err != nil {
				t.Fatal(err)
			}
			if _, duplicate := wire.Context["task"]; duplicate {
				t.Fatal("complete model input still contains the duplicate Task wrapper")
			}
			var context struct {
				Task core.GraphTask `json:"task"`
			}
			if err := json.Unmarshal(wire.Context["planner"], &context); err != nil {
				t.Fatal(err)
			}
			var request struct {
				Request string `json:"request"`
			}
			if err := json.Unmarshal([]byte(context.Task.Decisions[0].Text), &request); err != nil {
				t.Fatal(err)
			}
			var conversation struct {
				Messages []requestMessage `json:"conversation"`
			}
			if err := json.Unmarshal([]byte(wire.Question), &conversation); err != nil {
				t.Fatal(err)
			}
			if context.Task.ID != task.ID || request.Request == "" ||
				!strings.HasPrefix(request.Request, "request-digest:sha256:") || len(conversation.Messages) == 0 {
				t.Fatal("Planner lacks its transient turn or the saved request digest")
			}
			worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"fixture","inputTokens":10,"outputTokens":5,"totalNanoAIU":1,"aiCreditsStatus":"complete"}`}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(body)}
			worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
			return nil
		}
		if len(accountOnly) != 0 && accountOnly[0] {
			result, err := controller.AskPlanner(ctx, db, storePath, task.ID, text, selected, &requestQuotaFixtureRuntime{worker})
			result.Usage = nil
			return result, err
		}
		return controller.AskPlanner(ctx, db, storePath, task.ID, text, selected, worker)
	}
}

func TestRequestRefinesHumanIntentAndRunsSameTreeReviewWithoutCopyPaste(t *testing.T) {
	testApprovedRequest(t, false)
}

func TestRequestResumesApprovedFlowWithoutRepeatingCoder(t *testing.T) {
	testApprovedRequest(t, true)
}

func TestRequestAccountQuotaOmitsNativeCeilingAndResumes(t *testing.T) {
	testApprovedRequest(t, true, true)
}

func TestRequestOmittedNativeCeilingRequiresAccountApproval(t *testing.T) {
	_, args := requestFixture(t)
	for index, arg := range args {
		if arg == "--max-task-nano-aiu" {
			args = append(args[:index:index], args[index+2:]...)
			break
		}
	}
	var out bytes.Buffer
	err := requestCommand(args, strings.NewReader("/plan\n"), &out, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "requires --budget-guidance account") {
		t.Fatal("omission without account approval was accepted", err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if state.Records.Operation != nil || len(state.Graph.Tasks) != 0 {
		t.Fatal("denied omission mutated the workspace")
	}
}

func testApprovedRequest(t *testing.T, resume bool, quotaOnly ...bool) {
	t.Helper()
	root, args := requestFixture(t)
	accountOnly := len(quotaOnly) != 0 && quotaOnly[0]
	if accountOnly {
		for index, arg := range args {
			if arg == "--max-task-nano-aiu" {
				args = append(args[:index:index], args[index+2:]...)
				break
			}
		}
		args = append(args, "--budget-guidance", "account")
	}
	var calls, roles, runs int
	planner := func(ctx context.Context, db *store.Store, selected config.Config, task core.GraphTask,
		text string, credits float64, cli, base string) (controller.QuestionResult, error) {
		calls++
		var input struct {
			Files   map[string][]string `json:"committedFilesByDirectory"`
			Context []json.RawMessage   `json:"committedContext"`
		}
		if err := json.Unmarshal([]byte(text), &input); err != nil {
			t.Fatal(err)
		}
		wantPaths, err := gitproof.TrackedPathsAt(root, task.BaselineSHA)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(requestPathInventory(wantPaths))
		got, _ := json.Marshal(input.Files)
		if !bytes.Equal(got, want) || len(input.Context) != 1 || strings.Contains(text, `"committedPaths":`) {
			t.Fatal("Planner did not receive the complete grouped inventory and selected context")
		}
		state, _ := db.LoadWorkspace()
		if len(state.Graph.Dispatches) != 0 {
			t.Fatal("planning issued code before Human approval")
		}
		answer := requestFixtureAnswer([]string{"検索の一致条件を教えてください。"})
		if calls == 2 {
			if !strings.Contains(text, "部分一致") || !strings.Contains(text, "planner") {
				t.Fatal("Planner lost the user's refinement or earlier question", text)
			}
			answer = requestFixtureAnswer([]string{})
		}
		return fixturePlan(t, answer, accountOnly)(ctx, db, selected, task, text, credits, cli, base)
	}
	runner := func(ctx context.Context, runArgs []string, output io.Writer) error {
		runs++
		if !strings.Contains(strings.Join(runArgs, " "), "--builtin-prompts") {
			t.Fatal("request required user-created role prompts")
		}
		db, _, err := loadWorkspace()
		if err != nil {
			return err
		}
		defer db.Close()
		resolved, err := resolveConfiguration()
		if err != nil {
			return err
		}
		flowID := runArgs[2]
		if err := controller.CheckReviewFlowConfiguration(db, flowID, resolved.Config,
			map[string][]string{"CODER": {"view", "create", "edit", "apply_patch"}, "REVIEWER": {"view"}}); err != nil {
			return err
		}
		state, _ := db.LoadWorkspace()
		flow, _ := state.Graph.ReviewFlow(flowID)
		if flow.MaxTokens != 0 || flow.MaxRepairs != 0 {
			t.Fatal("new flow retained an observed-token spending ceiling", flow)
		}
		run := controller.RunReviewFlow
		if resume && runs == 1 {
			run = controller.StepReviewFlow
		}
		_, err = run(ctx, db, storePath, flowID, func(dispatch core.GraphDispatch) (runtime.Identity, error) {
			dispatch, err = selectDispatchConfiguration(db, dispatch)
			if err != nil {
				return nil, err
			}
			received, err := controller.RenderDispatchContext(db, dispatch, true)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(received, "部分一致の検索を追加する") ||
				strings.Contains(received, "部分一致です。") || strings.Contains(received, "検索機能をつけて") {
				t.Fatal("role context lost its approved plan or retained raw conversation", dispatch.Recipient)
			}
			if dispatch.Recipient == "REVIEWER" &&
				(!strings.Contains(received, "Implemented case-insensitive search; checked missing and Japanese queries.") ||
					!strings.Contains(received, `"reviewDiff"`) || !strings.Contains(received, "+func Search")) {
				t.Fatal("independent Reviewer did not receive saved report and immutable source diff")
			}
			if dispatch.Recipient == "REVIEWER" {
				legacy, err := controller.RenderDispatchContext(db, dispatch, false)
				if err != nil || !strings.Contains(legacy, "+func Search") {
					t.Fatal("non-compact Reviewer lost the immutable source diff", err)
				}
			}
			roles++
			worker := &requestFixtureRuntime{role: dispatch.Recipient, id: dispatch.ID,
				worktree: dispatch.Instruction.Worktree, selection: dispatch.Configuration.Selection,
				events: make(chan runtime.Event, 3)}
			worker.send = func(string) error {
				result := core.DispatchResult{
					DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
					Repository: dispatch.Repository, Status: core.DispatchSucceeded,
					StartedSHA: dispatch.ExpectedStartSHA(), OutputSHA: dispatch.ExpectedStartSHA(),
					ChangedFiles: []string{}, Evidence: []string{"work-report"}, Validations: []core.DispatchValidation{},
					NextTransition: core.WaitingForReview,
					WorkReport:     "Implemented case-insensitive search; checked missing and Japanese queries.",
				}
				if dispatch.Recipient == "CODER" {
					if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(root, "src/search.go"), []byte(`package search

import "strings"

func Search(items []string, query string) []string {
	var matched []string
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), strings.ToLower(query)) {
			matched = append(matched, item)
		}
	}
	return matched
}
`), 0600); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(root, "src/search_test.go"), []byte(`package search

import (
	"slices"
	"testing"
)

func TestSearch(t *testing.T) {
	items := []string{"Taxion CLI", "Other", "文化祭の検索"}
	for query, want := range map[string][]string{
		"AXI": {"Taxion CLI"}, "検索": {"文化祭の検索"}, "missing": nil,
	} {
		if got := Search(items, query); !slices.Equal(got, want) {
			t.Fatalf("Search(%q) = %v, want %v", query, got, want)
		}
	}
}
`), 0600); err != nil {
						return err
					}
					result.ChangedFiles = []string{"src/search.go", "src/search_test.go"}
				} else {
					content, err := os.ReadFile(filepath.Join(dispatch.Instruction.Worktree, "src/search.go"))
					if err != nil || !strings.Contains(string(content), "Search") {
						return errors.Join(err, errors.New("Reviewer did not receive Coder output"))
					}
					changed := false
					result.WorktreeChanged = &changed
					result.ReviewVerdict, result.NextTransition = "APPROVE", core.Approved
					result.Findings = []core.DispatchFinding{}
					for _, criterion := range dispatch.Instruction.Acceptance {
						result.Acceptance = append(result.Acceptance, core.DispatchAcceptance{Criterion: criterion,
							Status: "PASS", Evidence: []string{"work-report"}})
					}
				}
				body, err := json.Marshal(result)
				if err != nil {
					return err
				}
				worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"fixture","inputTokens":10,"outputTokens":5,"totalNanoAIU":1,"aiCreditsStatus":"complete"}`}
				worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(body)}
				worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
				return nil
			}
			if accountOnly {
				return &requestQuotaFixtureRuntime{worker}, nil
			}
			return worker, nil
		}, output)
		if resume && runs == 1 && err == nil {
			return errors.New("fixture interruption after saved Coder evidence")
		}
		return err
	}
	var out bytes.Buffer
	err := requestCommand(append(args, "検索機能をつけて"), strings.NewReader("/plan\n部分一致です。\n/approve\n"), &out, planner, runner)
	if resume {
		if err == nil || roles != 1 {
			t.Fatal("fixture did not stop after exactly one Coder", err, roles)
		}
		db, state, err := loadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		id := state.Records.Operation.ID
		db.Close()
		err = requestCommand([]string{"--resume", id}, strings.NewReader(""), &out, planner, runner)
		if err != nil {
			t.Fatal("approved checkpoint did not resume directly at Reviewer", err, out.String())
		}
	} else if err != nil {
		t.Fatal(err, out.String())
	}
	if calls != 2 || roles != 2 || !strings.Contains(out.String(), `"gate":"COMPLETED"`) {
		t.Fatal("copy-free request did not complete", calls, roles, out.String())
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if state.Records.Operation == nil || state.Records.Operation.State != "COMPLETED" ||
		state.Records.Operation.Owner != "" {
		t.Fatal("completed request retained an active operation")
	}
	var checkpoint requestCheckpoint
	if err := json.Unmarshal(state.Records.Operation.Checkpoint, &checkpoint); err != nil ||
		checkpoint.Preflight == nil || !checkpoint.Preflight.ExecutionGateOpen || len(checkpoint.Preflight.Lanes) != 4 {
		t.Fatal("approved preflight was not completed as one recorded batch", err)
	}
	if accountOnly {
		if !state.Records.Operation.AccountQuotaOnly || state.Records.Operation.PlanningUsageState != "unknown" ||
			!state.Graph.ReviewFlows[0].Budget.AccountQuotaOnly || state.Graph.ReviewFlows[0].Budget.MaxNanoAIU != 0 ||
			!strings.Contains(out.String(), "no nano-AIU ceiling or unit conversion") {
			t.Fatal("quota-only request fabricated a native ceiling or lost its monitoring contract")
		}
	} else if state.Records.Operation.PlanningNanoAIU != 2 ||
		state.Graph.ReviewFlows[0].Budget.MaxNanoAIU != 1e9-2 {
		t.Fatal("planning spend was not deducted from the approved hard ceiling")
	}
	for _, intermediate := range []string{"Starting execution:", "Planning record:", "Planner call"} {
		if strings.Contains(out.String(), intermediate) {
			t.Fatal("request emitted intermediate progress", intermediate)
		}
	}
	coder, reviewer := state.Graph.Dispatches[0], state.Graph.Dispatches[1]
	if strings.Count(out.String(), core.DocumentationAcceptance) != 1 ||
		!slices.Contains(coder.Instruction.Acceptance, core.DocumentationAcceptance) ||
		!slices.Equal(coder.Instruction.Acceptance, reviewer.Instruction.Acceptance) {
		t.Fatal("displayed documentation acceptance did not reach the approved Coder and Reviewer contracts")
	}
	evaluation, err := controller.EvaluateReviewFlow(state.Graph, coder.TaskID+"-flow")
	if err != nil || !evaluation.CostComparisonEligible {
		t.Fatal("request completion lacks documented same-target quality evidence", evaluation, err)
	}
	var intake struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal([]byte(coder.Instruction.Decisions[0].Text), &intake); err != nil {
		t.Fatal(err)
	}
	var executionRequest struct {
		PlanningTaskID string `json:"planningTaskId"`
		RequestHash    string `json:"requestHash"`
		PlanHash       string `json:"planHash"`
	}
	if err := json.Unmarshal([]byte(intake.Request), &executionRequest); err != nil {
		t.Fatal(err)
	}
	if executionRequest.PlanningTaskID != coder.TaskID+"-planning" ||
		executionRequest.RequestHash != "sha256:"+core.SHA256Hex([]byte("検索機能をつけて")) ||
		executionRequest.PlanHash == "" ||
		strings.Contains(intake.Request, `"conversation"`) ||
		strings.Contains(intake.Request, `"committedFilesByDirectory"`) ||
		strings.Contains(intake.Request, `"committedContext"`) {
		t.Fatal("execution lost its plan identity or retained conversation/context")
	}
	wantMode := "flow"
	if accountOnly {
		wantMode = "account"
	}
	if coder.Instruction.BudgetGuidance == nil || coder.Instruction.BudgetGuidance.Mode != wantMode ||
		reviewer.Instruction.BudgetGuidance == nil || reviewer.Instruction.BudgetGuidance.Mode != wantMode {
		t.Fatal("request did not retain approved guidance through independent review")
	}
	if coder.Result.OutputTreeSHA == "" || coder.Result.OutputTreeSHA != reviewer.Result.OutputTreeSHA ||
		gitTest(t, root, "rev-parse", "HEAD") != coder.Instruction.BaselineSHA {
		t.Fatal("request changed commit identity or reviewed another tree")
	}
	var planning, requestRecords int
	for _, record := range state.Records.Requests {
		if record.RequestID != state.Records.Operation.ID {
			continue
		}
		requestRecords++
		if record.Kind == "planning" {
			planning++
			if record.Sequence != uint64(planning) || !json.Valid([]byte(record.Payload)) {
				t.Fatal("invalid saved Planner receipt", record)
			}
		}
	}
	if planning != 2 {
		t.Fatal("Planner conversation or usage receipts missing", planning)
	}
	wantRecords := 4
	if resume {
		wantRecords++
	}
	if requestRecords != wantRecords {
		t.Fatal("execution events were duplicated instead of referencing the Flow")
	}
	var execution struct {
		FlowID    string `json:"flowId"`
		ErrorHash string `json:"errorHash"`
	}
	if err := core.DecodeGraphInput(strings.NewReader(state.Records.Requests[len(state.Records.Requests)-1].Payload), &execution); err != nil ||
		execution.FlowID != state.Graph.ReviewFlows[0].ID || execution.ErrorHash != core.SHA256Hex(nil) {
		t.Fatal("execution record does not reference its authoritative Flow", execution, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(storePath), "requests")); !os.IsNotExist(err) {
		t.Fatal("request created redundant JSON/log directory", err)
	}
	info, err := os.Stat(workspaceDBPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("request database is not private", err)
	}
}

func TestRequestDoesNotInferApprovalOrRepeatAfterFailure(t *testing.T) {
	for _, mode := range []string{"no-plan", "quit", "eof", "unanswered", "provider-failure", "missing-usage", "over-limit", "dirty", "wrong-repo"} {
		t.Run(mode, func(t *testing.T) {
			root, args := requestFixture(t)
			input := "/plan\n/quit\n"
			var calls int
			planner := func(ctx context.Context, db *store.Store, selected config.Config, task core.GraphTask,
				text string, credits float64, cli, base string) (controller.QuestionResult, error) {
				calls++
				if mode == "provider-failure" {
					return controller.QuestionResult{SessionID: "failed-session"}, errors.New("fixture failure")
				}
				answer := requestFixtureAnswer([]string{})
				if mode == "unanswered" {
					answer = requestFixtureAnswer([]string{"どの動作ですか？"})
				}
				result, err := fixturePlan(t, answer)(ctx, db, selected, task, text, credits, cli, base)
				if mode == "missing-usage" {
					result.Usage = nil
				}
				if mode == "over-limit" {
					count := int64(1000000)
					result.Usage[0].InputTokens = &count
				}
				return result, err
			}
			switch mode {
			case "no-plan":
				input = "/approve\n"
			case "quit":
				input = "/quit\n"
			case "eof":
				input = "/plan\n"
			case "unanswered":
				input = "/plan\n/approve\n"
			case "dirty":
				if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-repo":
				args[1] = "another/repo"
			}
			var out bytes.Buffer
			err := requestCommand(append(args, "コードを作って"), strings.NewReader(input), &out, planner,
				func(context.Context, []string, io.Writer) error { t.Fatal("unapproved runner started"); return nil })
			shouldSucceed := slices.Contains([]string{"quit", "eof", "missing-usage", "over-limit"}, mode)
			if shouldSucceed != (err == nil) {
				t.Fatal("unexpected request outcome", mode, err)
			}
			db, state, loadErr := loadWorkspace()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			db.Close()
			if len(state.Graph.Dispatches) != 0 || calls > 1 {
				t.Fatal("request created unapproved work or retried", calls)
			}
		})
	}
}

func TestRequestRepeatedPlanningDoesNotRequireConfigurationEdits(t *testing.T) {
	_, args := requestFixture(t)
	for range 2 {
		var out bytes.Buffer
		if err := requestCommand(append(args, "小さな変更を相談"), strings.NewReader("/plan\n/quit\n"), &out,
			fixturePlan(t, requestFixtureAnswer([]string{})), nil); err != nil {
			t.Fatal("a second request collided with the fixed configuration approval reference", err)
		}
	}
}

func TestRequestWholeModelInputIncludesLongOriginalRequestOnce(t *testing.T) {
	_, args := requestFixture(t)
	original := strings.Repeat("日本語と \"引用\"、改行\nを含む依頼。", 80)
	planner := func(ctx context.Context, db *store.Store, selected config.Config, task core.GraphTask,
		text string, credits float64, cli, base string) (controller.QuestionResult, error) {
		body, err := json.Marshal(requestFixtureAnswer([]string{}))
		if err != nil {
			t.Fatal(err)
		}
		selection, _ := json.Marshal(selected)
		worker := &requestFixtureRuntime{role: "ORCHESTRATOR", worktree: task.Worktree,
			id: "planner:" + task.ID, selection: selection, events: make(chan runtime.Event, 3)}
		worker.send = func(sent string) error {
			var input struct {
				Question string         `json:"question"`
				Context  map[string]any `json:"context"`
			}
			if err := json.NewDecoder(strings.NewReader(sent)).Decode(&input); err != nil {
				t.Fatal(err)
			}
			var count func(any) int
			count = func(value any) int {
				switch v := value.(type) {
				case string:
					if v == original {
						return 1
					}
					var nested any
					if json.Unmarshal([]byte(v), &nested) == nil {
						return count(nested)
					}
				case []any:
					total := 0
					for _, item := range v {
						total += count(item)
					}
					return total
				case map[string]any:
					total := 0
					for _, item := range v {
						total += count(item)
					}
					return total
				}
				return 0
			}
			if got := count(input.Question) + count(input.Context); got != 1 {
				t.Fatalf("entire sent input contains original request %d times, want once", got)
			}
			context := controller.QuestionContext{
				Task:    controller.PlanningTask{Task: task},
				Planner: &controller.PlannerContext{Task: task},
			}
			context.Task.Task.Title, context.Task.Task.Objective = original, original
			context.Planner.Task.Title, context.Planner.Task.Objective = original, original
			legacy, err := json.Marshal(struct {
				Question string                     `json:"question"`
				Context  controller.QuestionContext `json:"context"`
			}{original, context})
			if err != nil {
				t.Fatal(err)
			}
			if len(sent)*100 > len(legacy)*50 {
				t.Fatalf("whole model input saved less than 50%%: %d versus %d bytes", len(sent), len(legacy))
			}
			worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"fixture","inputTokens":10,"outputTokens":5}`}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(body)}
			worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
			return nil
		}
		return controller.AskPlanner(ctx, db, storePath, task.ID, text, selected, worker)
	}
	var out bytes.Buffer
	if err := requestCommand(append(args, original), strings.NewReader("/plan\n/quit\n"), &out, planner, nil); err != nil {
		t.Fatal(err)
	}
}

func TestUserGuideRequestExampleWithoutSDK(t *testing.T) {
	guide, err := os.ReadFile("../../docs/userguide/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	example := regexp.MustCompile("(?s)```sh\\n(taxion request .*?)\\n```").FindSubmatch(guide)
	if len(example) != 2 {
		t.Fatal("guide must contain one runnable request entry")
	}
	args := strings.Fields(strings.ReplaceAll(string(example[1]), "\\\n", ""))
	if len(args) < 3 {
		t.Fatal("invalid request example")
	}
	root, _ := requestFixture(t)
	args = args[2:]
	for index, value := range args {
		if value == "owner/repository" {
			args[index] = "owner/repo"
		}
	}
	args = append(args, "--worktree", root)
	var out bytes.Buffer
	if err := requestCommand(args, strings.NewReader("検索機能を追加して\n/plan\n/quit\n"), &out,
		fixturePlan(t, requestFixtureAnswer([]string{})), nil); err != nil {
		t.Fatal("guide request entry is not executable offline", err)
	}
}
