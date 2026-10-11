package main

import (
	"bytes"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestConversationQuestionRequiresExplicitLimitsBeforeOpeningWorkspace(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"run"},
		{"ask"},
		{"ask", "--task", "task", "--question", "今どうなっていますか？"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "1000", "--timeout", "1m"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "1000", "--timeout", "1m", "--max-ai-credits", "1"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "-1", "--timeout", "1m", "--max-ai-credits", "30"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "1000", "--timeout", "0s", "--max-ai-credits", "30"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "1000", "--timeout", "1m", "--max-ai-credits", "30", "extra"},
		{"plan", "--task", "task", "--question", "Repair or replan?"},
		{"plan", "--task", "task", "--question", "Repair or replan?", "--max-tokens", "1000", "--timeout", "1m", "--max-ai-credits", "1"},
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
	for _, text := range []string{"", "現在どうなっていますか？\n", "/adopt\n/quit\n", "/plan Should we revise the plan?\n/adopt\n/quit\n", "/draft Check the saved acceptance carefully.\n/approve\n/quit\n", "/step\n/quit\n"} {
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
				MaxRepairs: &repairs, MaxTokens: 5000, TimeoutSeconds: 60,
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
			if err := conversationWatch([]string{"--flow", flow.ID, "--actor", "human", "--source", "human:terminal"}, reader, &output); err != nil {
				t.Fatal(err)
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
