package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestConfigCheckIsExplicitAndDoesNotClaimAuthentication(t *testing.T) {
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
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := checkConfig([]string{"check", "--file", path}, &output); err != nil {
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
			Decisions: []core.TaskDecision{{ID: "decision-1", Source: "human:selection", Text: string(approved)}},
		}
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo})
		return state.Graph.Track(task, "human", "record selection")
	}); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := checkConfig([]string{"check", "--file", path, "--task", "task"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "matches a sourced Task approval") ||
		!strings.Contains(output.String(), "execution readiness have not been verified") {
		t.Fatal("Task approval output overstated execution verification")
	}
	override := filepath.Join(filepath.Dir(path), "override.yaml")
	if err := os.WriteFile(override, []byte("version: 1\nruntime:\n  account: selected-account\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := checkConfig([]string{"check", "--user-file", path, "--file", override, "--sources"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "runtime.account: invocation") ||
		!strings.Contains(output.String(), "runtime.contractRef: user") ||
		strings.Contains(output.String(), "selected-account") {
		t.Fatal("field origins missing or selected value exposed")
	}
	for _, args := range [][]string{
		nil, {"show"}, {"check"}, {"check", "--file", path, "extra"},
		{"check", "--file", path + ".missing"},
		{"check", "--file", path, "--task", "missing"},
		{"check", "--user-file", path, "--file", override, "--task", "task"},
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
	if err := checkConfig([]string{"check", "--file", path}, &output); err == nil ||
		strings.Contains(err.Error(), "private-secret") || output.Len() != 0 {
		t.Fatal("invalid input succeeded or leaked its value")
	}
}
