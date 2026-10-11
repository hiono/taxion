package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestTaskPlanPrintsObservationWithoutIssuingDispatch(t *testing.T) {
	oldPath := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = oldPath })
	task := core.GraphTask{
		ID: "question", Title: "Investigate compatibility", Repo: "owner/repo",
		Type: "INVESTIGATION", Status: core.Blocked, Issue: "Issue #74",
		CreatedFrom: "human:question", DeferredReason: "Scope not yet approved",
		ResumeCondition: "Human approval of investigation", NextAction: "Clarify investigation scope",
	}
	if err := (core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.HasGraph = true
		state.Graph = core.TaskGraph{Version: 1}
		return state.Graph.Track(task, "human", "retain question")
	}); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "plan-output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	oldStdout := os.Stdout
	os.Stdout = output
	err = taskCmd([]string{"plan", "--repo", task.Repo})
	os.Stdout = oldStdout
	if err != nil {
		t.Fatal(err)
	}
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var plan controller.FollowUpPlan
	if err := json.NewDecoder(output).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.Observation == nil || !strings.HasPrefix(plan.Observation.ContentHash, "sha256:") ||
		plan.Observation.StartedAt.IsZero() || plan.Observation.CompletedAt.Before(plan.Observation.StartedAt) ||
		len(plan.Tasks) != 1 || plan.Tasks[0].Action != "REASSESS_DEFERRED" {
		t.Fatalf("CLI observation incomplete: %+v", plan)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(state.Graph.Dispatches) != 0 || len(state.Ledger.Dispatches) != 0 ||
		len(state.Graph.History) != 1 || state.Graph.Tasks[0].Status != core.Blocked ||
		state.Graph.Tasks[0].AuthorizationID != "" {
		t.Fatal("side observation changed execution or authorization")
	}
}
