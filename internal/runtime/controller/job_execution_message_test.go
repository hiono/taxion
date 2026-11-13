package controller

import (
	"context"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func TestJobRolePostMessageRequiresBoundSourceAndReceivableTarget(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "message-source", JobID: job.ID, TaskID: task.ID, Role: "CODER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "message-target", JobID: job.ID, TaskID: task.ID, Role: "REVIEWER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	worker := &jobRoleRuntimeFake{
		role: "CODER", configHash: "message-config", selection: selection,
		events: make(chan runtime.Event),
	}
	controller, err := NewJobRoleController(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Start(context.Background(), "message-source"); err != nil {
		t.Fatal(err)
	}
	wrongRuntime, err := NewJobRoleController(db, &jobRoleRuntimeFake{
		role: "CODER", configHash: "different-config", selection: selection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wrongRuntime.PostMessage("message-source", "message-target", "wrong-runtime-message",
		"must not be attributed to another configuration", "", time.Now().UTC()); err == nil {
		t.Fatal("Runtime with a different configuration must not post for this RoleExecution")
	}
	if err := controller.PostMessage("message-source", "message-target", "valid-message",
		"Please review the change.", "", time.Now().UTC()); err != nil {
		t.Fatalf("bound source should address a created target: %v", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := state.Graph.TransitionRoleExecution("message-target", core.RoleExecutionRunning,
			"controller", "review started", time.Now().UTC()); err != nil {
			return err
		}
		return state.Graph.TransitionRoleExecution("message-target", core.RoleExecutionSucceeded,
			"controller", "review finished", time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	if err := controller.PostMessage("message-source", "message-target", "late-message",
		"this target has already finished", "", time.Now().UTC()); err == nil {
		t.Fatal("terminal RoleExecution must not receive new Job work")
	}
}
