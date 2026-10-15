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
	return root, []string{"--repo", "owner/repo", "--worktree", root, "--max-tokens", "1000",
		"--timeout", "1m", "--max-ai-credits", "30", "--max-repairs", "0", "--context-path", "README.md"}
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

func fixturePlan(t *testing.T, answer controller.QuestionAnswer) requestPlanner {
	t.Helper()
	return func(ctx context.Context, db *store.Store, selected config.Config, task core.GraphTask,
		text string, tokens int64, _ float64, _, _ string) (controller.QuestionResult, error) {
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
			first := conversation.Messages[0]
			if context.Task.ID != task.ID || request.Request == "" ||
				context.Task.Title == request.Request || context.Task.Objective == request.Request ||
				first.Text != "" || first.Source != "/planner/task/decisions/0/text" {
				t.Fatal("original request was lost or repeated outside its exact saved decision")
			}
			worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"fixture","inputTokens":10,"outputTokens":5}`}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(body)}
			worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
			return nil
		}
		return controller.AskPlanner(ctx, db, storePath, task.ID, text, selected, tokens, worker)
	}
}

func TestRequestRefinesHumanIntentAndRunsSameTreeReviewWithoutCopyPaste(t *testing.T) {
	root, args := requestFixture(t)
	var calls, roles int
	planner := func(ctx context.Context, db *store.Store, selected config.Config, task core.GraphTask,
		text string, tokens int64, credits float64, cli, base string) (controller.QuestionResult, error) {
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
			if !strings.Contains(text, "部分一致") || !strings.Contains(text, "planner") || tokens != 985 {
				t.Fatal("Planner lost the user's refinement, earlier question or shared token ceiling", text, tokens)
			}
			answer = requestFixtureAnswer([]string{})
		}
		return fixturePlan(t, answer)(ctx, db, selected, task, text, tokens, credits, cli, base)
	}
	runner := func(ctx context.Context, runArgs []string, output io.Writer) error {
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
		if flow.MaxTokens != 970 || flow.MaxRepairs != 0 {
			t.Fatal("flow did not subtract all Planner usage", flow)
		}
		_, err = controller.RunReviewFlow(ctx, db, storePath, flowID, func(dispatch core.GraphDispatch) (runtime.Identity, error) {
			dispatch, err = selectDispatchConfiguration(db, dispatch)
			if err != nil {
				return nil, err
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
					result.Acceptance = []core.DispatchAcceptance{{Criterion: dispatch.Instruction.Acceptance[0],
						Status: "PASS", Evidence: []string{"work-report"}}}
				}
				body, err := json.Marshal(result)
				if err != nil {
					return err
				}
				worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"fixture","inputTokens":10,"outputTokens":5}`}
				worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(body)}
				worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
				return nil
			}
			return worker, nil
		}, output)
		return err
	}
	var out bytes.Buffer
	if err := requestCommand(append(args, "検索機能をつけて"), strings.NewReader("/plan\n部分一致です。\n/approve\n"), &out, planner, runner); err != nil {
		t.Fatal(err, out.String())
	}
	if calls != 2 || roles != 2 || !strings.Contains(out.String(), "結果: COMPLETED") {
		t.Fatal("copy-free request did not complete", calls, roles, out.String())
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	coder, reviewer := state.Graph.Dispatches[0], state.Graph.Dispatches[1]
	var intake struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal([]byte(coder.Instruction.Decisions[0].Text), &intake); err != nil {
		t.Fatal(err)
	}
	var executionRequest struct {
		PlanningTaskID string           `json:"planningTaskId"`
		Conversation   []requestMessage `json:"conversation"`
	}
	if err := json.Unmarshal([]byte(intake.Request), &executionRequest); err != nil {
		t.Fatal(err)
	}
	if executionRequest.PlanningTaskID != coder.TaskID+"-planning" ||
		len(executionRequest.Conversation) != 3 || executionRequest.Conversation[0].Text != "検索機能をつけて" ||
		executionRequest.Conversation[0].Source != "" ||
		executionRequest.Conversation[2].Text != "部分一致です。" ||
		strings.Contains(intake.Request, `"committedFilesByDirectory"`) ||
		strings.Contains(intake.Request, `"committedContext"`) {
		t.Fatal("execution lost original intent or feedback, retained a dangling Planner reference, or copied planning-only files")
	}
	if coder.Instruction.BudgetGuidance == nil || coder.Instruction.BudgetGuidance.Mode != "flow" ||
		reviewer.Instruction.BudgetGuidance == nil || reviewer.Instruction.BudgetGuidance.Mode != "flow" {
		t.Fatal("request did not retain default guidance through independent review")
	}
	if coder.Result.OutputTreeSHA == "" || coder.Result.OutputTreeSHA != reviewer.Result.OutputTreeSHA ||
		gitTest(t, root, "rev-parse", "HEAD") != coder.Instruction.BaselineSHA {
		t.Fatal("request changed commit identity or reviewed another tree")
	}
	var planning int
	for _, record := range state.Records.Requests {
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
	if len(state.Records.Requests) != 3 {
		t.Fatal("execution events were duplicated instead of referencing the Flow")
	}
	var execution struct {
		FlowID string `json:"flowId"`
		Error  string `json:"error"`
	}
	if err := core.DecodeGraphInput(strings.NewReader(state.Records.Requests[2].Payload), &execution); err != nil ||
		execution.FlowID != state.Graph.ReviewFlows[0].ID || execution.Error != "" {
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
	for _, mode := range []string{"no-plan", "quit", "eof", "unanswered", "provider-failure", "missing-usage", "ceiling", "dirty", "wrong-repo"} {
		t.Run(mode, func(t *testing.T) {
			root, args := requestFixture(t)
			input := "/plan\n/quit\n"
			var calls int
			planner := func(ctx context.Context, db *store.Store, selected config.Config, task core.GraphTask,
				text string, tokens int64, credits float64, cli, base string) (controller.QuestionResult, error) {
				calls++
				if mode == "provider-failure" {
					return controller.QuestionResult{SessionID: "failed-session"}, errors.New("fixture failure")
				}
				answer := requestFixtureAnswer([]string{})
				if mode == "unanswered" {
					answer = requestFixtureAnswer([]string{"どの動作ですか？"})
				}
				result, err := fixturePlan(t, answer)(ctx, db, selected, task, text, tokens, credits, cli, base)
				if mode == "missing-usage" {
					result.Usage = nil
				}
				if mode == "ceiling" {
					count := tokens
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
			if (mode == "quit" || mode == "eof") != (err == nil) {
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
		text string, tokens int64, credits float64, cli, base string) (controller.QuestionResult, error) {
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
		return controller.AskPlanner(ctx, db, storePath, task.ID, text, selected, tokens, worker)
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
