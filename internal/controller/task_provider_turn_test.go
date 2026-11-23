package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
)

func TestProviderControllerSendTaskExecutionPersistsResultAndCompleteUsage(t *testing.T) {
	_, database, principal, jobID, _, sourceResult, _ := startContractPlanner(t)
	ctx := context.Background()
	jobContracts, err := NewJobContractController(database)
	if err != nil {
		t.Fatalf("NewJobContractController: %v", err)
	}
	plannerExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	revision, err := func() (domain.JobContractRevision, error) {
		_, value, proposeErr := jobContracts.Propose(
			ctx, jobID, plannerExecutionID, sourceResult.ID,
			"cmd_90909090909090909090909090909090", principal,
		)
		return value, proposeErr
	}()
	if err != nil {
		t.Fatalf("Propose JobContract: %v", err)
	}
	if _, _, err := jobContracts.Approve(
		ctx, jobID, "cmd_91919191919191919191919191919191",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve bounded Task Coder execution", principal,
	); err != nil {
		t.Fatalf("Approve JobContract: %v", err)
	}
	taskController, err := NewTaskController(database)
	if err != nil {
		t.Fatalf("NewTaskController: %v", err)
	}
	_, task, err := taskController.Create(ctx, domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_81818181818181818181818181818181",
		Title: "Coder turn", Purpose: "Exercise Task-bound Provider Send",
		AcceptanceCriteria: []string{"Persist final result and complete usage"},
	})
	if err != nil {
		t.Fatalf("Create Task: %v", err)
	}
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	worktree := &fakeTaskWorktreeVerifier{}
	executionController, err := NewExecutionControllerWithWorktree(database, assessor, worktree)
	if err != nil {
		t.Fatalf("NewExecutionControllerWithWorktree: %v", err)
	}
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_92929292929292929292929292929292",
		Role: "CODER", Effect: "repository.write", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(ctx, permissionRequest)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	_, execution, _, err := executionController.StartTaskExecution(
		ctx, permission.ID, "cmd_a7070707070707070707070707070707", permissionRequest,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	runtime := &fakePlanningProviderRuntime{identity: provider.Identity{
		ProviderID: permission.ProviderID, AccountID: permission.AccountID,
		SDKVersion: "test-sdk/1", ConfigurationSHA256: permission.ConfigurationSHA256,
	}}
	runtime.capabilities = provider.Capabilities{
		Identity:    runtime.identity,
		StartLookup: true, StartIdempotency: true, TaskWorktree: true,
		SendLookup: true, SendIdempotency: true, FinalOutputRetrieval: true,
		UsageIdentity: true, UsageCompleteness: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilityStartLookup, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityStartIdempotency, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityTaskWorktree, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilitySendLookup, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilitySendIdempotency, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("e", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityFinalOutput, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("f", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("1", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("2", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		},
	}
	runtime.lookupResult = provider.LookupResult{Found: false, OperationID: permission.OperationID, Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("3", 64)}
	providerController, err := NewProviderControllerWithExecutionAndWorktree(database, runtime, executionController, worktree)
	if err != nil {
		t.Fatalf("NewProviderControllerWithExecutionAndWorktree: %v", err)
	}
	if _, _, err := providerController.DispatchTaskExecution(
		ctx, jobID, execution.ID, permission.ID, permissionRequest,
	); err != nil {
		t.Fatalf("DispatchTaskExecution: %v", err)
	}
	_, turn, observation, result, err := providerController.SendTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_d3030303030303030303030303030303",
		"Implement the approved Task changes.", permissionRequest, 100, principal,
	)
	if err != nil {
		t.Fatalf("SendTaskExecution: %v", err)
	}
	if turn.TurnNumber != 1 || observation.Status != domain.TaskTurnCompleted ||
		observation.TokensUsed != 12 || result.Status != domain.TaskTurnCompleted {
		t.Fatalf("Task Turn did not complete with persisted Usage: turn=%#v observation=%#v result=%#v",
			turn, observation, result)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.ProviderResultObservations) != 1 || len(job.ProviderUsageObservations) != 1 ||
		!job.ProviderUsageObservations[0].Complete || len(job.TaskTurnObservations) != 1 ||
		runtime.sendCalls != 1 || runtime.resultCalls != 1 || runtime.usageCalls != 1 {
		t.Fatalf("Task Turn facts or Provider calls are incomplete: results=%d usage=%d observations=%d calls=%d/%d/%d",
			len(job.ProviderResultObservations), len(job.ProviderUsageObservations), len(job.TaskTurnObservations),
			runtime.sendCalls, runtime.resultCalls, runtime.usageCalls)
	}
}
