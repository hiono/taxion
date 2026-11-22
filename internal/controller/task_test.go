package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

func TestTaskControllerCreatesImmutableTaskWithoutStartingExecution(t *testing.T) {
	database, jobID, revision := approvedExecutionControllerFixture(t)
	controller, err := NewTaskController(database)
	if err != nil {
		t.Fatalf("NewTaskController: %v", err)
	}
	before, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Task: %v", err)
	}
	request := domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1",
		Title: "Implement approved repository change",
		Purpose: "Create the bounded implementation output",
		AcceptanceCriteria: []string{"The requested change is implemented"},
	}
	receipt, task, err := controller.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("TaskController.Create: %v", err)
	}
	if receipt.Revision != before.Revision+1 || task.JobApprovalID == "" ||
		task.ContractRevision != revision.Revision || task.ID != domain.TaskID(request.CommandID) {
		t.Fatalf("Task provenance or receipt is invalid: receipt=%#v task=%#v", receipt, task)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Task: %v", err)
	}
	if len(job.Tasks) != 1 || len(job.RoleExecutions) != len(before.RoleExecutions) ||
		len(job.ProviderStartIntents) != len(before.ProviderStartIntents) ||
		len(job.ExecutionPermissions) != 0 {
		t.Fatalf("Task creation started execution or changed unrelated facts: %#v", job)
	}
	replay, replayed, err := controller.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("exact Task replay: %v", err)
	}
	if replay != receipt || replayed.ID != task.ID {
		t.Fatalf("exact replay changed Task: receipt=%#v replay=%#v task=%#v", receipt, replay, task)
	}
	changed := request
	changed.Title = "Different Task under the same command"
	if _, _, err := controller.Create(context.Background(), changed); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed Task command error = %v, want idempotency conflict", err)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after idempotency conflict: %v", err)
	}
	if len(job.Tasks) != 1 {
		t.Fatalf("conflicting Task command mutated aggregate: %#v", job.Tasks)
	}
}

func TestTaskControllerRejectsDRAFTJobWithoutMutation(t *testing.T) {
	_, database, _, jobID, _, _, _ := startContractPlanner(t)
	controller, err := NewTaskController(database)
	if err != nil {
		t.Fatalf("NewTaskController: %v", err)
	}
	before, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before rejected Task: %v", err)
	}
	request := domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2",
		Title: "Premature implementation",
		Purpose: "Must remain blocked until JobApproval",
		AcceptanceCriteria: []string{"No Task is created"},
	}
	if _, _, err := controller.Create(context.Background(), request); err == nil {
		t.Fatal("Task was created for DRAFT Job")
	}
	after, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after rejected Task: %v", err)
	}
	if after.Revision != before.Revision || len(after.Tasks) != 0 {
		t.Fatalf("rejected Task request mutated Job: before=%#v after=%#v", before, after)
	}
}
