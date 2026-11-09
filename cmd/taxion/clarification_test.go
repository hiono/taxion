package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/provenance"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestClarificationCLIRecordsHumanAnswerWithoutApprovalOrDispatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	previous := storePath
	storePath = filepath.Join(dir, "ledger.json")
	t.Cleanup(func() { storePath = previous })
	root, baseline := createGitWorktree(t, "work/task", "https://github.com/owner/repo.git")
	task := core.GraphTask{
		ID: "task", Title: "Clarify acceptance", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Issue: "Issue #283", Status: core.Draft, Owner: "CODER", Reviewer: "REVIEWER",
		CreatedFrom: "human:request", Objective: "Preserve the accepted contract",
		Scope: []string{"README.md"}, Acceptance: []string{"approved behavior"},
		Worktree: root, Branch: "work/task", BaselineSHA: baseline,
	}
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(task, "human", "original request"); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.InitializeWorkspace(store.Workspace{
		HasGraph: true, Graph: graph, Ledger: core.Ledger{
			Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "taxion")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build public CLI: %s: %v", output, err)
	}
	run := func(args ...string) []byte {
		t.Helper()
		output, err := exec.Command(binary, append([]string{"--store", storePath}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("public CLI %v: %s: %v", args, output, err)
		}
		return output
	}
	policy, err := json.Marshal(provenance.Policy{SchemaVersion: 1, Mode: "development", StoreDirectory: dir})
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, policy, 0600); err != nil {
		t.Fatal(err)
	}
	run("provenance", "configure", "--file", policyPath)
	var output bytes.Buffer
	output.Write(run("conversation", "clarification", "observe", "--task", "task"))
	var observed controller.ClarificationContext
	if err := json.Unmarshal(output.Bytes(), &observed); err != nil {
		t.Fatal(err)
	}
	request := controller.ClarificationRequest{
		ID: "acceptance", TaskID: task.ID, Role: "PLANNER", Category: "specification",
		BasisHash: observed.Hash, Question: "Which behavior is intended?", Reason: "The owner is not recorded",
		Uncertainty: "intended-behavior", References: []string{"taxion:clarification-task"},
		AnswerFormat: "text", DecisionImpact: "Select one interpretation",
		StopCondition: "Do not execute the dependent change", Deadline: time.Now().Add(time.Hour),
	}
	write := func(name string, value any) string {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	output.Reset()
	output.Write(run("conversation", "clarification", "request", "--file", write("question.json", request)))
	var routed controller.Clarification
	if err := json.Unmarshal(output.Bytes(), &routed); err != nil || routed.Recipient != "HUMAN" || routed.State != "OBSERVED" {
		t.Fatal("CLI lost the Human route or granted authority", output.String(), err)
	}
	answer := map[string]any{"id": request.ID, "answer": controller.ClarificationAnswer{
		Role: "HUMAN", Actor: "user", Source: "human:clarification", Text: "Preserve the documented behavior.",
	}}
	output.Reset()
	output.Write(run("conversation", "clarification", "answer", "--file", write("answer.json", answer)))
	output.Reset()
	output.Write(run("conversation", "clarification", "history"))
	var history []controller.Clarification
	if err := json.Unmarshal(output.Bytes(), &history); err != nil || len(history) != 2 ||
		history[1].Answer == nil || history[1].Answer.At.IsZero() || history[1].State != "OBSERVED" {
		t.Fatal("CLI reopen lost provenance or accepted the answer automatically", output.String(), err)
	}
	after, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	after.Records.Requests = before.Records.Requests
	if !reflect.DeepEqual(before, after) {
		t.Fatal("answer changed Task, approval, Dispatch, HOLD or runtime ownership")
	}
}
