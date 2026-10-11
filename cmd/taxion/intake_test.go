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
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestConversationIntakeRequiresExactHumanConfirmationBeforeDispatch(t *testing.T) {
	old := storePath
	dir := t.TempDir()
	storePath = filepath.Join(dir, "ledger.json")
	t.Cleanup(func() { storePath = old })
	db, err := store.Open(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.InitializeWorkspace(store.Workspace{Ledger: core.Ledger{}}); err != nil {
		t.Fatal(err)
	}
	task := core.GraphTask{
		ID: "intake", Title: "Requested work", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Status: core.Draft, Issue: "Issue #54", CreatedFrom: "human:request",
		Owner: "CODER", Reviewer: "REVIEWER", Objective: "Implement requested work",
		Scope: []string{"src"}, Acceptance: []string{"approved behavior passes"},
		RequiredEvidence: []string{"test-report"}, ValidationCommands: []string{"go test ./..."},
		ExitCondition: "independently verified", Worktree: filepath.Join(dir, "repo"),
		Branch: "work", BaselineSHA: strings.Repeat("a", 40),
	}
	write := func(name string, value any) string {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var out bytes.Buffer
	if err := conversationCmd([]string{"intake", "--file", write("intake.json", controller.IntakeRequest{
		Request: "Please implement the approved behavior; ask if scope is unclear.",
		Task:    task, Actor: "human", Source: "human:request",
	})}, &out); err != nil {
		t.Fatal(err)
	}
	var proposal struct {
		Task core.GraphTask `json:"task"`
		Hash string         `json:"hash"`
	}
	if err := json.Unmarshal(out.Bytes(), &proposal); err != nil || proposal.Task.Status != core.Draft || proposal.Hash == "" {
		t.Fatal("intake did not expose an unapproved contract and exact hash", err)
	}
	resolved, err := config.Resolve([]config.Layer{{Source: "user", Input: []byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: human, worker: local, approvalRef: intake-config}
`)}})
	if err != nil {
		t.Fatal(err)
	}
	confirmation := controller.IntakeConfirmation{TaskID: task.ID, ExpectedHash: proposal.Hash,
		Actor: "human", Source: "human:confirm", Reason: "Confirm this exact scope, acceptance and configuration",
		Selection: resolved.Config, AdditionalTaskIDs: []string{"intake-flow-review-0"}}
	out.Reset()
	if err := conversationCmd([]string{"confirm", "--file", write("confirm.json", confirmation)}, &out); err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	ready, _ := state.Graph.GraphTask(task.ID)
	if ready.Status != core.Ready || len(state.Graph.Dispatches) != 0 {
		t.Fatal("confirmation executed work or failed to authorize exact contract")
	}
	out.Reset()
	if err := conversationCmd([]string{"proposal", "--task", task.ID}, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &proposal); err != nil || proposal.Task.Status != core.Ready {
		t.Fatal("proposal retrieval did not reconstruct saved state", err)
	}
}
