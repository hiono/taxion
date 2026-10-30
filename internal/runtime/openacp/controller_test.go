package openacp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestPlannedControllerReceivesAsynchronousACPResponse(t *testing.T) {
	t.Setenv("TAXION_FAKE_MODE", "delayed")
	guidance, err := core.NewBillingBudgetGuidance("", "provider", core.BillingSettings{})
	if err != nil {
		t.Fatal(err)
	}
	a := fakeAdapter(t, false, guidance)
	root := a.Worktree()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "work/task")
	git("config", "user.name", "Taxion Test")
	git("config", "user.email", "taxion-test@example.com")
	git("remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(root, "result.txt"), []byte("baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "result.txt")
	git("commit", "-m", "baseline")
	graph := core.TaskGraph{Version: 1}
	task := core.GraphTask{
		ID: "task", Title: "Test planned ACP", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Issue: "Issue #231", Owner: "CODER", Reviewer: "REVIEWER", Status: core.Draft,
		Objective: "Receive a delayed ACP response", Scope: []string{"result.txt"},
		Acceptance: []string{"response received"}, RequiredEvidence: []string{"test-report"},
		ValidationCommands: []string{"test -s result.txt"}, ExitCondition: "response received",
		Branch: "work/task", Worktree: root, BaselineSHA: git("rev-parse", "HEAD"),
		CreatedFrom: "human:1", AuthorizationID: "test-authorization",
		Decisions:  []core.TaskDecision{{ID: "test-authorization", Source: "human:1", Text: "authorize local test"}},
		FileAccess: a.config.FileAccess, BudgetGuidance: guidance,
	}
	if err := graph.Track(task, "human", "register test"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.InitializeWorkspace(store.Workspace{
		HasGraph: true, Graph: graph, Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}},
	}); err != nil {
		t.Fatal(err)
	}
	repairs := 0
	request := controller.ReviewFlowRequest{
		ID: "flow", TaskID: task.ID, Repository: task.Repo, ReviewRoot: filepath.Join(t.TempDir(), "reviews"),
		MaxRepairs: &repairs, TimeoutSeconds: 600,
		StageBudget:   &core.StageBudget{Version: 1, ValidationSeconds: 5, ReviewSeconds: 10},
		Authorization: core.TaskDecision{ID: "flow-authorization", Source: "human:1", Text: "authorize local ACP test"},
	}
	preview, err := controller.PreviewStagePlan(graph, request)
	if err != nil {
		t.Fatal(err)
	}
	request.StagePlanHash = preview.Plan.Hash
	flow, err := controller.StartReviewFlow(db, filepath.Join(t.TempDir(), "ledger.json"), request)
	if err != nil {
		t.Fatal(err)
	}
	control, err := controller.New(db, a)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	id := flow.DispatchIDs[0]
	if _, err := control.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(ctx, id, "execute"); err != nil {
		t.Fatal(err)
	}
	events, err := control.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	output := false
	for event := range events {
		switch event.Type {
		case runtime.EventOutput:
			output = event.Payload == `{"ok":true}`
		case runtime.EventFailed, runtime.EventCancelled:
			t.Fatal("planned Controller cancelled the ACP response", event)
		case runtime.EventTurnFinished:
			if !output {
				t.Fatal("ACP response was lost")
			}
			if err := control.Stop(ctx, id); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("ACP turn ended without its response")
}
