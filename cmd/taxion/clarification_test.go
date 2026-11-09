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
	var assessment controller.ClarificationAssessment
	if err := json.Unmarshal(run("conversation", "clarification", "assess", "--id", request.ID), &assessment); err != nil ||
		assessment.State != "OBSERVED" || assessment.Decision.Verdict != "blocked" {
		t.Fatal("unanswered question was admitted", assessment, err)
	}
	output.Reset()
	output.Write(run("conversation", "clarification", "answer", "--file", write("answer.json", answer)))
	if err := json.Unmarshal(run("conversation", "clarification", "assess", "--id", request.ID), &assessment); err != nil ||
		assessment.State != "OBSERVED" || assessment.Decision.Verdict != "accept" {
		t.Fatal("observed answer was not eligible as input", assessment, err)
	}
	output.Reset()
	output.Write(run("conversation", "clarification", "history"))
	var history []controller.Clarification
	if err := json.Unmarshal(output.Bytes(), &history); err != nil || len(history) != 2 ||
		history[1].Answer == nil || history[1].Answer.At.IsZero() || history[1].State != "OBSERVED" {
		t.Fatal("CLI reopen lost provenance or accepted the answer automatically", output.String(), err)
	}
	conflict := map[string]any{"id": request.ID, "answer": controller.ClarificationAnswer{
		Role: "HUMAN", Actor: "user", Source: "human:second-interpretation", Text: "Replace the documented behavior.",
	}}
	args := []string{"--store", storePath, "conversation", "clarification", "answer", "--file", write("conflict.json", conflict)}
	if output, err := exec.Command(binary, args...).CombinedOutput(); err == nil || !bytes.Contains(output, []byte("retained as OBSERVED")) {
		t.Fatal("public CLI accepted a conflicting answer", string(output), err)
	}
	if err := json.Unmarshal(run("conversation", "clarification", "assess", "--id", request.ID), &assessment); err != nil ||
		assessment.Decision.Verdict != "blocked" {
		t.Fatal("conflict did not block input admission", assessment, err)
	}
	if err := json.Unmarshal(run("conversation", "clarification", "history"), &history); err != nil || len(history) != 3 ||
		history[2].Conflict == nil || history[2].Conflict.Source != "human:second-interpretation" ||
		history[2].Answer.Text != "Preserve the documented behavior." {
		t.Fatal("public CLI did not preserve both answer sources", history, err)
	}
	resolution := controller.ClarificationResolutionRequest{
		ID: request.ID, ExpectedHash: assessment.Decision.BasisHash, Selected: assessment.Candidates[1].Receipt,
		Role: "HUMAN", Actor: "specification-owner", Source: "human:explicit-judgment",
		Reason: "The second interpretation is intended; this does not approve execution.",
	}
	run("conversation", "clarification", "resolve-answer", "--file", write("resolution.json", resolution))
	if err := json.Unmarshal(run("conversation", "clarification", "assess", "--id", request.ID), &assessment); err != nil ||
		assessment.State != "OBSERVED" || assessment.Decision.Verdict != "accept" || assessment.Resolution == nil ||
		assessment.Resolution.Selected != resolution.Selected {
		t.Fatal("public CLI did not retain the explicit observational judgment", assessment, err)
	}
	if err := json.Unmarshal(run("conversation", "clarification", "history"), &history); err != nil || len(history) != 4 ||
		history[3].Answer.Text != "Preserve the documented behavior." || history[3].Resolution == nil {
		t.Fatal("resolution overwrote the original evidence", history, err)
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
