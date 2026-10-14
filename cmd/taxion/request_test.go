package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
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
		worker.send = func(string) error {
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
	if coder.Instruction.BudgetGuidance == nil || coder.Instruction.BudgetGuidance.Mode != "flow" ||
		reviewer.Instruction.BudgetGuidance == nil || reviewer.Instruction.BudgetGuidance.Mode != "flow" {
		t.Fatal("request did not retain default guidance through independent review")
	}
	if coder.Result.OutputTreeSHA == "" || coder.Result.OutputTreeSHA != reviewer.Result.OutputTreeSHA ||
		gitTest(t, root, "rev-parse", "HEAD") != coder.Instruction.BaselineSHA {
		t.Fatal("request changed commit identity or reviewed another tree")
	}
	receipts, err := filepath.Glob(filepath.Join(filepath.Dir(storePath), "requests", "*", "planning-*.json"))
	if err != nil || len(receipts) != 2 {
		t.Fatal("Planner conversation or usage receipts missing", receipts, err)
	}
	for _, path := range receipts {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("request receipt is not private", err)
		}
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
