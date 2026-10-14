package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
)

func TestTaskGraphCommandsAndDispatchGate(t *testing.T) {
	old := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = old })
	memoryStore, err := memory.Open(memory.PathForLedger(storePath))
	if err != nil {
		t.Fatal(err)
	}
	memoryID, err := memoryStore.Put(memory.KindDecision, memory.Entry{
		Title: "Keep the review SHA fixed", Body: "Rerun review for every new output SHA.",
		SourceRefs: []string{"issue:52"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := memoryStore.Close(); err != nil {
		t.Fatal(err)
	}
	worktree, baseline := createGitWorktree(t, "work/task", "https://github.com/owner/repo.git")
	task := core.Task{ID: "task", Title: "concern", Repo: "owner/repo"}
	if err := (core.Ledger{Tasks: []core.Task{task}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	gt := core.GraphTask{ID: task.ID, Title: task.Title, Repo: task.Repo, Type: "INVESTIGATION", Status: core.Blocked,
		Issue: "Issue #47", CreatedFrom: "human:1", DeferredReason: "scope out", ResumeCondition: "approved", NextAction: "investigate",
		Owner: "CODER", Objective: "investigate", Scope: []string{"docs"}, Acceptance: []string{"report"}, RequiredEvidence: []string{"report"}, ExitCondition: "report",
		ValidationCommands: []string{"test -f docs/guide.md"},
		Worktree:           worktree, Branch: "work/task", BaselineSHA: baseline,
		MemoryRefs: []string{fmt.Sprintf("decision:%d", memoryID)},
		FileAccess: &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"docs/guide.md"}}}
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
	if err := taskCmd([]string{"plan", "--repo", task.Repo}); err != nil {
		t.Fatal(err)
	}
	if err := taskCmd([]string{"plan"}); err == nil {
		t.Fatal("planning inferred a repository")
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
	snapshots := state.Graph.Dispatches[0].Instruction.MemoryContext
	if len(snapshots) != 1 || snapshots[0].Reference != gt.MemoryRefs[0] ||
		snapshots[0].Body != "Rerun review for every new output SHA." ||
		len(snapshots[0].SourceRefs) != 1 || snapshots[0].SourceRefs[0] != "issue:52" {
		t.Fatalf("Memory context was not persisted with Dispatch: %+v", snapshots)
	}
	rendered, err := core.RenderDispatchInstruction(state.Graph.Dispatches[0])
	if err != nil || !strings.Contains(rendered, "Rerun review for every new output SHA.") {
		t.Fatalf("Memory context missing from rendered instruction: %v\n%s", err, rendered)
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
	outputSHA := commitGitFile(t, worktree, "docs/guide.md", "completed")
	resultFile := filepath.Join(t.TempDir(), "result.json")
	result := core.DispatchResult{
		DispatchID: dispatchID, TaskID: task.ID, Recipient: "CODER", Repository: task.Repo,
		Status: core.DispatchSucceeded, StartedSHA: gt.BaselineSHA,
		OutputSHA:    outputSHA,
		ChangedFiles: []string{"docs/guide.md"},
		Validations:  []core.DispatchValidation{{Command: "go test ./...", ExitCode: 0, Worktree: worktree, SHA: outputSHA}},
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

func createGitWorktree(t *testing.T, branch, remote string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, path, "init", "-b", branch)
	gitTest(t, path, "config", "user.name", "Taxion Test")
	gitTest(t, path, "config", "user.email", "taxion-test@example.com")
	gitTest(t, path, "remote", "add", "origin", remote)
	commitGitFile(t, path, "README.md", "baseline")
	return path, gitTest(t, path, "rev-parse", "HEAD")
}

func commitGitFile(t *testing.T, path, name, content string) string {
	t.Helper()
	file := filepath.Join(path, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, path, "add", "--", name)
	gitTest(t, path, "commit", "-m", "test(fixture): record test change", "-m", "intent(fixture): exercise verified result persistence")
	return gitTest(t, path, "rev-parse", "HEAD")
}

func gitTest(t *testing.T, path string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", path}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
