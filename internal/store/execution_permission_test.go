package store

import (
	"context"
	"errors"
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
		t.Fatalf("GetJob before Task: %v", err)
	}
	_, task, err := database.AddTask(ctx, job.Revision, domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_92929292929292929292929292929292",
		Title: "Bounded execution task", Purpose: "Provide a task-scoped permission boundary",
		AcceptanceCriteria: []string{"The task-scoped operation is valid"},
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before ExecutionPermission: %v", err)
	}
	now := time.Now().UTC().Round(0)
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_93939393939393939393939393939393",
		Role: "CODER", Effect: "repository.write", Resource: contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
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


func TestTaskExecutionStartAtomicRollbackReplaySingleUseAndRestart(t *testing.T) {
	for _, role := range []string{domain.RoleCoder, domain.RoleReviewer} {
		t.Run(role, func(t *testing.T) {
			database, path := newTestStore(t)
			ctx := context.Background()
			principal := newAuthenticatedTestPrincipal(t)
			jobID := "job_task_execution_" + strings.ToLower(role)
			execution, _, sourceResult := createJobContractPlanner(t, database, jobID, principal)
			job, err := database.GetJob(ctx, jobID)
			if err != nil { t.Fatalf("GetJob before contract: %v", err) }
			contract := validStoreJobContract()
			_, revision, err := database.AddJobContractRevision(
				ctx, jobID, job.Revision, "cmd_a1010101010101010101010101010101",
				execution.ID, sourceResult.ID, sourceResult.ContentSHA256, contract,
			)
			if err != nil { t.Fatalf("AddJobContractRevision: %v", err) }
			job, err = database.GetJob(ctx, jobID)
			if err != nil { t.Fatalf("GetJob before approval: %v", err) }
			if _, _, err := database.AddJobApproval(
				ctx, jobID, job.Revision, "cmd_a2020202020202020202020202020202",
				revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
				"Approve the bounded Task execution", principal,
			); err != nil { t.Fatalf("AddJobApproval: %v", err) }
			job, err = database.GetJob(ctx, jobID)
			if err != nil { t.Fatalf("GetJob before Task: %v", err) }
			_, task, err := database.AddTask(ctx, job.Revision, domain.TaskRequest{
				JobID: jobID, CommandID: "cmd_a3030303030303030303030303030303",
				Title: "Execute the approved task", Purpose: "Start one bounded provider operation",
				AcceptanceCriteria: []string{"The exact Task operation is started"},
			})
			if err != nil { t.Fatalf("AddTask: %v", err) }
			job, err = database.GetJob(ctx, jobID)
			if err != nil { t.Fatalf("GetJob before permission: %v", err) }
			now := time.Now().UTC().Round(0)
			request := domain.ExecutionPermissionRequest{
				JobID: jobID, TaskID: task.ID, CommandID: "cmd_a4040404040404040404040404040404",
				Role: role, Effect: "repository.write", Resource: contract.TargetRepositories[0],
				ProviderID: "provider.test", AccountID: "account.primary",
				ConfigurationSHA256: contract.ConfigurationSHA256,
				ReservedTurns: 2, ReservedTokens: 500, ExpiresAt: now.Add(30 * time.Minute),
			}
			requestSHA, err := domain.ExecutionPermissionRequestSHA256(request)
			if err != nil { t.Fatalf("ExecutionPermissionRequestSHA256: %v", err) }
			assessment := domain.ExecutionEffectAssessment{
				Role: role, Effect: request.Effect, Resource: request.Resource,
				ProviderID: request.ProviderID, AccountID: request.AccountID,
				ConfigurationSHA256: request.ConfigurationSHA256, RequestSHA256: requestSHA,
				Reversible: true, Mutating: role == domain.RoleCoder,
				ValidUntil: request.ExpiresAt.Add(time.Minute), EvidenceSHA256: strings.Repeat("e", 64),
			}
			_, permission, err := database.AddExecutionPermission(ctx, job.Revision, request, requestSHA, assessment)
			if err != nil { t.Fatalf("AddExecutionPermission: %v", err) }
			job, err = database.GetJob(ctx, jobID)
			if err != nil { t.Fatalf("GetJob before Task execution start: %v", err) }
			startRevision := job.Revision
			startCommandID := "cmd_a5050505050505050505050505050505"

			if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_task_execution_receipt BEFORE INSERT ON job_commands WHEN NEW.command_type = 'TASK_EXECUTION_START' BEGIN SELECT RAISE(ABORT, 'injected Task execution receipt failure'); END`); err != nil {
				t.Fatalf("create rollback trigger: %v", err)
			}
			if _, _, err := database.StartTaskExecution(
				ctx, startRevision, jobID, task.ID, permission.ID, startCommandID, requestSHA,
			); err == nil { t.Fatal("injected receipt failure did not abort Task execution start") }
			afterFailure, err := database.GetJob(ctx, jobID)
			if err != nil { t.Fatalf("GetJob after rollback: %v", err) }
			if afterFailure.Revision != startRevision ||
				len(afterFailure.RoleExecutions) != len(job.RoleExecutions) ||
				len(afterFailure.ProviderStartIntents) != len(job.ProviderStartIntents) {
				t.Fatalf("failed Task execution start partially mutated Job: before revision=%d after=%#v", startRevision, afterFailure)
			}
			if _, err := database.db.ExecContext(ctx, "DROP TRIGGER fail_task_execution_receipt"); err != nil {
				t.Fatalf("drop rollback trigger: %v", err)
			}

			receipt, started, err := database.StartTaskExecution(
				ctx, startRevision, jobID, task.ID, permission.ID, startCommandID, requestSHA,
			)
			if err != nil { t.Fatalf("StartTaskExecution: %v", err) }
			if receipt.Revision != startRevision+1 ||
				started.RoleExecution.Role != role ||
				started.RoleExecution.TaskID != task.ID ||
				started.RoleExecution.ExecutionPermissionID != permission.ID ||
				started.ProviderStartIntent.ExecutionPermissionID != permission.ID ||
				started.ProviderStartIntent.ID != permission.OperationID ||
				started.ProviderStartIntent.Budget != (domain.PlanningBudget{MaxTurns: request.ReservedTurns, MaxTokens: request.ReservedTokens}) {
				t.Fatalf("Task execution facts are not atomically bound to permission: receipt=%#v started=%#v", receipt, started)
			}

			if err := database.Close(); err != nil { t.Fatalf("Close before restart: %v", err) }
			reopened, err := Open(ctx, path)
			if err != nil { t.Fatalf("reopen Store: %v", err) }
			defer reopened.Close()
			restored, err := reopened.GetJob(ctx, jobID)
			if err != nil { t.Fatalf("GetJob after restart: %v", err) }
			if len(restored.RoleExecutions) != len(job.RoleExecutions)+1 ||
				len(restored.ProviderStartIntents) != len(job.ProviderStartIntents)+1 {
				t.Fatalf("Task execution pair was not restored: %#v", restored)
			}
			replay, replayed, err := reopened.StartTaskExecution(
				ctx, startRevision, jobID, task.ID, permission.ID, startCommandID, requestSHA,
			)
			if err != nil { t.Fatalf("exact StartTaskExecution replay after restart: %v", err) }
			if replay != receipt || replayed.RoleExecution.ID != started.RoleExecution.ID ||
				replayed.ProviderStartIntent.ID != started.ProviderStartIntent.ID {
				t.Fatalf("replay changed Task execution facts: receipt=%#v replay=%#v", receipt, replayed)
			}
			if _, _, err := reopened.StartTaskExecution(
				ctx, restored.Revision, jobID, task.ID, permission.ID, startCommandID, strings.Repeat("0", 64),
			); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatalf("changed Start payload error = %v, want idempotency conflict", err)
			}
			if _, _, err := reopened.StartTaskExecution(
				ctx, restored.Revision, jobID, task.ID, permission.ID,
				"cmd_a6060606060606060606060606060606", requestSHA,
			); err == nil { t.Fatal("a second Start command consumed the same single-use ExecutionPermission") }
			afterReject, err := reopened.GetJob(ctx, jobID)
			if err != nil { t.Fatalf("GetJob after single-use rejection: %v", err) }
			if afterReject.Revision != restored.Revision ||
				len(afterReject.RoleExecutions) != len(restored.RoleExecutions) ||
				len(afterReject.ProviderStartIntents) != len(restored.ProviderStartIntents) {
				t.Fatalf("rejected second consumption mutated Job: %#v", afterReject)
			}
		})
	}
}
