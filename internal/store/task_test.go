package store

import (
	"context"
	"testing"

	"github.com/hiono/taxion/internal/domain"
)

func TestTaskStoreRollbackAndRestartRestoration(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_task_store"
	execution, _, sourceResult := createJobContractPlanner(t, database, jobID, principal)
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before JobContract revision: %v", err)
	}
	contract := validStoreJobContract()
	_, revision, err := database.AddJobContractRevision(
		ctx, jobID, job.Revision, "cmd_90909090909090909090909090909090",
		execution.ID, sourceResult.ID, sourceResult.ContentSHA256, contract,
	)
	if err != nil {
		t.Fatalf("AddJobContractRevision: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before approval: %v", err)
	}
	if _, _, err := database.AddJobApproval(
		ctx, jobID, job.Revision, "cmd_91919191919191919191919191919191",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve bounded Task decomposition", principal,
	); err != nil {
		t.Fatalf("AddJobApproval: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Task: %v", err)
	}
	request := domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_93939393939393939393939393939393",
		Title: "Implement bounded task", Purpose: "Persist the approved work decomposition",
		AcceptanceCriteria: []string{"The Task is immutable and scoped"},
	}
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_task_receipt BEFORE INSERT ON job_commands WHEN NEW.command_type = 'TASK' BEGIN SELECT RAISE(ABORT, 'injected Task receipt failure'); END`); err != nil {
		t.Fatalf("create rollback trigger: %v", err)
	}
	if _, _, err := database.AddTask(ctx, job.Revision, request); err == nil {
		t.Fatal("injected receipt failure did not abort Task command")
	}
	afterFailure, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Task rollback: %v", err)
	}
	if afterFailure.Revision != job.Revision || len(afterFailure.Tasks) != 0 {
		t.Fatalf("failed Task command partially mutated Job: before=%#v after=%#v", job, afterFailure)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER fail_task_receipt"); err != nil {
		t.Fatalf("drop rollback trigger: %v", err)
	}
	receipt, task, err := database.AddTask(ctx, job.Revision, request)
	if err != nil {
		t.Fatalf("retry Task after rollback: %v", err)
	}
	if receipt.Revision != job.Revision+1 || task.JobApprovalID == "" || task.ContractRevision != revision.Revision {
		t.Fatalf("Task receipt or approval provenance invalid: receipt=%#v task=%#v", receipt, task)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close before restart: %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen Store: %v", err)
	}
	defer reopened.Close()
	restored, err := reopened.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after restart: %v", err)
	}
	if len(restored.Tasks) != 1 || restored.Tasks[0].ID != task.ID ||
		restored.Tasks[0].JobApprovalID != task.JobApprovalID ||
		restored.Tasks[0].CanonicalHash != task.CanonicalHash {
		t.Fatalf("Task provenance did not survive restart: %#v", restored.Tasks)
	}
	replay, replayed, err := reopened.AddTask(ctx, restored.Revision, request)
	if err != nil {
		t.Fatalf("exact Task replay after restart: %v", err)
	}
	if replay != receipt || replayed.ID != task.ID {
		t.Fatalf("restart replay changed Task: receipt=%#v replay=%#v task=%#v", receipt, replay, task)
	}
}
