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
	idempotentReleaseFound := false
	for _, release := range job.TaskTurnDispatchReleases {
		if release.ClaimID == oldClaim.ID &&
			release.Outcome == domain.TaskTurnDispatchReleasedIdempotentRetry &&
			release.EvidenceSHA256 == strings.Repeat("e", 64) {
			idempotentReleaseFound = true
		}
	}
	if !idempotentReleaseFound || len(job.TaskTurnDispatchClaims) != 3 ||
		len(job.TaskTurnDispatchReleases) != 3 || len(job.TaskTurnObservations) != 2 ||
		runtime.sendCalls != 2 || runtime.resultCalls != 2 || runtime.usageCalls != 2 {
		t.Fatalf("Task Turn idempotent recovery facts are incomplete: released=%v claims=%d releases=%d observations=%d calls=%d/%d/%d",
			idempotentReleaseFound, len(job.TaskTurnDispatchClaims), len(job.TaskTurnDispatchReleases),
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
	if _, _, err := f.database.ReleaseTaskTurnDispatchClaim(
		f.ctx, f.jobID, firstClaim.ID, f.principal,
		domain.TaskTurnDispatchReleasedIdempotentRetry, strings.Repeat("a", 64),
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
	staleLookupJob, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before simulated Lookup: %v", err)
	}
	if _, _, err := f.database.ReleaseTaskTurnDispatchClaim(
		f.ctx, f.jobID, firstClaim.ID, f.principal,
		domain.TaskTurnDispatchReleasedIdempotentRetry, strings.Repeat("a", 64),
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
		f.ctx, staleLookupJob, turn, f.principal, strings.Repeat("b", 64),
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
