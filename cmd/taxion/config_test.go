package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
)

func TestConfigCheckFixedFilesAndTaskApproval(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	input := `version: 1
project: {id: project-a}
repository:
  provider: github
  host: github.com
  path: owner/application
  account: developer-a
  credentialRef: scm-credential
runtime:
  provider: copilot
  account: developer-b
  credentialRef: ai-credential
  contractRef: company-contract
execution:
  requester: developer-a
  worker: local-worker
  approvalRef: decision-1
`
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(userDir, "taxion", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := checkConfig([]string{"check"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "have not been verified") ||
		strings.Contains(output.String(), "ai-credential") {
		t.Fatal("check claimed external authorization or printed credential references")
	}
	oldStore := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	defer func() { storePath = oldStore }()
	if err := initCmd(); err != nil {
		t.Fatal(err)
	}
	candidate, err := config.Load([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := json.Marshal(config.Approval{
		Kind: config.ApprovalKind, TaskID: "task", Reason: "approved selection", Allowed: []config.Config{candidate},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.HasGraph = true
		state.Graph = core.TaskGraph{Version: 1}
		task := core.GraphTask{
			ID: "task", Title: "config", Repo: "owner/application", Type: "IMPLEMENTATION",
			Issue: "Issue #69", Status: core.Draft, CreatedFrom: "human:request",
			Owner: "CODER", Reviewer: "REVIEWER", Objective: "approved work", Scope: []string{"src"},
			Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"report"}, ExitCondition: "tests pass",
			ValidationCommands: []string{"go test ./..."}, Worktree: t.TempDir(), Branch: "work/task",
			BaselineSHA: strings.Repeat("a", 40), AuthorizationID: "decision-1",
			Decisions: []core.TaskDecision{{ID: "decision-1", Source: "human:selection", Text: string(approved)}},
		}
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo})
		return state.Graph.Track(task, "human", "record selection")
	}); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := checkConfig([]string{"check", "--task", "task"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "matches a sourced Task approval") ||
		!strings.Contains(output.String(), "execution readiness have not been verified") {
		t.Fatal("Task approval output overstated execution verification")
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		if err := state.Graph.Transition("task", core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
			return err
		}
		dispatch, err := core.NewGraphDispatch(state.Graph, "dispatch", "task", candidate.Repository.Path, core.RelationshipNone, "", "")
		if err != nil {
			return err
		}
		if err := state.Graph.IssueDispatch(dispatch, "controller", "issue"); err != nil {
			return err
		}
		state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{
			ID: dispatch.ID, TaskID: dispatch.TaskID, Repo: dispatch.Repository, Active: true, CreatedAt: dispatch.CreatedAt,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := checkConfig([]string{"check", "--dispatch", "dispatch", "--sources"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "saved Dispatch instruction") ||
		!strings.Contains(output.String(), "runtime configuration binding have not been verified") ||
		strings.Contains(output.String(), "ai-credential") {
		t.Fatal("Dispatch approval check exposed references or claimed runtime verification")
	}
	output.Reset()
	if err := checkConfig([]string{"bind", "--dispatch", "dispatch"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "selection and field origins saved") ||
		!strings.Contains(output.String(), "runtime session binding remain unverified") ||
		strings.Contains(output.String(), "ai-credential") {
		t.Fatal("configuration binding output exposed values or overstated runtime verification")
	}
	db, beforeAuth, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	selectedDispatch, err := selectDispatchConfiguration(db, beforeAuth.Graph.Dispatches[0])
	if err != nil || selectedDispatch.Configuration == nil {
		t.Fatal("runtime did not select the approved fixed configuration", err)
	}
	workerConfig := copilot.Config{
		Role: "CODER", SystemPrompt: "approved role", CLIPath: "/bin/true",
		WorkingDirectory: selectedDispatch.Instruction.Worktree, BaseDirectory: t.TempDir(),
	}
	selectedWorker, err := newDispatchWorker(context.Background(), db, selectedDispatch, workerConfig, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer selectedWorker.Close()
	if len(selectedWorker.ExecutionConfiguration()) == 0 {
		t.Fatal("saved selection not supplied to the SDK identity")
	}
	authArgs := []string{"auth", "--dispatch", "dispatch", "--system-prompt-file", "missing-prompt", "--cli", "missing-copilot"}
	if err := runtimeCmd(authArgs); err == nil || !strings.Contains(err.Error(), "read system prompt") {
		t.Fatal("approved auth probe did not reach local adapter inputs", err)
	}
	if err := os.Mkdir(".taxion", 0700); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(".taxion", "config.yaml")
	if err := os.WriteFile(override, []byte("version: 1\nruntime:\n  account: selected-account\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := checkConfig([]string{"check", "--sources"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "runtime.account: project") ||
		!strings.Contains(output.String(), "runtime.contractRef: user") ||
		strings.Contains(output.String(), "selected-account") {
		t.Fatal("field origins missing or selected value exposed")
	}
	if err := runtimeCmd(authArgs); err == nil ||
		!strings.Contains(err.Error(), "outside the recorded approval") ||
		strings.Contains(err.Error(), "selected-account") {
		t.Fatal("unapproved auth probe reached adapter inputs or exposed configuration", err)
	}
	if _, err := selectDispatchConfiguration(db, selectedDispatch); err == nil {
		t.Fatal("runtime accepted an unapproved configuration change")
	}
	for _, action := range []string{"start", "resume", "send", "events", "commit"} {
		args := []string{action, "--dispatch", "dispatch", "--system-prompt-file", "missing-prompt", "--cli", "missing-copilot"}
		if action == "send" {
			args = append(args, "--prompt-file", "missing-input")
		}
		if action == "commit" {
			args = append(args, "--message-file", "missing-message")
		}
		if err := runtimeCmd(args); err == nil || !strings.Contains(err.Error(), "outside the recorded approval") {
			t.Fatal("execution action bypassed fixed configuration gate", action, err)
		}
	}
	if err := runtimeCmd([]string{"stop", "--dispatch", "dispatch", "--system-prompt-file", "missing-prompt"}); err == nil ||
		!strings.Contains(err.Error(), "read system prompt") {
		t.Fatal("safe-stop attachment was blocked by current settings", err)
	}
	stopWorker, err := newDispatchWorker(context.Background(), db, selectedDispatch, workerConfig, false, false, "")
	if err != nil || stopWorker.ConfigHash() != selectedWorker.ConfigHash() {
		t.Fatal("saved identity cannot attach for safe stop after configuration changes", err)
	}
	defer stopWorker.Close()
	afterAuth, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(beforeAuth, afterAuth) {
		t.Fatal("auth preflight changed the workspace", err)
	}
	for _, args := range [][]string{
		nil, {"show"}, {"check", "extra"},
		{"check", "--task", "missing"},
		{"check", "--task", "task"},
		{"check", "--dispatch", "dispatch"},
		{"check", "--dispatch", "missing"},
		{"check", "--task", "task", "--dispatch", "dispatch"},
		{"bind"}, {"bind", "--task", "task"},
		{"bind", "--dispatch", "missing"}, {"bind", "--dispatch", "dispatch"},
	} {
		output.Reset()
		if err := checkConfig(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid invocation produced a successful configuration check")
		}
	}
	if err := os.WriteFile(path, []byte(input+"token: private-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := checkConfig([]string{"check"}, &output); err == nil ||
		strings.Contains(err.Error(), "private-secret") || output.Len() != 0 {
		t.Fatal("invalid input succeeded or leaked its value")
	}
}
