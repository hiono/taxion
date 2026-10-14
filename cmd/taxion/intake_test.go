package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/tools"
)

func TestUserGuideSkillApprovalExampleWithoutSDK(t *testing.T) {
	guide, err := os.ReadFile("../../docs/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, example := range regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllSubmatch(guide, -1) {
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(example[1], &header); err != nil {
			t.Fatal(err)
		}
		if header.Kind != tools.ApprovalKind {
			continue
		}
		found++
		var approval tools.Approval
		if err := core.DecodeGraphInput(bytes.NewReader(example[1]), &approval); err != nil {
			t.Fatal("guide tools approval does not match schema", err)
		}
		directory := t.TempDir()
		path := filepath.Join(directory, "SKILL.md")
		if err := os.WriteFile(path, []byte("---\nname: fixture-guide\ndescription: Synthetic guide instructions.\n---\nCheck the document.\n"), 0600); err != nil {
			t.Fatal(err)
		}
		hash, err := tools.Hash(directory)
		if err != nil {
			t.Fatal(err)
		}
		for role, policy := range approval.Roles {
			if len(policy.Skills) != 1 {
				t.Fatal("guide must identify its skill asset")
			}
			policy.Skills[0] = tools.Asset{Path: directory, Hash: hash}
			policy.EvidenceDirectory = filepath.Join(t.TempDir(), "private")
			approval.Roles[role] = policy
		}
		body, err := json.Marshal(approval)
		if err != nil {
			t.Fatal(err)
		}
		decisions := []core.TaskDecision{{ID: "tools", Source: "human:fixture", Text: string(body)}}
		for _, role := range []string{"CODER", "REVIEWER"} {
			policy, err := tools.Resolve(decisions, approval.TaskID, role)
			if err != nil || policy == nil {
				t.Fatal("guide role policy did not resolve", role, err)
			}
			if err := policy.Check(); err != nil {
				t.Fatal("guide role asset did not pass free preflight", role, err)
			}
		}
		if _, err := tools.Resolve(decisions, "unrelated-task", "CODER"); err == nil {
			t.Fatal("guide approval transferred to unrelated Task")
		}
	}
	if found != 1 {
		t.Fatalf("expected one skill approval example, got %d", found)
	}
}

func TestUserGuideColdStartExamplesWithoutSDK(t *testing.T) {
	guide, err := os.ReadFile("../../docs/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	var examples [][][]byte
	for _, example := range regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllSubmatch(guide, -1) {
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(example[1], &header); err != nil {
			t.Fatal("guide JSON is invalid", err)
		}
		if header.Kind != tools.ApprovalKind {
			examples = append(examples, example)
		}
	}
	if len(examples) != 3 {
		t.Fatalf("expected intake, confirmation and flow examples; got %d", len(examples))
	}

	var intake controller.IntakeRequest
	var confirmation controller.IntakeConfirmation
	var flowRequest controller.ReviewFlowRequest
	for index, target := range []any{&intake, &confirmation, &flowRequest} {
		if err := core.DecodeGraphInput(bytes.NewReader(examples[index][1]), target); err != nil {
			t.Fatalf("decode guide example %d: %v", index+1, err)
		}
	}
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := storePath
	storePath = filepath.Join(dir, "ledger.json")
	t.Cleanup(func() { storePath = old })
	root, baseline := createGitWorktree(t, intake.Task.Branch,
		"https://github.com/"+intake.Task.Repo+".git")
	intake.Task.Worktree, intake.Task.BaselineSHA = root, baseline
	if err := initCmd(); err != nil {
		t.Fatal(err)
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
	if err := conversationCmd([]string{"intake", "--file", write("request.json", intake)}, &out); err != nil {
		t.Fatalf("guide intake is not executable: %v", err)
	}
	var proposal struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(out.Bytes(), &proposal); err != nil {
		t.Fatal(err)
	}
	confirmation.ExpectedHash = proposal.Hash
	if err := os.MkdirAll(filepath.Join(dir, ".taxion"), 0700); err != nil {
		t.Fatal(err)
	}
	configuration, err := json.Marshal(confirmation.Selection)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".taxion/config.yaml"), configuration, 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := conversationCmd([]string{"confirm", "--file", write("confirmation.json", confirmation)}, &out); err != nil {
		t.Fatalf("guide confirmation is not executable: %v", err)
	}
	if err := checkConfig([]string{"check", "--task", intake.Task.ID}, &out); err != nil {
		t.Fatal(err)
	}
	flowRequest.ReviewRoot = filepath.Join(dir, "reviews")
	if err := workflowCmd([]string{"start", "--file", write("flow.json", flowRequest)}); err != nil {
		t.Fatalf("guide flow is not executable: %v", err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dispatch := state.Graph.Dispatches[0]
	if _, err := selectDispatchConfiguration(db, dispatch); err != nil {
		t.Fatal(err)
	}
	if _, err := gitproof.ObserveStart(dispatch); err != nil {
		t.Fatalf("guide Coder cannot start: %v", err)
	}
	task, _ := state.Graph.GraphTask(intake.Task.ID)
	for index := 0; index <= *flowRequest.MaxRepairs; index++ {
		reviewID := fmt.Sprintf("%s-review-%d", flowRequest.ID, index)
		approved := false
		for _, decision := range task.Decisions {
			if decision.ID == confirmation.Selection.Execution.ApprovalRef {
				approved = config.CheckApproval(decision.Text, reviewID, decision.ID, confirmation.Selection) == nil
			}
		}
		if !approved {
			t.Fatalf("guide configuration approval does not cover Reviewer Task %s", reviewID)
		}
	}
	if task.OutputMode != core.OutputModeWorktreeTreeSnapshot || dispatch.Instruction.CommitMessagePolicy != "" {
		t.Fatal("guide file-only tool allowlist requires snapshot mode, not managed commit")
	}
	if len(state.Graph.Dispatches) != 1 || dispatch.ExecutionStarted || dispatch.RuntimeSessionID != "" {
		t.Fatal("cold-start verification executed an SDK turn")
	}
}

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
