package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestTaskGraphCommandsAndDispatchGate(t *testing.T) {
	old := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = old })
	task := core.Task{ID: "task", Title: "concern", Repo: "owner/repo"}
	if err := (core.Ledger{Tasks: []core.Task{task}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	gt := core.GraphTask{ID: task.ID, Title: task.Title, Repo: task.Repo, Type: "INVESTIGATION", Status: core.Blocked,
		Issue: "Issue #47", CreatedFrom: "human:1", DeferredReason: "scope out", ResumeCondition: "approved", NextAction: "investigate",
		Owner: "CODER", Objective: "investigate", Scope: []string{"docs"}, Acceptance: []string{"report"}, RequiredEvidence: []string{"report"}, ExitCondition: "report",
		Worktree: "/work/task", Branch: "work/task", BaselineSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	file := filepath.Join(t.TempDir(), "request.json")
	write := func(value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(map[string]any{"task": gt, "actor": "human", "reason": "retain concern",
		"concern": core.Concern{ID: "c1", TaskID: task.ID, Source: "human:1", Text: "naming"}})
	if err := taskCmd([]string{"track", "--file", file}); err != nil {
		t.Fatal(err)
	}
	if err := taskCmd([]string{"graph"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCmd([]string{"--task", task.ID, "--repo", task.Repo}); !errors.Is(err, core.ErrGraphRejected) {
		t.Fatal("scope-out dispatched", err)
	}
	gt.DeferredReason = ""
	gt.AuthorizationID = "d1"
	gt.Decisions = []core.TaskDecision{{ID: "d1", Source: "human:2", Text: "resume investigation"}}
	write(map[string]any{"task": gt, "actor": "human", "reason": "approve"})
	if err := taskCmd([]string{"track", "--file", file}); err != nil {
		t.Fatal(err)
	}
	write(map[string]any{"taskId": task.ID, "status": core.Ready, "actor": "human", "reason": "ready"})
	if err := taskCmd([]string{"transition", "--file", file}); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCmd([]string{"--task", task.ID, "--repo", task.Repo}); err != nil {
		t.Fatal(err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(state.Ledger.Dispatches) != 1 {
		t.Fatal("dispatch missing", err)
	}
	g := state.Graph
	if len(g.Concerns) != 1 || len(g.History) != 4 {
		t.Fatal("history lost", err)
	}
	dispatchID := state.Ledger.Dispatches[0].ID
	if err := dispatchCmd([]string{"transition", "--id", dispatchID, "--status", string(core.DispatchAcknowledged), "--actor", "worker", "--reason", "accepted"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCmd([]string{"transition", "--id", dispatchID, "--status", string(core.DispatchRunning), "--actor", "worker", "--reason", "started"}); err != nil {
		t.Fatal(err)
	}
	resultFile := filepath.Join(t.TempDir(), "result.json")
	result := core.DispatchResult{
		DispatchID: dispatchID, TaskID: task.ID, Recipient: "CODER", Repository: task.Repo,
		Status: core.DispatchSucceeded, StartedSHA: gt.BaselineSHA,
		OutputSHA:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ChangedFiles: []string{"docs/guide.md"},
		Validations:  []core.DispatchValidation{{Command: "go test ./...", ExitCode: 0}},
		Evidence:     []string{"report"}, NextTransition: core.WaitingForEvidence,
	}
	write(result)
	if err := os.Rename(file, resultFile); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCmd([]string{"transition", "--id", dispatchID, "--status", string(core.DispatchSucceeded), "--actor", "worker", "--reason", "finished", "--result-file", resultFile}); err != nil {
		t.Fatal(err)
	}
	db, state, err = loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if state.Ledger.Dispatches[0].Active || state.Graph.Dispatches[0].Status != core.DispatchSucceeded {
		t.Fatalf("dispatch transition not projected atomically: %+v %+v", state.Ledger.Dispatches[0], state.Graph.Dispatches[0])
	}
	write(map[string]any{"taskId": task.ID, "status": core.Completed, "actor": "human", "reason": "skip"})
	if err := taskCmd([]string{"transition", "--file", file}); err == nil {
		t.Fatal("invalid transition")
	}
	if err := taskCmd([]string{"track"}); err == nil {
		t.Fatal("missing request")
	}
}
