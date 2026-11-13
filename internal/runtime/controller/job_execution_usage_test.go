package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func TestJobRoleEventsPersistProviderUsage(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "usage-event-exec", JobID: job.ID, TaskID: task.ID, Role: "CODER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	worker := &jobRoleRuntimeFake{
		role: "CODER", configHash: "runtime-config-hash", selection: selection,
		events: make(chan runtime.Event, 1),
	}
	controller, err := NewJobRoleController(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := controller.Start(context.Background(), "usage-event-exec")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := controller.Events(ctx, "usage-event-exec")
	if err != nil {
		t.Fatal(err)
	}
	amount := 17.0
	payload, err := json.Marshal(core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &amount,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 1, UsageID: "provider-usage-event"},
		ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: string(payload)}
	observed, ok := <-events
	if !ok || observed.Type != runtime.EventUsage {
		t.Fatalf("usage event was not forwarded after persistence: %+v, open=%v", observed, ok)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	records := state.Graph.JobUsageRecords(job.ID)
	if len(records) != 1 || records[0].RoleExecutionID != "usage-event-exec" ||
		*records[0].Usage.TotalNanoAIU != amount {
		t.Fatalf("provider usage was not persisted against the RoleExecution: %+v", records)
	}
}
