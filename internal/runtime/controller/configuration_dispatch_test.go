package controller

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestDispatchConfigurationUsesIssuedApprovalAndCurrentTask(t *testing.T) {
	for _, failure := range []string{"", "absent-snapshot", "candidate", "blocked", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			candidate, err := config.Load([]byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`))
			if err != nil {
				t.Fatal(err)
			}
			approval := config.Approval{Kind: config.ApprovalKind, TaskID: "task", Reason: "authorize this combination", Allowed: []config.Config{candidate}}
			data, err := json.Marshal(approval)
			if err != nil {
				t.Fatal(err)
			}
			decision := core.TaskDecision{ID: "approval", Source: "human:selection", Text: string(data)}
			task := core.GraphTask{
				ID: "task", Title: "approved execution", Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #69",
				Status: core.Draft, Owner: "CODER", Reviewer: "REVIEWER", CreatedFrom: "human:request",
				Objective: "approved work", Scope: []string{"src"}, Acceptance: []string{"tests pass"},
				ValidationCommands: []string{"go test ./..."}, RequiredEvidence: []string{"report"}, ExitCondition: "tests pass",
				Worktree: t.TempDir(), Branch: "work/task", BaselineSHA: strings.Repeat("a", 40),
				AuthorizationID: "authorization", Decisions: []core.TaskDecision{{ID: "authorization", Source: "human:1", Text: "authorize scope"}},
			}
			if failure != "absent-snapshot" {
				task.Decisions = append(task.Decisions, decision)
			}
			graph := core.TaskGraph{Version: 1}
			if err := graph.Track(task, "human", "register"); err != nil {
				t.Fatal(err)
			}
			if err := graph.Transition(task.ID, core.Ready, "human", "approve", core.TransitionProof{}); err != nil {
				t.Fatal(err)
			}
			dispatch, err := core.NewGraphDispatch(graph, "dispatch", task.ID, task.Repo, core.RelationshipNone, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := graph.IssueDispatch(dispatch, "controller", "issue"); err != nil {
				t.Fatal(err)
			}
			if failure == "absent-snapshot" {
				task, _ = graph.GraphTask(task.ID)
				task.Decisions = append(task.Decisions, decision)
				// Exercise a valid later Task record, without editing the issued instruction.
				graph.Tasks[0] = task
				graph.History = append(graph.History, core.GraphEvent{
					TaskID: task.ID, Actor: "human", Reason: "later approval record", At: graph.History[len(graph.History)-1].At, Task: task,
				})
			}
			if failure == "blocked" {
				if err := graph.Transition(task.ID, core.Blocked, "human", "pause", core.TransitionProof{}); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "cancelled" {
				result := &core.DispatchResult{DispatchID: dispatch.ID, TaskID: task.ID, Recipient: dispatch.Recipient,
					Repository: task.Repo, Status: core.DispatchCancelled, StartedSHA: dispatch.ExpectedStartSHA(),
					Evidence: []string{"human:cancel"}, NextTransition: core.Blocked}
				if err := graph.TransitionDispatch(dispatch.ID, core.DispatchCancelled, "human", "cancel", false, false, result); err != nil {
					t.Fatal(err)
				}
			}
			db, err := store.Open(filepath.Join(t.TempDir(), "workspace.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.InitializeWorkspace(store.Workspace{
				HasGraph: true, Graph: graph,
				Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}},
					Dispatches: []core.Dispatch{{ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: failure != "cancelled", CreatedAt: dispatch.CreatedAt}}},
			}); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if failure == "candidate" {
				candidate.Runtime.ContractRef = "different"
			}
			got, err := CheckDispatchConfiguration(db, dispatch.ID, candidate)
			if (err == nil) != (failure == "") || failure == "" && got != decision {
				t.Fatal("Dispatch approval boundary mismatch", failure, err)
			}
			if failure == "absent-snapshot" {
				if !strings.Contains(err.Error(), "absent from the saved Dispatch instruction") {
					t.Fatal("later approval rejected for an unrelated reason", err)
				}
				if _, err := CheckConfiguration(db, task.ID, candidate); err != nil {
					t.Fatal("fixture does not have a valid current Task approval", err)
				}
			}
			if _, err := CheckDispatchConfiguration(db, "missing", candidate); err == nil {
				t.Fatal("missing Dispatch accepted")
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("read-only check changed durable workspace", err)
			}
		})
	}
}
