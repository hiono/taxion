package controller

import (
	"context"
	"errors"
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
		ReservedTurns:       2, ReservedTokens: 200, ExpiresAt: now.Add(30 * time.Minute),
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
		UsageIdentity: true, UsageCompleteness: true, StopProof: true, CrossProcessSessionReattach: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilityStartLookup, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityStartIdempotency, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityTaskWorktree, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilitySendLookup, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilitySendIdempotency, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("e", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityFinalOutput, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("f", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("1", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("2", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityStopProof, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("3", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilitySessionReattach, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("5", 64), EvidenceLevel: provider.EvidenceDurable, VerifiedAt: now},
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
	taskResultFound := false
	for _, result := range job.ProviderResultObservations {
		if result.OperationID == turn.OperationID && result.TurnID == turn.ID && result.Final {
			taskResultFound = true
			break
		}
	}
	if !taskResultFound || len(job.ProviderUsageObservations) != 1 ||
		!job.ProviderUsageObservations[0].Complete || len(job.TaskTurnObservations) != 1 ||
		runtime.sendCalls != 1 || runtime.resultCalls != 1 || runtime.usageCalls != 1 {
		t.Fatalf("Task Turn facts or Provider calls are incomplete: taskResult=%v results=%d usage=%d observations=%d calls=%d/%d/%d",
			taskResultFound, len(job.ProviderResultObservations), len(job.ProviderUsageObservations), len(job.TaskTurnObservations),
			runtime.sendCalls, runtime.resultCalls, runtime.usageCalls)
	}
	runtime.capabilities.CrossProcessSessionReattach = false
	if _, _, _, _, err := providerController.ResumeTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_b8080808080808080808080808080808",
		permissionRequest, principal,
	); !errors.Is(err, provider.ErrUnsupportedCapability) || runtime.resumeCalls != 0 {
		t.Fatalf("Resume without E5 reattachment evidence was not rejected: calls=%d err=%v", runtime.resumeCalls, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.PlanningResumeIntents) != 0 {
		t.Fatalf("unsupported Resume capability created an intent: intents=%d err=%v", len(job.PlanningResumeIntents), err)
	}
	runtime.capabilities.CrossProcessSessionReattach = true
	runtime.resumeErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown}
	_, resumeIntent, unknownResume, _, resumeErr := providerController.ResumeTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_b8080808080808080808080808080808",
		permissionRequest, principal,
	)
	if resumeErr == nil || unknownResume.Status != domain.PlanningResumeUnknown || runtime.resumeCalls != 1 {
		t.Fatalf("ambiguous Task Resume was not retained as UNKNOWN: intent=%#v observation=%#v calls=%d err=%v",
			resumeIntent, unknownResume, runtime.resumeCalls, resumeErr)
	}
	if _, _, _, _, err := providerController.ResumeTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_c9090909090909090909090909090909",
		permissionRequest, principal,
	); err == nil || runtime.resumeCalls != 1 {
		t.Fatalf("a new Resume OperationID bypassed UNKNOWN reconciliation: calls=%d err=%v", runtime.resumeCalls, err)
	}
	if _, _, _, err := providerController.StopTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_e4040404040404040404040404040404",
		permissionRequest, domain.ProviderStopReasonTaskComplete, principal,
	); err == nil || runtime.stopCalls != 0 {
		t.Fatalf("Stop raced an unresolved Resume: stopCalls=%d err=%v", runtime.stopCalls, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.ProviderStopIntents) != 0 {
		t.Fatalf("unresolved Resume created a Stop intent: intents=%d err=%v", len(job.ProviderStopIntents), err)
	}
	runtime.resumeErr = nil
	_, resumedIntent, attachedResume, resumeResult, err := providerController.ResumeTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_b8080808080808080808080808080808",
		permissionRequest, principal,
	)
	if err != nil || resumedIntent.ID != resumeIntent.ID || resumedIntent.OperationID != resumeIntent.OperationID ||
		resumedIntent.ProviderSessionID != turn.ProviderSessionID || attachedResume.Status != domain.PlanningResumeAttached ||
		!resumeResult.Attached || resumeResult.SessionID != turn.ProviderSessionID || runtime.resumeCalls != 2 {
		t.Fatalf("same-Session Task Resume did not reconcile to ATTACHED: intent=%#v observation=%#v result=%#v calls=%d err=%v",
			resumedIntent, attachedResume, resumeResult, runtime.resumeCalls, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Resume: %v", err)
	}
	if _, err := domain.NewTaskTurnIntent(
		job, execution.ID, permission.ID, turn.ProviderSessionID,
		"cmd_f5050505050505050505050505050505", strings.Repeat("4", 64), 100, now.Add(time.Second),
	); err != nil {
		t.Fatalf("Task Turn was not allowed after ATTACHED Resume: %v", err)
	}
	runtime.capabilities.StopProof = false
	if _, _, _, err := providerController.StopTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_e4040404040404040404040404040404",
		permissionRequest, domain.ProviderStopReasonTaskComplete, principal,
	); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("Stop without E4 StopProof was not rejected: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.ProviderStopIntents) != 0 {
		t.Fatalf("unsupported Stop capability created a Stop intent: intents=%d err=%v", len(job.ProviderStopIntents), err)
	}
	runtime.capabilities.StopProof = true
	_, stopIntent, stopObservation, err := providerController.StopTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_e4040404040404040404040404040404",
		permissionRequest, domain.ProviderStopReasonTaskComplete, principal,
	)
	if err != nil {
		t.Fatalf("StopTaskExecution: %v", err)
	}
	if stopIntent.RoleExecutionID != execution.ID || stopIntent.SessionID != turn.ProviderSessionID ||
		!stopObservation.Stopped || !stopObservation.Quiescent || runtime.stopCalls != 1 {
		t.Fatalf("Task Provider Stop was not confirmed: intent=%#v observation=%#v stopCalls=%d",
			stopIntent, stopObservation, runtime.stopCalls)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Stop: %v", err)
	}
	if len(job.ProviderStopIntents) != 1 || len(job.ProviderStopObservations) != 1 {
		t.Fatalf("Task Provider Stop facts were not persisted: intents=%d observations=%d",
			len(job.ProviderStopIntents), len(job.ProviderStopObservations))
	}
	_, _, replayedStop, err := providerController.StopTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_e4040404040404040404040404040404",
		permissionRequest, domain.ProviderStopReasonTaskComplete, principal,
	)
	if err != nil || replayedStop.ID != stopObservation.ID || runtime.stopCalls != 1 {
		t.Fatalf("exact Stop replay repeated or changed the Provider side effect: observation=%#v calls=%d err=%v",
			replayedStop, runtime.stopCalls, err)
	}
	if _, _, _, _, err := providerController.ResumeTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_a6060606060606060606060606060606",
		permissionRequest, principal,
	); err == nil || runtime.resumeCalls != 2 {
		t.Fatalf("Resume was accepted after Provider Stop: resumeCalls=%d err=%v", runtime.resumeCalls, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before post-Stop Turn: %v", err)
	}
	if _, _, err := database.AddTaskTurnIntent(
		ctx, jobID, job.Revision, execution.ID, permission.ID, turn.ProviderSessionID,
		"cmd_f5050505050505050505050505050505", strings.Repeat("4", 64), 100,
	); err == nil {
		t.Fatal("Task Turn was accepted after Provider Stop intent")
	}
}
