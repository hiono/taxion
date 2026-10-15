package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestConversationQuestionRequiresTimeAndCreditLimitsBeforeOpeningWorkspace(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"run"},
		{"ask"},
		{"ask", "--task", "task", "--question", "今どうなっていますか？"},
		{"ask", "--task", "task", "--question", "status?", "--timeout", "1m"},
		{"ask", "--task", "task", "--question", "status?", "--timeout", "1m", "--max-ai-credits", "1"},
		{"ask", "--task", "task", "--question", "status?", "--timeout", "0s", "--max-ai-credits", "30"},
		{"ask", "--task", "task", "--question", "status?", "--timeout", "1m", "--max-ai-credits", "30", "extra"},
		{"plan", "--task", "task", "--question", "Repair or replan?"},
		{"plan", "--task", "task", "--question", "Repair or replan?", "--timeout", "1m", "--max-ai-credits", "1"},
		{"investigate", "--task", "task", "--question", "Read a file"},
		{"investigate", "--task", "task", "--question", "Read a file", "--timeout", "1m", "--max-ai-credits", "30"},
		{"investigate", "--task", "task", "--question", "Read a file", "--timeout", "1m", "--max-ai-credits", "30", "--path", "src/a.go", "--max-bytes", "65537"},
	} {
		var output bytes.Buffer
		if err := conversationCmd(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid conversation invocation created a result", args, err)
		}
	}
}

func TestPlannerSchemaRequiresAdvisoryActionWithoutChangingSideQuestionSchema(t *testing.T) {
	ordinary := questionResponseSchema()
	planner := plannerResponseSchema()
	if ordinary["properties"].(map[string]any)["recommendation"] != nil ||
		!reflect.DeepEqual(planner["required"], []string{"recommendation", "confirmed", "assumptions", "unconfirmed"}) {
		t.Fatal("planner schema changed ordinary questions or omitted a required action")
	}
	actions := planner["properties"].(map[string]any)["recommendation"].(map[string]any)["enum"]
	if !reflect.DeepEqual(actions, []string{"REVIEW", "REPAIR", "REPLAN", "INVESTIGATE", "HOLD", "HUMAN_REQUIRED", "REPORT"}) {
		t.Fatal("planner schema authorizes execution or review approval")
	}
	for _, description := range []string{"/planner/task is the GraphTask itself", "/planner/task/acceptance/0",
		"/planner/results/N/result", "/planner/memory/N", "/planner/flows/N/held"} {
		if !strings.Contains(plannerSystemPrompt, description) {
			t.Fatal("Planner prompt no longer distinguishes its actual citation shape", description)
		}
	}
}

func TestQuestionCitationPathsUseActualRecordsWithoutInventedWrappers(t *testing.T) {
	paths, err := questionCitationPaths(map[string]any{
		"planner": map[string]any{
			"task":    map[string]any{"status": "READY", "acceptance": []string{"criterion"}},
			"results": []any{map[string]any{"result": map[string]any{"status": "SUCCEEDED"}}},
		},
		"task": map[string]any{"task": map[string]any{"status": "READY"}, "absent": nil,
			"history": []any{map[string]any{"task": map[string]any{"status": "DRAFT"}}}},
		"observation": map[string]any{"contentHash": "hash", "a/b~c": "escaped"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []string{"/planner/task/status", "/planner/task/acceptance/0",
		"/planner/results/0/result", "/task/task/status", "/task/history", "/observation/a~1b~0c"} {
		if !slices.Contains(paths, valid) {
			t.Fatal("missing actual record source", valid)
		}
	}
	for _, invalid := range []string{"/planner/task/task/status", "/task/task/proof/actionPerformed",
		"/task/absent", "/task/history/0/task/status", "/planner/results/0/result/missing"} {
		if slices.Contains(paths, invalid) {
			t.Fatal("citation schema permits invented or recursively repeated evidence", invalid)
		}
	}
	again, err := questionCitationPaths(map[string]any{
		"b": map[string]any{"z": 1, "a": 2}, "a": map[string]any{"b": 3},
	})
	if err != nil || !reflect.DeepEqual(again, []string{"/a/b", "/b/a", "/b/z"}) {
		t.Fatal("citation ordering is not deterministic", again, err)
	}
}

func TestRuntimePromptSeparatesSubmissionFromControllerValidation(t *testing.T) {
	prompt := runtimeSubmissionPrompt("Independent role", "immutable-contract", true)
	for _, requirement := range []string{"immutable-contract", "never invent execution",
		"submit status SUCCEEDED and validations []", "Pending controller validation alone",
		"before persisting a successful result", "independently determine its verdict"} {
		if !strings.Contains(prompt, requirement) {
			t.Fatal("runtime prompt lost the submission/verification boundary", requirement)
		}
	}
	if runtimeSystemPrompt("Independent role", "immutable-contract") != runtimeSubmissionPrompt("Independent role", "immutable-contract", false) ||
		strings.Contains(runtimeSystemPrompt("Independent role", "immutable-contract"), "Pending controller validation alone") {
		t.Fatal("opt-in submission policy changed the legacy prompt or saved-session identity")
	}
}

func TestStrictResultSchemaPinsIssuedIdentitiesAndRepairStart(t *testing.T) {
	for _, test := range []struct {
		name, role, target, expectedStart string
	}{
		{"initial-coder", "CODER", "", strings.Repeat("a", 40)},
		{"repair-coder", "CODER", strings.Repeat("b", 40), strings.Repeat("b", 40)},
		{"reviewer", "REVIEWER", strings.Repeat("a", 40), strings.Repeat("a", 40)},
	} {
		t.Run(test.name, func(t *testing.T) {
			dispatch := core.GraphDispatch{
				ID: "dispatch", TaskID: "task", Recipient: test.role, Repository: "owner/repo",
			}
			dispatch.Instruction.Version = core.DispatchInstructionVersion
			dispatch.Instruction.BaselineSHA = strings.Repeat("a", 40)
			dispatch.Instruction.TargetSHA = test.target
			ordinary := dispatchResultSchema(dispatch)["properties"].(map[string]any)
			strict := strictDispatchResultSchema(dispatch)["properties"].(map[string]any)
			transitions := strict["nextTransition"].(map[string]any)["enum"].([]core.TaskStatus)
			if !reflect.DeepEqual(transitions, []core.TaskStatus{
				core.Draft, core.Ready, core.Active, core.WaitingForEvidence, core.WaitingForReview,
				core.RequestChanges, core.Blocked, core.HumanDecisionRequired, core.Approved,
				core.Completed, core.Rejected, core.Cancelled, core.Superseded,
			}) || ordinary["nextTransition"].(map[string]any)["enum"] != nil {
				t.Fatal("strict schema must allow actual Task states without changing legacy schemas")
			}
			for field, expected := range map[string]string{
				"dispatchId": dispatch.ID, "taskId": dispatch.TaskID, "recipient": dispatch.Recipient,
				"repository": dispatch.Repository, "startedSha": test.expectedStart,
			} {
				if ordinary[field].(map[string]any)["enum"] != nil ||
					!reflect.DeepEqual(strict[field].(map[string]any)["enum"], []string{expected}) {
					t.Fatal("strict response lost issued identity or changed legacy schema", field)
				}
			}
			for _, field := range []string{"outputSha", "status", "reviewVerdict", "acceptance", "findings"} {
				if !reflect.DeepEqual(ordinary[field], strict[field]) {
					t.Fatal("strict identities guessed a mutable outcome", field)
				}
			}
		})
	}
}

type checkedConversationReader struct {
	io.Reader
	check func()
}

func (r checkedConversationReader) Read(p []byte) (int, error) {
	r.check()
	return r.Reader.Read(p)
}

func TestWatchedConversationHoldsBeforeInputAndNeverApprovesOnEOF(t *testing.T) {
	for _, text := range []string{"", "interactive", "現在どうなっていますか？\n", "/adopt\n/quit\n", "/apply REVIEWER 0 Check the finding\n/quit\n", "/investigate src/a.go Read this file\n/apply REVIEWER 0 Check the finding\n/quit\n", "/plan Should we revise the plan?\n/adopt\n/quit\n", "/draft Check the saved acceptance carefully.\n/approve\n/quit\n", "/step\n/quit\n"} {
		t.Run(text, func(t *testing.T) {
			old := storePath
			dir := t.TempDir()
			storePath = filepath.Join(dir, "ledger.json")
			t.Cleanup(func() { storePath = old })
			db, err := store.Open(workspaceDBPath())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			task := core.GraphTask{
				ID: "work", Title: "Application", Repo: "owner/repo", Type: "IMPLEMENTATION",
				Status: core.Draft, Issue: "Issue #53", CreatedFrom: "human:1",
				Owner: "CODER", Reviewer: "REVIEWER", Objective: "Implement application",
				Scope: []string{"src"}, Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
				ValidationCommands: []string{"go test ./..."}, ExitCondition: "tests pass",
				Worktree: filepath.Join(dir, "repo"), Branch: "work", BaselineSHA: strings.Repeat("a", 40),
				AuthorizationID: "authorize", Decisions: []core.TaskDecision{{ID: "authorize", Source: "human:1", Text: "approve"}},
			}
			graph := core.TaskGraph{Version: 1}
			if err := graph.Track(task, "human", "track"); err != nil {
				t.Fatal(err)
			}
			if err := graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.InitializeWorkspace(store.Workspace{
				HasGraph: true, Graph: graph, Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}},
			}); err != nil {
				t.Fatal(err)
			}
			repairs := 1
			flow, err := controller.StartReviewFlow(db, storePath, controller.ReviewFlowRequest{
				ID: "watch", TaskID: task.ID, Repository: task.Repo, ReviewRoot: filepath.Join(dir, "reviews"),
				MaxRepairs: &repairs, TimeoutSeconds: 60,
				Authorization: core.TaskDecision{ID: "flow-approval", Source: "human:1", Text: "explicit bounded approval"},
			})
			if err != nil {
				t.Fatal(err)
			}
			reader := checkedConversationReader{Reader: strings.NewReader(text), check: func() {
				state, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				current, _ := state.Graph.ReviewFlow(flow.ID)
				if !current.HandoffHeld() {
					t.Fatal("console interpreted Human input before persisting the hold")
				}
			}}
			var output bytes.Buffer
			args := []string{"--flow", flow.ID, "--actor", "human", "--source", "human:terminal"}
			if text == "interactive" {
				input, writer := io.Pipe()
				defer input.Close()
				started := make(chan struct{})
				run := func(ctx context.Context, args []string, out io.Writer) error {
					close(started)
					if _, err := io.WriteString(out, "progress while Human input is available\n"); err != nil {
						return err
					}
					<-ctx.Done()
					return ctx.Err()
				}
				go func() {
					defer writer.Close()
					if _, err := io.WriteString(writer, "/step\n"); err != nil {
						return
					}
					select {
					case <-started:
						_, _ = io.WriteString(writer, "/step\n/hold\n/quit\n")
					case <-time.After(5 * time.Second):
						_ = writer.CloseWithError(errors.New("interactive runner never started"))
					}
				}()
				err := watchedConversation(append(args, "--interactive-execution"), input, &output, run)
				if !errors.Is(err, context.Canceled) || !strings.Contains(output.String(), "already owns an execution") ||
					!strings.Contains(output.String(), "progress while Human input") {
					t.Fatal("interactive input blocked, allowed a second runner or ignored EOF safe stop", err, output.String())
				}
			} else {
				if err := conversationWatch(args, reader, &output); err != nil {
					t.Fatal(err)
				}
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			current, _ := state.Graph.ReviewFlow(flow.ID)
			if !current.HandoffHeld() || current.RunnerID != "" || len(state.Graph.Dispatches) != 1 ||
				state.Graph.Dispatches[0].RuntimeSessionID != "" || current.Authorization != flow.Authorization {
				t.Fatal("console EOF or invalid input ran or approved a role")
			}
			for _, draft := range current.InstructionDrafts {
				if draft.Approval != nil || draft.Publication != nil {
					t.Fatal("pre-execution draft silently approved or published")
				}
			}
			if text != "" && !strings.Contains(output.String(), "Operation rejected:") {
				t.Fatal("rejected operation failed silently")
			}
		})
	}
}
