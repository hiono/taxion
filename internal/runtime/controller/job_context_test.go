package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestBuildJobExecutionContextIsJobScopedAndExcludesRuntimeBindings(t *testing.T) {
	db, request := intakeApprovalFixture(t, false)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		job, ok := state.Graph.JobForTask(request.TaskID)
		if !ok {
			return fmt.Errorf("intake Job is missing")
		}
		now := time.Now().UTC()
		if err := state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "context-execution", JobID: job.ID, TaskID: request.TaskID, Role: "PLANNER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := state.Graph.TransitionRoleExecution("context-execution", core.RoleExecutionRunning,
			"controller", "start context fixture", now.Add(time.Second)); err != nil {
			return err
		}
		if err := state.Graph.BindRoleExecutionSession("context-execution", "private-session-id", "private-config-hash"); err != nil {
			return err
		}
		return state.Graph.AppendJobMessage(core.JobMessage{
			ID: "context-message-1", JobID: job.ID, TaskID: request.TaskID,
			FromRole: "HUMAN", ToRole: "PLANNER", Body: "確認可能な前提から進めてください。",
			Source: "human:request", TargetExecutionID: "context-execution", At: now.Add(2 * time.Second),
		})
	}); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	context, hash, err := BuildJobExecutionContext(state.Graph, "context-execution")
	if err != nil {
		t.Fatal(err)
	}
	if context.Job.Request != "Implement within this contract." || len(context.Tasks) != 1 ||
		len(context.Messages) != 1 || context.Current.Role != "PLANNER" {
		t.Fatalf("incomplete Job context: %+v", context)
	}
	if len(hash) != 64 {
		t.Fatalf("context hash length = %d, want 64", len(hash))
	}
	rendered, err := RenderJobExecutionContext(context)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-session-id", "private-config-hash", "runtimeSessionId", "runtimeConfigHash", "authorizationId", "worktree"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("Role context exposes controller-only field %q", forbidden)
		}
	}
	_, repeatedHash, err := BuildJobExecutionContext(state.Graph, "context-execution")
	if err != nil || repeatedHash != hash {
		t.Fatal("identical Job context must produce a stable hash", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		job, _ := state.Graph.JobForTask(request.TaskID)
		return state.Graph.AppendJobMessage(core.JobMessage{
			ID: "context-message-2", JobID: job.ID, TaskID: request.TaskID,
			FromRole: "HUMAN", ToRole: "PLANNER", Body: "追加情報です。",
			Source: "human:answer", TargetExecutionID: "context-execution", At: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	updated, _ := db.LoadWorkspace()
	_, updatedHash, err := BuildJobExecutionContext(updated.Graph, "context-execution")
	if err != nil {
		t.Fatal(err)
	}
	if updatedHash == hash {
		t.Fatal("new collaboration information must change the context hash")
	}
}
