package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func TestExecutionPermissionStoreRollbackReplayAndRestart(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_execution_permission_store"
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
		"Approve bounded execution", principal,
	); err != nil {
		t.Fatalf("AddJobApproval: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before ExecutionPermission: %v", err)
	}
	now := time.Now().UTC().Round(0)
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, CommandID: "cmd_92929292929292929292929292929292",
		Role: "CODER", Effect: "repository.write", Resource: contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: contract.ConfigurationSHA256,
		ReservedTurns: 1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	requestSHA, err := domain.ExecutionPermissionRequestSHA256(request)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	assessment := domain.ExecutionEffectAssessment{
		Role: request.Role, Effect: request.Effect, Resource: request.Resource,
		ProviderID: request.ProviderID, AccountID: request.AccountID,
		ConfigurationSHA256: request.ConfigurationSHA256, RequestSHA256: requestSHA,
		Reversible: true, Mutating: true, ValidUntil: request.ExpiresAt.Add(5 * time.Minute),
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_execution_permission_receipt BEFORE INSERT ON job_commands WHEN NEW.command_type = 'EXECUTION_PERMISSION' BEGIN SELECT RAISE(ABORT, 'injected permission receipt failure'); END`); err != nil {
		t.Fatalf("create rollback trigger: %v", err)
	}
	if _, _, err := database.AddExecutionPermission(ctx, job.Revision, request, requestSHA, assessment); err == nil {
		t.Fatal("injected receipt failure did not abort ExecutionPermission command")
	}
	afterFailure, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after rollback: %v", err)
	}
	if afterFailure.Revision != job.Revision || len(afterFailure.ExecutionPermissions) != 0 {
		t.Fatalf("failed command partially mutated Job: before=%#v after=%#v", job, afterFailure)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER fail_execution_permission_receipt"); err != nil {
		t.Fatalf("drop rollback trigger: %v", err)
	}
	receipt, permission, err := database.AddExecutionPermission(ctx, job.Revision, request, requestSHA, assessment)
	if err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if receipt.Revision != job.Revision+1 || permission.RequestSHA256 != requestSHA {
		t.Fatalf("permission receipt or digest mismatch: receipt=%#v permission=%#v", receipt, permission)
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
	if len(restored.ExecutionPermissions) != 1 {
		t.Fatalf("restored ExecutionPermission count = %d, want 1", len(restored.ExecutionPermissions))
	}
	restoredPermission := restored.ExecutionPermissions[0]
	if restoredPermission.ID != permission.ID || restoredPermission.JobApprovalID != permission.JobApprovalID ||
		restoredPermission.AssessmentSHA256 != permission.AssessmentSHA256 ||
		restoredPermission.Assessment.EvidenceSHA256 != assessment.EvidenceSHA256 {
		t.Fatalf("ExecutionPermission provenance changed after restart: %#v", restoredPermission)
	}
	if err := restoredPermission.ValidateFor(restored, permission.OperationID, requestSHA, time.Now().UTC()); err != nil {
		t.Fatalf("restored ExecutionPermission validation: %v", err)
	}
	replay, replayed, err := reopened.AddExecutionPermission(ctx, restored.Revision, request, requestSHA, assessment)
	if err != nil {
		t.Fatalf("exact replay after restart: %v", err)
	}
	if replay != receipt || replayed.ID != permission.ID {
		t.Fatalf("restart replay changed receipt or permission: receipt=%#v replay=%#v permission=%#v", receipt, replay, replayed)
	}
}
