package controller

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestConfigurationApprovalPersistsAndDoesNotChangeTask(t *testing.T) {
	candidate, err := config.Load([]byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config.Approval{
		Kind: config.ApprovalKind, TaskID: "task", Reason: "Human approved this execution", Allowed: []config.Config{candidate},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := core.TaskGraph{Version: 1}
	task := core.GraphTask{
		ID: "task", Title: "test config", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Issue: "Issue #69", Status: core.Draft, CreatedFrom: "human:request",
		Decisions: []core.TaskDecision{{ID: "approval", Source: "human:selection", Text: string(data)}},
	}
	if err := graph.Track(task, "human", "record approved identity combination"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(store.Workspace{
		HasGraph: true, Graph: graph,
		Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	decision, err := CheckConfiguration(db, task.ID, candidate)
	if err != nil || decision.Source != "human:selection" {
		t.Fatal("sourced approval was not restored", err)
	}
	if _, err := CheckConfiguration(db, "missing-task", candidate); err == nil {
		t.Fatal("missing Task accepted")
	}
	changed := candidate
	changed.Runtime.ContractRef = "different-contract"
	if _, err := CheckConfiguration(db, task.ID, changed); err == nil {
		t.Fatal("unapproved contract accepted")
	}
	changed = candidate
	changed.Repository.Path = "other/repo"
	if _, err := CheckConfiguration(db, task.ID, changed); err == nil {
		t.Fatal("different repository accepted")
	}
	changed = candidate
	changed.Execution.ApprovalRef = "absent"
	if _, err := CheckConfiguration(db, task.ID, changed); err == nil {
		t.Fatal("missing decision accepted")
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("approval check mutated durable state", err)
	}
}
