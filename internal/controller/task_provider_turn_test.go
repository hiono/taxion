package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		UsageIdentity: true, UsageCompleteness: true, StopProof: true,
		CrossProcessSessionReattach: true, ResumeIdempotency: true,
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
			{Capability: provider.CapabilityResumeIdempotency, Identity: runtime.identity, TestID: "task-turn-test", EvidenceSHA256: strings.Repeat("6", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
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
	completeCapabilities := runtime.capabilities
	runtime.capabilities.SendLookup = false
	runtime.capabilities.SendIdempotency = false
	if _, _, _, _, err := providerController.SendTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_c1010101010101010101010101010101",
		"Capability preflight must precede intent persistence.", permissionRequest, 100, principal,
	); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("Send without E4 Lookup or Idempotency was not rejected: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.TaskTurnIntents) != 0 {
		t.Fatalf("missing Send capability persisted a Turn intent: intents=%d err=%v", len(job.TaskTurnIntents), err)
	}
	runtime.capabilities = completeCapabilities
	runtime.capabilities.FinalOutputRetrieval = false
	if _, _, _, _, err := providerController.SendTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_c2020202020202020202020202020202",
		"Final Result capability preflight must precede intent persistence.", permissionRequest, 100, principal,
	); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("Send without FinalOutput capability was not rejected: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.TaskTurnIntents) != 0 {
		t.Fatalf("missing FinalOutput capability persisted a Turn intent: intents=%d err=%v", len(job.TaskTurnIntents), err)
	}
	runtime.capabilities = completeCapabilities
	worktree.failAt = worktree.verifyCalls + 1
	worktree.failErr = errors.New("temporary pre-dispatch Worktree verification failure")
	if _, _, _, _, err := providerController.SendTaskExecution(
		ctx, jobID, execution.ID, permission.ID, "cmd_d3030303030303030303030303030303",
		"Implement the approved Task changes.", permissionRequest, 100, principal,
	); err == nil {
		t.Fatal("initial pre-dispatch Worktree failure was not returned")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.TaskTurnIntents) != 1 || len(job.TaskTurnDispatchClaims) != 0 ||
		len(job.TaskTurnObservations) != 0 || runtime.sendCalls != 0 {
		t.Fatalf("pre-dispatch failure left unexpected dispatch facts: intents=%d claims=%d observations=%d sends=%d err=%v",
			len(job.TaskTurnIntents), len(job.TaskTurnDispatchClaims), len(job.TaskTurnObservations), runtime.sendCalls, err)
	}
	worktree.failAt = 0
	runtime.sendHook = func(_ context.Context, request provider.TurnRequest) error {
		if !request.RequireIdempotency {
			return errors.New("Task Turn with verified E4 Send idempotency did not require Provider-side deduplication")
		}
		return nil
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
	job, err = database.GetJob(ctx, jobID)
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
		len(job.TaskTurnDispatchClaims) != 1 || len(job.TaskTurnDispatchReleases) != 1 ||
		runtime.sendCalls != 1 || runtime.resultCalls != 1 || runtime.usageCalls != 1 {
		t.Fatalf("Task Turn facts or Provider calls are incomplete: taskResult=%v results=%d usage=%d observations=%d claims=%d releases=%d calls=%d/%d/%d",
			taskResultFound, len(job.ProviderResultObservations), len(job.ProviderUsageObservations), len(job.TaskTurnObservations),
			len(job.TaskTurnDispatchClaims), len(job.TaskTurnDispatchReleases), runtime.sendCalls, runtime.resultCalls, runtime.usageCalls)
	}
	if _, active := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID); active {
		t.Fatal("completed Task Turn retained an active dispatch claim")
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
	runtime.resumeHook = func(_ context.Context, request provider.ResumeRequest) error {
		if !request.RequireIdempotency {
			t.Error("Task Resume did not require Provider-side idempotency")
		}
		return nil
	}
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
		resumedIntent.TaskID != task.ID || resumedIntent.ExecutionPermissionID != permission.ID ||
		resumedIntent.ProviderSessionID != turn.ProviderSessionID || attachedResume.Status != domain.PlanningResumeAttached ||
		!resumeResult.Attached || resumeResult.OperationID != resumedIntent.OperationID ||
		resumeResult.SessionID != turn.ProviderSessionID || runtime.resumeCalls != 2 {
		t.Fatalf("same-Session Task Resume did not reconcile to ATTACHED: intent=%#v observation=%#v result=%#v calls=%d err=%v",
			resumedIntent, attachedResume, resumeResult, runtime.resumeCalls, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Resume: %v", err)
	}
	if _, err := domain.NewTaskTurnIntent(
		job, execution.ID, permission.ID, turn.ProviderSessionID,
		"cmd_f5050505050505050505050505050505", strings.Repeat("4", 64), 100, job.UpdatedAt.Add(time.Second),
	); err != nil {
		t.Fatalf("Task Turn was not allowed after ATTACHED Resume: %v", err)
	}
	if len(job.ProviderUsageObservations) != 1 {
		t.Fatalf("unexpected initial usage count: %d", len(job.ProviderUsageObservations))
	}
	_, replayedUsage, err := providerController.RetrieveTaskProviderUsage(
		ctx, jobID, execution.ID, permission.ID, permissionRequest,
		turn.ProviderSessionID, turn.ID, job.ProviderUsageObservations[0].UsageID, principal,
	)
	if err != nil || replayedUsage.ID != job.ProviderUsageObservations[0].ID || runtime.usageCalls != 1 {
		t.Fatalf("completed Turn Usage replay called Provider or changed observation: usage=%#v calls=%d err=%v",
			replayedUsage, runtime.usageCalls, err)
	}
	if _, _, err := providerController.RetrieveTaskProviderUsage(
		ctx, jobID, execution.ID, permission.ID, permissionRequest,
		turn.ProviderSessionID, turn.ID, "usage_after_terminal", principal,
	); err == nil || runtime.usageCalls != 1 {
		t.Fatalf("new Usage was accepted after terminal Turn: calls=%d err=%v", runtime.usageCalls, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Task Turn dispatch recovery: %v", err)
	}
	nextCommandID := "cmd_f5050505050505050505050505050505"
	nextPrompt := "Implement the approved Task changes again."
	nextPromptDigest := sha256.Sum256([]byte(nextPrompt))
	nextPromptSHA256 := hex.EncodeToString(nextPromptDigest[:])
	_, nextTurn, created, err := database.AddTaskTurnIntentWithStatus(
		ctx, jobID, job.Revision, execution.ID, permission.ID, turn.ProviderSessionID,
		nextCommandID, nextPromptSHA256, 50,
	)
	if err != nil || !created {
		t.Fatalf("persist second Task Turn intent: created=%v err=%v", created, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before simulated dispatch crash: %v", err)
	}
	oldClaimCommandID := "cmd_17171717171717171717171717171717"
	_, oldClaim, claimCreated, err := database.AddTaskTurnDispatchClaim(
		ctx, jobID, job.Revision, nextTurn.ID, oldClaimCommandID, principal,
	)
	if err != nil || !claimCreated {
		t.Fatalf("persist pre-dispatch Task Turn claim: created=%v err=%v", claimCreated, err)
	}
	runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: nextTurn.OperationID, SessionID: turn.ProviderSessionID,
		TurnID: nextTurn.ID, Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("9", 64),
	}
	runtime.usageResult = provider.Usage{
		UsageID: "usage_2", Tokens: 8, Complete: true, EvidenceSHA256: strings.Repeat("a", 64),
	}
	_, recoveredTurn, recoveredObservation, _, err := providerController.SendTaskExecution(
		ctx, jobID, execution.ID, permission.ID, nextCommandID,
		nextPrompt, permissionRequest, 50, principal,
	)
	if err != nil || recoveredTurn.ID != nextTurn.ID || recoveredObservation.Status != domain.TaskTurnCompleted {
		t.Fatalf("SendIdempotency did not recover an active dispatch claim: turn=%#v observation=%#v err=%v",
			recoveredTurn, recoveredObservation, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Task Turn dispatch recovery: %v", err)
	}
	preDispatchReleaseFound := false
	for _, release := range job.TaskTurnDispatchReleases {
		if release.ClaimID == oldClaim.ID &&
			release.Outcome == domain.TaskTurnDispatchReleasedDefinitiveFailure &&
			validProviderEvidenceSHA256(release.EvidenceSHA256) {
			preDispatchReleaseFound = true
		}
	}
	if !preDispatchReleaseFound || len(job.TaskTurnDispatchClaims) != 3 ||
		len(job.TaskTurnDispatchReleases) != 3 || len(job.TaskTurnObservations) != 2 ||
		runtime.sendCalls != 2 || runtime.resultCalls != 2 || runtime.usageCalls != 2 {
		t.Fatalf("Task Turn pre-dispatch recovery facts are incomplete: released=%v claims=%d releases=%d observations=%d calls=%d/%d/%d",
			preDispatchReleaseFound, len(job.TaskTurnDispatchClaims), len(job.TaskTurnDispatchReleases),
			len(job.TaskTurnObservations), runtime.sendCalls, runtime.resultCalls, runtime.usageCalls)
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
	runtime.capabilities.StopLookup = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence, provider.CapabilityEvidence{
		Capability: provider.CapabilityStopLookup, Identity: runtime.identity,
		TestID: "task-stop-lookup-test", EvidenceSHA256: strings.Repeat("f", 64),
		EvidenceLevel: provider.EvidenceLive, VerifiedAt: now,
	})
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
		"cmd_18181818181818181818181818181818", strings.Repeat("4", 64), 100,
	); err == nil {
		t.Fatal("Task Turn was accepted after Provider Stop intent")
	}
}

func TestTaskProviderTurnRejectsLookupResultDigestMismatch(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	now := time.Now().UTC()
	f.runtime.capabilities.SendLookup = true
	f.runtime.capabilities.SendIdempotency = true
	f.runtime.capabilities.FinalOutputRetrieval = true
	f.runtime.capabilities.UsageIdentity = true
	f.runtime.capabilities.UsageCompleteness = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "turn-result-digest", EvidenceSHA256: strings.Repeat("1", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity, TestID: "turn-result-digest", EvidenceSHA256: strings.Repeat("2", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: f.runtime.identity, TestID: "turn-result-digest", EvidenceSHA256: strings.Repeat("3", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "turn-result-digest", EvidenceSHA256: strings.Repeat("4", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "turn-result-digest", EvidenceSHA256: strings.Repeat("5", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	prompt := "Verify the Lookup final Result digest."
	commandID := "cmd_81818181818181818181818181818182"
	_, turn, created, err := f.database.AddTaskTurnIntentWithStatus(
		f.ctx, f.jobID, job.Revision, f.execution.ID, f.permission.ID, "session_1",
		commandID, sha256String(prompt), 100,
	)
	if err != nil || !created {
		t.Fatalf("AddTaskTurnIntentWithStatus: created=%v err=%v", created, err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before dispatch claim: %v", err)
	}
	_, dispatchClaim, created, err := f.database.AddTaskTurnDispatchClaim(
		f.ctx, f.jobID, job.Revision, turn.ID,
		"cmd_82828282828282828282828282828282", f.principal,
	)
	if err != nil || !created {
		t.Fatalf("AddTaskTurnDispatchClaim: created=%v err=%v", created, err)
	}
	if _, created, err := f.controller.recordTaskTurnDispatchObservation(
		f.ctx, f.jobID, dispatchClaim.ID, domain.TaskTurnDispatchObservationStarted, "", f.principal,
	); err != nil || !created {
		t.Fatalf("record Task Turn dispatch start: created=%v err=%v", created, err)
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: turn.OperationID, Status: domain.TaskTurnCompleted,
		ReceiptID: "receipt_1", SessionID: "session_1", TurnID: turn.ID,
		ResultSHA256: strings.Repeat("f", 64), UsageID: "usage_mismatch",
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	f.runtime.resultResult = provider.Result{
		OperationID: turn.OperationID, SessionID: turn.ProviderSessionID, TurnID: turn.ID,
		Content: "mismatched final provider result", ResultSHA256: sha256String("mismatched final provider result"),
		Final: true, EvidenceSHA256: strings.Repeat("d", 64),
	}
	_, recoveredTurn, observation, _, err := f.controller.SendTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		prompt, f.permissionRequest, 100, f.principal,
	)
	if !errors.Is(err, provider.ErrProviderInvalidResult) || recoveredTurn.ID != turn.ID ||
		observation.Status != domain.TaskTurnUnknown || f.runtime.resultCalls != 1 || f.runtime.usageCalls != 0 {
		t.Fatalf("Lookup Result digest mismatch was not retained as UNKNOWN: turn=%#v observation=%#v resultCalls=%d usageCalls=%d err=%v",
			recoveredTurn, observation, f.runtime.resultCalls, f.runtime.usageCalls, err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	taskResultPersisted, taskUsagePersisted := false, false
	for _, result := range job.ProviderResultObservations {
		taskResultPersisted = taskResultPersisted || result.TurnID == turn.ID
	}
	for _, usage := range job.ProviderUsageObservations {
		taskUsagePersisted = taskUsagePersisted || usage.TurnID == turn.ID
	}
	if err != nil || taskResultPersisted || taskUsagePersisted {
		t.Fatalf("mismatched final Result or Usage facts were persisted for the Task Turn: taskResult=%v taskUsage=%v err=%v",
			taskResultPersisted, taskUsagePersisted, err)
	}
	if _, active := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID); !active {
		t.Fatal("ambiguous Task Turn released its dispatch claim")
	}

	correctedContent := "corrected final provider result"
	correctedSHA := sha256String(correctedContent)
	f.runtime.lookupResult.ResultSHA256 = correctedSHA
	f.runtime.lookupResult.UsageID = "usage_corrected"
	f.runtime.resultResult = provider.Result{
		OperationID: turn.OperationID, SessionID: turn.ProviderSessionID, TurnID: turn.ID,
		Content: correctedContent, ResultSHA256: correctedSHA,
		Final: true, EvidenceSHA256: strings.Repeat("8", 64),
	}
	f.runtime.capabilities.UsageIdentity = true
	f.runtime.capabilities.UsageCompleteness = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "turn-result-digest", EvidenceSHA256: strings.Repeat("9", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "turn-result-digest", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	f.runtime.usageResult = provider.Usage{
		UsageID: "usage_corrected", Tokens: 12, Complete: true, EvidenceSHA256: strings.Repeat("b", 64),
	}
	_, _, recovered, _, retryErr := f.controller.SendTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		prompt, f.permissionRequest, 100, f.principal,
	)
	if retryErr != nil || recovered.Status != domain.TaskTurnCompleted {
		t.Fatalf("corrected final Result could not recover the same Turn: observation=%#v err=%v", recovered, retryErr)
	}
}

func TestTaskProviderTurnReleasesDefinitiveSendFailureAndRetriesSameIntent(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	now := time.Now().UTC()
	f.runtime.capabilities.SendLookup = true
	f.runtime.capabilities.SendIdempotency = true
	f.runtime.capabilities.FinalOutputRetrieval = true
	f.runtime.capabilities.UsageIdentity = true
	f.runtime.capabilities.UsageCompleteness = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "turn-definitive-failure", EvidenceSHA256: strings.Repeat("1", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity, TestID: "turn-definitive-failure", EvidenceSHA256: strings.Repeat("5", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: f.runtime.identity, TestID: "turn-definitive-failure", EvidenceSHA256: strings.Repeat("2", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "turn-definitive-failure", EvidenceSHA256: strings.Repeat("3", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "turn-definitive-failure", EvidenceSHA256: strings.Repeat("4", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	commandID := "cmd_83838383838383838383838383838383"
	prompt := "Retry after a definitive Provider rejection."
	f.runtime.sendErr = &provider.OperationError{Class: provider.FailureDefinitive, Code: provider.OperationCodeInvalidRequest}
	_, turn, observation, _, err := f.controller.SendTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		prompt, f.permissionRequest, 100, f.principal,
	)
	if err == nil || turn.ID == "" || observation.ID != "" {
		t.Fatalf("definitive Send rejection was not returned without a fabricated observation: turn=%#v observation=%#v err=%v",
			turn, observation, err)
	}
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after definitive Send rejection: %v", err)
	}
	_, activeClaim := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID)
	if len(job.TaskTurnDispatchClaims) != 1 || len(job.TaskTurnDispatchReleases) != 1 ||
		len(job.TaskTurnObservations) != 0 ||
		job.TaskTurnDispatchReleases[0].Outcome != domain.TaskTurnDispatchReleasedDefinitiveFailure ||
		!validProviderEvidenceSHA256(job.TaskTurnDispatchReleases[0].EvidenceSHA256) ||
		activeClaim || !taskTurnNeverDispatched(job, turn.ID) {
		t.Fatalf("definitive Send rejection did not release its exact claim with evidence: claims=%#v releases=%#v observations=%#v active=%v",
			job.TaskTurnDispatchClaims, job.TaskTurnDispatchReleases, job.TaskTurnObservations, activeClaim)
	}

	f.runtime.sendErr = nil
	_, retriedTurn, completed, result, err := f.controller.SendTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		prompt, f.permissionRequest, 100, f.principal,
	)
	if err != nil || retriedTurn.ID != turn.ID || completed.Status != domain.TaskTurnCompleted ||
		result.Status != domain.TaskTurnCompleted || f.runtime.sendCalls != 2 {
		t.Fatalf("same Task Turn intent did not retry after definitive non-dispatch: turn=%#v observation=%#v result=%#v calls=%d err=%v",
			retriedTurn, completed, result, f.runtime.sendCalls, err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil || len(job.TaskTurnDispatchClaims) != 2 || len(job.TaskTurnDispatchReleases) != 2 ||
		len(job.TaskTurnObservations) != 1 {
		t.Fatalf("retry did not preserve claim-scoped dispatch history: claims=%d releases=%d observations=%d err=%v",
			len(job.TaskTurnDispatchClaims), len(job.TaskTurnDispatchReleases), len(job.TaskTurnObservations), err)
	}
}

func TestProviderCapabilityEvidenceSHA256AcceptsEvidenceAtLeastLive(t *testing.T) {
	identity := provider.Identity{
		ProviderID: "provider.test", AccountID: "account.primary",
		SDKVersion: "sdk1", ConfigurationSHA256: strings.Repeat("f", 64),
	}
	digest := strings.Repeat("a", 64)
	for _, test := range []struct {
		name  string
		level provider.EvidenceLevel
		want  string
	}{
		{name: "live", level: provider.EvidenceLive, want: digest},
		{name: "durable", level: provider.EvidenceDurable, want: digest},
		{name: "sdk", level: provider.EvidenceSDK},
	} {
		t.Run(test.name, func(t *testing.T) {
			capabilities := provider.Capabilities{
				Identity: identity, StopDispatchFencing: true,
				Evidence: []provider.CapabilityEvidence{{
					Capability: provider.CapabilityStopDispatchFencing,
					Identity:   identity, TestID: "stop-fencing-evidence",
					EvidenceSHA256: digest, EvidenceLevel: test.level,
					VerifiedAt: time.Now().UTC(),
				}},
			}
			if got := providerCapabilityEvidenceSHA256(capabilities, provider.CapabilityStopDispatchFencing); got != test.want {
				t.Fatalf("evidence digest = %q, want %q for level %s", got, test.want, test.level)
			}
		})
	}
}

func TestReleaseTaskTurnDispatchClaimBeforeSendDoesNotReleaseReplacementClaim(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before creating Turn: %v", err)
	}
	promptDigest := sha256.Sum256([]byte("Claim replacement regression test"))
	_, turn, err := f.database.AddTaskTurnIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, f.permission.ID, "session_1",
		"cmd_75757575757575757575757575757575", hex.EncodeToString(promptDigest[:]), 100,
	)
	if err != nil {
		t.Fatalf("Add Task Turn intent: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before first claim: %v", err)
	}
	_, firstClaim, _, err := f.database.AddTaskTurnDispatchClaim(
		f.ctx, f.jobID, job.Revision, turn.ID,
		"cmd_78787878787878787878787878787878", f.principal,
	)
	if err != nil {
		t.Fatalf("Add first dispatch claim: %v", err)
	}
	if _, created, err := f.controller.recordTaskTurnDispatchObservation(
		f.ctx, f.jobID, firstClaim.ID, domain.TaskTurnDispatchObservationStarted, "", f.principal,
	); err != nil || !created {
		t.Fatalf("persist first dispatch start: created=%v err=%v", created, err)
	}
	lookupEvidence := strings.Repeat("a", 64)
	if _, created, err := f.controller.recordTaskTurnDispatchObservation(
		f.ctx, f.jobID, firstClaim.ID, domain.TaskTurnDispatchObservationLookupNotFound, lookupEvidence, f.principal,
	); err != nil || !created {
		t.Fatalf("persist first lookup NOT_FOUND: created=%v err=%v", created, err)
	}
	if _, _, err := f.database.ReleaseTaskTurnDispatchClaim(
		f.ctx, f.jobID, firstClaim.ID, f.principal,
		domain.TaskTurnDispatchReleasedIdempotentRetry, strings.Repeat("b", 64), lookupEvidence,
	); err != nil {
		t.Fatalf("release first dispatch claim: %v", err)
	}

	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before replacement claim: %v", err)
	}
	_, replacementClaim, _, err := f.database.AddTaskTurnDispatchClaim(
		f.ctx, f.jobID, job.Revision, turn.ID,
		"cmd_79797979797979797979797979797979", f.principal,
	)
	if err != nil {
		t.Fatalf("Add replacement dispatch claim: %v", err)
	}
	if err := f.controller.releaseTaskTurnDispatchClaimBeforeSend(
		f.ctx, f.jobID, turn, firstClaim.ID, f.principal,
	); err != nil {
		t.Fatalf("release stale pre-send claim: %v", err)
	}

	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after stale release: %v", err)
	}
	activeClaim, active := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID)
	if !active || activeClaim.ID != replacementClaim.ID {
		t.Fatalf("stale pre-send cleanup changed replacement claim: active=%v claim=%#v want=%#v",
			active, activeClaim, replacementClaim)
	}
}

func TestDefinitivePreSendReleaseDoesNotResolveEarlierUnknownClaim(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before creating Turn: %v", err)
	}
	prompt := "Keep earlier ambiguous dispatch unresolved."
	_, turn, err := f.database.AddTaskTurnIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, f.permission.ID, "session_1",
		"cmd_87878787878787878787878787878787", sha256String(prompt), 100,
	)
	if err != nil {
		t.Fatalf("Add Task Turn intent: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before first claim: %v", err)
	}
	_, firstClaim, _, err := f.database.AddTaskTurnDispatchClaim(
		f.ctx, f.jobID, job.Revision, turn.ID,
		"cmd_88888888888888888888888888888888", f.principal,
	)
	if err != nil {
		t.Fatalf("Add first dispatch claim: %v", err)
	}
	if _, created, err := f.controller.recordTaskTurnDispatchObservation(
		f.ctx, f.jobID, firstClaim.ID, domain.TaskTurnDispatchObservationStarted, "", f.principal,
	); err != nil || !created {
		t.Fatalf("persist first dispatch start: created=%v err=%v", created, err)
	}
	if _, err := f.controller.recordTaskTurnObservation(
		f.ctx, f.jobID, turn.ID, firstClaim.ID, domain.TaskTurnUnknown, 0, "", false,
	); err != nil {
		t.Fatalf("persist first attempt UNKNOWN: %v", err)
	}
	lookupEvidence := strings.Repeat("a", 64)
	if _, created, err := f.controller.recordTaskTurnDispatchObservation(
		f.ctx, f.jobID, firstClaim.ID, domain.TaskTurnDispatchObservationLookupNotFound, lookupEvidence, f.principal,
	); err != nil || !created {
		t.Fatalf("persist first lookup NOT_FOUND: created=%v err=%v", created, err)
	}
	if _, _, err := f.database.ReleaseTaskTurnDispatchClaim(
		f.ctx, f.jobID, firstClaim.ID, f.principal,
		domain.TaskTurnDispatchReleasedIdempotentRetry, strings.Repeat("b", 64), lookupEvidence,
	); err != nil {
		t.Fatalf("release first claim for idempotent retry: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before replacement claim: %v", err)
	}
	_, replacement, _, err := f.database.AddTaskTurnDispatchClaim(
		f.ctx, f.jobID, job.Revision, turn.ID,
		"cmd_89898989898989898989898989898989", f.principal,
	)
	if err != nil {
		t.Fatalf("Add replacement claim: %v", err)
	}
	if err := f.controller.releaseTaskTurnDispatchClaimBeforeSend(
		f.ctx, f.jobID, turn, replacement.ID, f.principal,
	); err != nil {
		t.Fatalf("release replacement claim before Send: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after replacement release: %v", err)
	}
	if len(job.TaskTurnObservations) != 1 || job.TaskTurnObservations[0].Status != domain.TaskTurnUnknown ||
		len(job.TaskTurnDispatchReleases) != 2 ||
		job.TaskTurnDispatchReleases[0].Outcome != domain.TaskTurnDispatchReleasedIdempotentRetry ||
		job.TaskTurnDispatchReleases[1].ClaimID != replacement.ID ||
		job.TaskTurnDispatchReleases[1].Outcome != domain.TaskTurnDispatchReleasedDefinitiveFailure ||
		taskTurnNeverDispatched(job, turn.ID) {
		t.Fatalf("replacement pre-send release changed the earlier ambiguous dispatch: observations=%#v releases=%#v neverDispatched=%v",
			job.TaskTurnObservations, job.TaskTurnDispatchReleases, taskTurnNeverDispatched(job, turn.ID))
	}
}

func TestReleaseTaskTurnDispatchClaimForIdempotentRetryDoesNotReleaseReplacementClaim(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before creating Turn: %v", err)
	}
	promptDigest := sha256.Sum256([]byte("Idempotent retry claim identity regression"))
	_, turn, err := f.database.AddTaskTurnIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, f.permission.ID, "session_1",
		"cmd_85858585858585858585858585858585", hex.EncodeToString(promptDigest[:]), 100,
	)
	if err != nil {
		t.Fatalf("Add Task Turn intent: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before first claim: %v", err)
	}
	_, firstClaim, _, err := f.database.AddTaskTurnDispatchClaim(
		f.ctx, f.jobID, job.Revision, turn.ID,
		"cmd_86868686868686868686868686868686", f.principal,
	)
	if err != nil {
		t.Fatalf("Add first dispatch claim: %v", err)
	}
	if _, created, err := f.controller.recordTaskTurnDispatchObservation(
		f.ctx, f.jobID, firstClaim.ID, domain.TaskTurnDispatchObservationStarted, "", f.principal,
	); err != nil || !created {
		t.Fatalf("persist first dispatch start: created=%v err=%v", created, err)
	}
	staleLookupJob, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before simulated Lookup: %v", err)
	}
	lookupEvidence := strings.Repeat("a", 64)
	if _, created, err := f.controller.recordTaskTurnDispatchObservation(
		f.ctx, f.jobID, firstClaim.ID, domain.TaskTurnDispatchObservationLookupNotFound, lookupEvidence, f.principal,
	); err != nil || !created {
		t.Fatalf("persist first lookup NOT_FOUND: created=%v err=%v", created, err)
	}
	if _, _, err := f.database.ReleaseTaskTurnDispatchClaim(
		f.ctx, f.jobID, firstClaim.ID, f.principal,
		domain.TaskTurnDispatchReleasedIdempotentRetry, strings.Repeat("b", 64), lookupEvidence,
	); err != nil {
		t.Fatalf("release first dispatch claim: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before replacement claim: %v", err)
	}
	_, replacementClaim, _, err := f.database.AddTaskTurnDispatchClaim(
		f.ctx, f.jobID, job.Revision, turn.ID,
		"cmd_87878787878787878787878787878787", f.principal,
	)
	if err != nil {
		t.Fatalf("Add replacement dispatch claim: %v", err)
	}

	if _, err := f.controller.releaseTaskTurnDispatchClaimForIdempotentRetry(
		f.ctx, staleLookupJob, turn, f.principal, strings.Repeat("b", 64), strings.Repeat("a", 64),
	); !errors.Is(err, provider.ErrStopUnconfirmed) {
		t.Fatalf("stale Lookup released a replacement claim: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after stale Lookup: %v", err)
	}
	activeClaim, active := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID)
	if !active || activeClaim.ID != replacementClaim.ID {
		t.Fatalf("stale Lookup changed replacement claim: active=%v claim=%#v want=%#v",
			active, activeClaim, replacementClaim)
	}
}
