package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

type taskStopWorktreeVerifier struct {
	verifyCalls int
	failAt      int
	verifyErr   error
}

func (v *taskStopWorktreeVerifier) Prepare(_ context.Context, jobID string, task domain.Task) (domain.WorktreeSnapshot, error) {
	return testWorktreeSnapshot(jobID, task), nil
}

func (v *taskStopWorktreeVerifier) Verify(context.Context, string, domain.Task, domain.WorktreeSnapshot) error {
	v.verifyCalls++
	if v.failAt != 0 && v.verifyCalls == v.failAt {
		return v.verifyErr
	}
	return nil
}

type taskProviderStopFixture struct {
	ctx               context.Context
	database          *store.Store
	principal         auth.AuthenticatedPrincipal
	jobID             string
	task              domain.Task
	execution         domain.RoleExecution
	permission        domain.ExecutionPermission
	permissionRequest domain.ExecutionPermissionRequest
	runtime           *fakePlanningProviderRuntime
	worktree          *taskStopWorktreeVerifier
	controller        *ProviderController
}

func newTaskProviderStopFixture(t *testing.T) taskProviderStopFixture {
	t.Helper()
	ctx := context.Background()
	_, database, principal, jobID, _, sourceResult, _ := startContractPlanner(t)
	jobContracts, err := NewJobContractController(database)
	if err != nil {
		t.Fatalf("NewJobContractController: %v", err)
	}
	plannerExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	_, revision, err := jobContracts.Propose(
		ctx, jobID, plannerExecutionID, sourceResult.ID,
		"cmd_90909090909090909090909090909090", principal,
	)
	if err != nil {
		t.Fatalf("Propose JobContract: %v", err)
	}
	if _, _, err := jobContracts.Approve(
		ctx, jobID, "cmd_91919191919191919191919191919191",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve bounded Task Provider Stop tests", principal,
	); err != nil {
		t.Fatalf("Approve JobContract: %v", err)
	}
	taskController, err := NewTaskController(database)
	if err != nil {
		t.Fatalf("NewTaskController: %v", err)
	}
	_, task, err := taskController.Create(ctx, domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_81818181818181818181818181818181",
		Title: "Task Provider Stop", Purpose: "Exercise Stop failure and replay boundaries",
		AcceptanceCriteria: []string{"Stop never repeats an ambiguous Provider side effect"},
	})
	if err != nil {
		t.Fatalf("Create Task: %v", err)
	}
	now := time.Now().UTC().Round(0)
	worktree := &taskStopWorktreeVerifier{}
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	executionController, err := NewExecutionControllerWithWorktree(database, assessor, worktree)
	if err != nil {
		t.Fatalf("NewExecutionControllerWithWorktree: %v", err)
	}
	executionController.now = func() time.Time { return now }
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
	identity := provider.Identity{
		ProviderID: permission.ProviderID, AccountID: permission.AccountID,
		SDKVersion: "test-sdk/1", ConfigurationSHA256: permission.ConfigurationSHA256,
	}
	runtime := &fakePlanningProviderRuntime{identity: identity}
	runtime.capabilities = provider.Capabilities{
		Identity: identity, StartLookup: true, StartIdempotency: true,
		TaskWorktree: true, StopProof: true, StopLookup: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilityStartLookup, Identity: identity, TestID: "task-stop-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityStartIdempotency, Identity: identity, TestID: "task-stop-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityTaskWorktree, Identity: identity, TestID: "task-stop-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityStopProof, Identity: identity, TestID: "task-stop-test", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityStopLookup, Identity: identity, TestID: "task-stop-test", EvidenceSHA256: strings.Repeat("f", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		},
	}
	runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: permission.OperationID,
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("e", 64),
	}
	controller, err := NewProviderControllerWithExecutionAndWorktree(database, runtime, executionController, worktree)
	if err != nil {
		t.Fatalf("NewProviderControllerWithExecutionAndWorktree: %v", err)
	}
	controller.now = func() time.Time { return now }
	if _, _, err := controller.DispatchTaskExecution(ctx, jobID, execution.ID, permission.ID, permissionRequest); err != nil {
		t.Fatalf("DispatchTaskExecution: %v", err)
	}
	return taskProviderStopFixture{
		ctx: ctx, database: database, principal: principal, jobID: jobID,
		task: task, execution: execution, permission: permission,
		permissionRequest: permissionRequest, runtime: runtime,
		worktree: worktree, controller: controller,
	}
}

func (f taskProviderStopFixture) stop(commandID string, reason domain.ProviderStopReason) (store.CommandReceipt, domain.ProviderStopIntent, domain.ProviderStopObservation, error) {
	return f.controller.StopTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		f.permissionRequest, reason, f.principal,
	)
}

type blockingTaskStopRuntime struct {
	*fakePlanningProviderRuntime
	entered chan struct{}
	release chan struct{}
}

func (r *blockingTaskStopRuntime) Stop(ctx context.Context, request provider.StopRequest) (provider.StopResult, error) {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		return provider.StopResult{}, ctx.Err()
	}
	return r.fakePlanningProviderRuntime.Stop(ctx, request)
}

func TestTaskProviderStopDispatchClaimPreventsConcurrentDuplicate(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	workerCtx, cancelWorker := context.WithCancel(f.ctx)
	f.ctx = workerCtx
	t.Cleanup(cancelWorker)
	blocking := &blockingTaskStopRuntime{
		fakePlanningProviderRuntime: f.runtime,
		entered:                     make(chan struct{}, 1),
		release:                     make(chan struct{}),
	}
	controller, err := NewProviderControllerWithExecutionAndWorktree(
		f.database, blocking, f.controller.execution, f.worktree,
	)
	if err != nil {
		t.Fatalf("NewProviderControllerWithExecutionAndWorktree: %v", err)
	}
	controller.now = f.controller.now
	f.controller = controller

	commandID := "cmd_adadadadadadadadadadadadadadadad"
	firstResult := make(chan error, 1)
	go func() {
		_, _, _, stopErr := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
		firstResult <- stopErr
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Stop did not reach the Provider")
	}
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob while Stop is in flight: %v", err)
	}
	if _, active := domain.ActiveProviderStopDispatchClaim(job, domain.ProviderStopOperationID(commandID)); !active {
		t.Fatal("Stop dispatch claim was not persisted in the JobAggregate before Provider dispatch")
	}

	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: domain.ProviderStopOperationID(commandID), SessionID: "session_1",
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("a", 64),
	}
	_, _, _, secondErr := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if !errors.Is(secondErr, provider.ErrStopUnconfirmed) {
		t.Fatalf("concurrent duplicate Stop was not rejected: %v", secondErr)
	}
	if f.runtime.stopCalls != 0 {
		t.Fatalf("second call reached Provider while first dispatch was in flight: calls=%d", f.runtime.stopCalls)
	}
	close(blocking.release)
	select {
	case firstErr := <-firstResult:
		if firstErr != nil {
			t.Fatalf("first Stop failed: %v", firstErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first Stop did not complete")
	}
	if f.runtime.stopCalls != 1 {
		t.Fatalf("same OperationID dispatched Stop %d times, want 1", f.runtime.stopCalls)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after Stop completion: %v", err)
	}
	if len(job.ProviderStopDispatchClaims) != 1 || len(job.ProviderStopDispatchReleases) != 1 {
		t.Fatalf("Stop dispatch claim lifecycle was not persisted: claims=%d releases=%d",
			len(job.ProviderStopDispatchClaims), len(job.ProviderStopDispatchReleases))
	}
	if _, active := domain.ActiveProviderStopDispatchClaim(job, domain.ProviderStopOperationID(commandID)); active {
		t.Fatal("completed Stop retained an active dispatch claim")
	}
}

func TestTaskProviderStopAmbiguousOutcomeIsNotReplayed(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	commandID := "cmd_b8080808080808080808080808080808"
	f.runtime.stopErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout}
	_, intent, first, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if err == nil || !providerStopObservationUnknown(first) || f.runtime.stopCalls != 1 {
		t.Fatalf("ambiguous Stop was not retained as UNKNOWN: intent=%#v observation=%#v calls=%d err=%v",
			intent, first, f.runtime.stopCalls, err)
	}
	f.runtime.stopErr = nil
	_, replayIntent, replay, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if !errors.Is(err, provider.ErrStopUnconfirmed) || replayIntent.ID != intent.ID ||
		replay.ID != first.ID || f.runtime.stopCalls != 1 {
		t.Fatalf("ambiguous Stop replay repeated or changed the operation: intent=%#v observation=%#v calls=%d err=%v",
			replayIntent, replay, f.runtime.stopCalls, err)
	}
	if _, _, _, err := f.stop("cmd_c9090909090909090909090909090909", domain.ProviderStopReasonCancelled); err == nil {
		t.Fatal("a new Stop OperationID bypassed the unresolved Stop")
	}
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.ProviderStopIntents) != 1 || len(job.ProviderStopObservations) != 1 || f.runtime.stopCalls != 1 {
		t.Fatalf("ambiguous Stop changed immutable facts or repeated side effects: intents=%d observations=%d calls=%d",
			len(job.ProviderStopIntents), len(job.ProviderStopObservations), f.runtime.stopCalls)
	}
}

func TestTaskProviderStopRetainsUnknownWhenNotFoundRetryIsAmbiguous(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	enableTaskStopLookup(&f)
	enableTaskStopFencing(&f)
	commandID := "cmd_19191919191919191919191919191919"
	f.runtime.stopErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout}
	_, intent, first, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if err == nil || !providerStopObservationUnknown(first) || f.runtime.stopCalls != 1 {
		t.Fatalf("initial ambiguous Stop was not retained: intent=%#v observation=%#v calls=%d err=%v",
			intent, first, f.runtime.stopCalls, err)
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: intent.OperationID, SessionID: intent.SessionID,
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("8", 64),
	}
	restarted := restartTaskProviderController(t, f)
	_, replayedIntent, replay, err := restarted.StopTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		f.permissionRequest, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if !errors.Is(err, provider.ErrStopUnconfirmed) || replayedIntent.ID != intent.ID ||
		replay.ID != first.ID || f.runtime.stopCalls != 2 {
		t.Fatalf("ambiguous NOT_FOUND retry did not preserve prior UNKNOWN: intent=%#v observation=%#v calls=%d err=%v",
			replayedIntent, replay, f.runtime.stopCalls, err)
	}
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after ambiguous NOT_FOUND retry: %v", err)
	}
	if len(job.ProviderStopObservations) != 1 || len(job.ProviderStopDispatchClaims) != 2 ||
		len(job.ProviderStopDispatchStarts) != 2 || len(job.ProviderStopDispatchReleases) != 1 {
		t.Fatalf("ambiguous retry changed durable Stop facts: observations=%d claims=%d starts=%d releases=%d",
			len(job.ProviderStopObservations), len(job.ProviderStopDispatchClaims),
			len(job.ProviderStopDispatchStarts), len(job.ProviderStopDispatchReleases))
	}
	if active, ok := domain.ActiveProviderStopDispatchClaim(job, intent.OperationID); !ok ||
		active.ID != job.ProviderStopDispatchClaims[1].ID {
		t.Fatalf("ambiguous retry did not retain the replacement claim: claim=%#v active=%v", active, ok)
	}
}

func TestTaskProviderStopCannotCommitWhileSendDispatchIsActive(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	workerCtx, cancelWorker := context.WithCancel(f.ctx)
	f.ctx = workerCtx
	t.Cleanup(cancelWorker)
	now := time.Now().UTC().Round(0)
	f.runtime.capabilities.SendLookup = true
	f.runtime.capabilities.SendIdempotency = true
	f.runtime.capabilities.FinalOutputRetrieval = true
	f.runtime.capabilities.UsageIdentity = true
	f.runtime.capabilities.UsageCompleteness = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "stop-send-race", EvidenceSHA256: strings.Repeat("1", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity, TestID: "stop-send-race", EvidenceSHA256: strings.Repeat("2", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: f.runtime.identity, TestID: "stop-send-race", EvidenceSHA256: strings.Repeat("3", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "stop-send-race", EvidenceSHA256: strings.Repeat("4", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "stop-send-race", EvidenceSHA256: strings.Repeat("5", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	f.runtime.sendHook = func(ctx context.Context, _ provider.TurnRequest) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	sendResult := make(chan error, 1)
	go func() {
		_, _, _, _, sendErr := f.controller.SendTaskExecution(
			f.ctx, f.jobID, f.execution.ID, f.permission.ID,
			"cmd_abababababababababababababababab", "Implement the approved Task changes.",
			f.permissionRequest, 100, f.principal,
		)
		sendResult <- sendErr
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Task Provider Send did not reach the dispatch boundary")
	}

	_, _, _, stopErr := f.stop("cmd_acacacacacacacacacacacacacacacac", domain.ProviderStopReasonTaskComplete)
	if stopErr == nil || f.runtime.stopCalls != 0 {
		t.Fatalf("Stop intent was committed or Provider Stop dispatched while Send claim was active: stopCalls=%d err=%v",
			f.runtime.stopCalls, stopErr)
	}
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob during Send: %v", err)
	}
	if len(job.ProviderStopIntents) != 0 {
		t.Fatalf("Stop intent was persisted during active Send: %d", len(job.ProviderStopIntents))
	}
	if _, active := domain.ActiveTaskTurnDispatchClaim(job, domain.TaskTurnOperationID("cmd_abababababababababababababababab")); !active {
		t.Fatal("Task Turn dispatch claim was not active while Send was in flight")
	}

	close(release)
	select {
	case sendErr := <-sendResult:
		if sendErr != nil {
			t.Fatalf("Task Provider Send failed: %v", sendErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Task Provider Send did not complete")
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after Send: %v", err)
	}
	if _, active := domain.ActiveTaskTurnDispatchClaim(job, domain.TaskTurnOperationID("cmd_abababababababababababababababab")); active {
		t.Fatal("terminal Send retained an active dispatch claim")
	}
	_, _, stopObservation, stopErr := f.stop("cmd_adadadadadadadadadadadadadadadad", domain.ProviderStopReasonTaskComplete)
	if stopErr != nil || !stopObservation.Quiescent || f.runtime.stopCalls != 1 {
		t.Fatalf("Stop did not proceed after Send became terminal: observation=%#v calls=%d err=%v",
			stopObservation, f.runtime.stopCalls, stopErr)
	}
}

func TestTaskProviderSendCannotDispatchAfterStopIntent(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	now := f.controller.now()
	f.runtime.capabilities.SendIdempotency = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence, provider.CapabilityEvidence{
		Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity,
		TestID: "task-stop-send-race", EvidenceSHA256: strings.Repeat("1", 64),
		EvidenceLevel: provider.EvidenceLive, VerifiedAt: now,
	})
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before Stop intent: %v", err)
	}
	if _, _, err := f.database.AddProviderStopIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, "session_1",
		"cmd_aeaeaeaeaeaeaeaeaeaeaeaeaeaeaeae",
		domain.ProviderStopReasonTaskComplete, f.principal,
	); err != nil {
		t.Fatalf("persist Stop intent: %v", err)
	}

	_, _, _, _, err = f.controller.SendTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID,
		"cmd_afafafafafafafafafafafafafafafaf",
		"Implement the approved Task changes.",
		f.permissionRequest, 100, f.principal,
	)
	if err == nil || f.runtime.sendCalls != 0 {
		t.Fatalf("Send crossed an existing Stop intent: sendCalls=%d err=%v", f.runtime.sendCalls, err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after rejected Send: %v", err)
	}
	if len(job.ProviderStopIntents) != 1 || len(job.TaskTurnIntents) != 0 ||
		len(job.TaskTurnDispatchClaims) != 0 || f.runtime.sendCalls != 0 {
		t.Fatalf("rejected Send changed execution state: stopIntents=%d turnIntents=%d claims=%d sendCalls=%d",
			len(job.ProviderStopIntents), len(job.TaskTurnIntents), len(job.TaskTurnDispatchClaims), f.runtime.sendCalls)
	}
}

func TestTaskProviderStopRejectsUnresolvedTaskTurn(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before pending Turn: %v", err)
	}
	if _, _, err := f.database.AddTaskTurnIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, f.permission.ID, "session_1",
		"cmd_abababababababababababababababab", strings.Repeat("a", 64), 100,
	); err != nil {
		t.Fatalf("persist pending Task Turn: %v", err)
	}
	if _, _, _, err := f.stop("cmd_acacacacacacacacacacacacacacacac", domain.ProviderStopReasonTaskComplete); err == nil {
		t.Fatal("Provider Stop was accepted while the same Session had an unresolved Task Turn")
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after rejected Stop: %v", err)
	}
	if len(job.ProviderStopIntents) != 0 || f.runtime.stopCalls != 0 {
		t.Fatalf("rejected Stop created an intent or reached Provider: intents=%d stopCalls=%d",
			len(job.ProviderStopIntents), f.runtime.stopCalls)
	}
}

func TestTaskProviderStopRejectsUnconfirmedProviderResponses(t *testing.T) {
	tests := []struct {
		name   string
		result provider.StopResult
	}{
		{name: "wrong operation identity", result: provider.StopResult{OperationID: "provider_stop_wrong", SessionID: "session_1", Stopped: true, Quiescent: true, EvidenceSHA256: strings.Repeat("d", 64)}},
		{name: "wrong session identity", result: provider.StopResult{OperationID: "provider_stop_cmd", SessionID: "session_other", Stopped: true, Quiescent: true, EvidenceSHA256: strings.Repeat("d", 64)}},
		{name: "not stopped", result: provider.StopResult{OperationID: "provider_stop_cmd", SessionID: "session_1", Stopped: false, Quiescent: false}},
		{name: "not quiescent", result: provider.StopResult{OperationID: "provider_stop_cmd", SessionID: "session_1", Stopped: true, Quiescent: false, EvidenceSHA256: strings.Repeat("d", 64)}},
		{name: "missing evidence", result: provider.StopResult{OperationID: "provider_stop_cmd", SessionID: "session_1", Stopped: true, Quiescent: true}},
		{name: "invalid evidence digest", result: provider.StopResult{OperationID: "provider_stop_cmd", SessionID: "session_1", Stopped: true, Quiescent: true, EvidenceSHA256: "not-a-digest"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newTaskProviderStopFixture(t)
			commandID := "cmd_d9090909090909090909090909090909"
			result := test.result
			if result.OperationID == "provider_stop_cmd" {
				result.OperationID = domain.ProviderStopOperationID(commandID)
			}
			f.runtime.stopResult = result
			_, _, observation, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
			if !errors.Is(err, provider.ErrStopUnconfirmed) || !providerStopObservationUnknown(observation) || f.runtime.stopCalls != 1 {
				t.Fatalf("unconfirmed Provider response was accepted: observation=%#v calls=%d err=%v",
					observation, f.runtime.stopCalls, err)
			}
			job, readErr := f.database.GetJob(f.ctx, f.jobID)
			if readErr != nil {
				t.Fatalf("GetJob: %v", readErr)
			}
			if len(job.ProviderStopIntents) != 1 || len(job.ProviderStopObservations) != 1 ||
				job.ProviderStopObservations[0].Stopped || job.ProviderStopObservations[0].Quiescent {
				t.Fatalf("invalid Provider response did not leave one UNKNOWN observation: %#v", job.ProviderStopObservations)
			}
		})
	}
}

func TestTaskProviderStopCapabilityAndWorktreeGatesHaveNoProviderSideEffect(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*taskProviderStopFixture)
	}{
		{name: "missing StopProof", mutate: func(f *taskProviderStopFixture) { f.runtime.capabilities.StopProof = false }},
		{name: "Provider identity mismatch", mutate: func(f *taskProviderStopFixture) {
			f.runtime.capabilities.Identity.ProviderID = "provider.other"
		}},
		{name: "Worktree verification failure before intent", mutate: func(f *taskProviderStopFixture) {
			f.worktree.failAt = f.worktree.verifyCalls + 1
			f.worktree.verifyErr = errors.New("Worktree verification failed")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newTaskProviderStopFixture(t)
			test.mutate(&f)
			_, _, _, err := f.stop("cmd_e1010101010101010101010101010101", domain.ProviderStopReasonTaskComplete)
			if err == nil || f.runtime.stopCalls != 0 {
				t.Fatalf("invalid Stop boundary reached Provider: calls=%d err=%v", f.runtime.stopCalls, err)
			}
			job, readErr := f.database.GetJob(f.ctx, f.jobID)
			if readErr != nil {
				t.Fatalf("GetJob: %v", readErr)
			}
			if len(job.ProviderStopIntents) != 0 || len(job.ProviderStopObservations) != 0 {
				t.Fatalf("pre-intent rejection persisted Stop facts: intents=%d observations=%d",
					len(job.ProviderStopIntents), len(job.ProviderStopObservations))
			}
		})
	}
}

func TestTaskProviderStopWorktreeRevalidationFailureAfterIntentCanRetryAfterLookupNotFound(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	f.worktree.failAt = f.worktree.verifyCalls + 2
	f.worktree.verifyErr = errors.New("Worktree changed immediately before Provider Stop")
	commandID := "cmd_f2020202020202020202020202020202"
	_, intent, _, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if err == nil || f.runtime.stopCalls != 0 {
		t.Fatalf("Provider Stop crossed a failed final Worktree check: calls=%d err=%v", f.runtime.stopCalls, err)
	}
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after final Worktree rejection: %v", err)
	}
	if len(job.ProviderStopIntents) != 1 || len(job.ProviderStopObservations) != 0 {
		t.Fatalf("intent-first boundary was not preserved: intents=%d observations=%d", len(job.ProviderStopIntents), len(job.ProviderStopObservations))
	}
	enableTaskStopLookup(&f)
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: intent.OperationID, SessionID: "session_1",
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("8", 64),
	}
	_, replayIntent, observation, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if err != nil || replayIntent.ID != intent.ID || !observation.Stopped || !observation.Quiescent ||
		f.runtime.stopCalls != 1 || f.worktree.verifyCalls < 4 {
		t.Fatalf("retry did not reverify Worktree and complete Stop after verified NOT_FOUND: intent=%#v observation=%#v verifies=%d calls=%d err=%v",
			replayIntent, observation, f.worktree.verifyCalls, f.runtime.stopCalls, err)
	}
}

func TestTaskProviderStopRejectsInvalidLookupAfterWorktreeRevalidationFailure(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	f.worktree.failAt = f.worktree.verifyCalls + 2
	f.worktree.verifyErr = errors.New("Worktree changed immediately before Provider Stop")
	commandID := "cmd_f3030303030303030303030303030303"
	_, intent, _, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if err == nil || f.runtime.stopCalls != 0 {
		t.Fatalf("Provider Stop crossed a failed final Worktree check: calls=%d err=%v", f.runtime.stopCalls, err)
	}
	enableTaskStopLookup(&f)
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: "wrong_operation", SessionID: "session_1",
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("8", 64),
	}
	_, replayIntent, observation, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if !errors.Is(err, provider.ErrStopUnconfirmed) || replayIntent.ID != intent.ID ||
		!providerStopObservationUnknown(observation) || f.runtime.stopCalls != 0 {
		t.Fatalf("invalid Lookup evidence was treated as Worktree recovery: intent=%#v observation=%#v calls=%d err=%v",
			replayIntent, observation, f.runtime.stopCalls, err)
	}
}

func TestTaskProviderStopConflictingReplayDoesNotRepeatProviderCall(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	commandID := "cmd_03030303030303030303030303030303"
	_, intent, observation, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if err != nil || !observation.Stopped || !observation.Quiescent || f.runtime.stopCalls != 1 {
		t.Fatalf("initial Stop failed: intent=%#v observation=%#v calls=%d err=%v",
			intent, observation, f.runtime.stopCalls, err)
	}
	if _, _, _, err := f.stop(commandID, domain.ProviderStopReasonCancelled); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("conflicting Stop replay error = %v, want idempotency conflict", err)
	}
	if f.runtime.stopCalls != 1 {
		t.Fatalf("conflicting replay repeated Provider Stop: calls=%d", f.runtime.stopCalls)
	}
}

func enableTaskStopLookup(f *taskProviderStopFixture) {
	f.runtime.capabilities.StopLookup = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence, provider.CapabilityEvidence{
		Capability: provider.CapabilityStopLookup, Identity: f.runtime.identity,
		TestID: "task-stop-lookup-test", EvidenceSHA256: strings.Repeat("f", 64),
		EvidenceLevel: provider.EvidenceLive, VerifiedAt: f.controller.now(),
	})
}

func enableTaskStopFencing(f *taskProviderStopFixture) {
	f.runtime.capabilities.StopDispatchFencing = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence, provider.CapabilityEvidence{
		Capability: provider.CapabilityStopDispatchFencing, Identity: f.runtime.identity,
		TestID: "task-stop-fencing-test", EvidenceSHA256: strings.Repeat("9", 64),
		EvidenceLevel: provider.EvidenceLive, VerifiedAt: f.controller.now(),
	})
}

func restartTaskProviderController(t *testing.T, f taskProviderStopFixture) *ProviderController {
	t.Helper()
	controller, err := NewProviderControllerWithExecutionAndWorktree(
		f.database, f.runtime, f.controller.execution, f.worktree,
	)
	if err != nil {
		t.Fatalf("restart ProviderController: %v", err)
	}
	controller.now = f.controller.now
	return controller
}

func TestTaskProviderStopReconcilesProviderSideEffectAfterControllerRestart(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	enableTaskStopLookup(&f)
	commandID := "cmd_f3030303030303030303030303030303"
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before Stop intent: %v", err)
	}
	_, intent, err := f.database.AddProviderStopIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, "session_1",
		commandID, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil {
		t.Fatalf("persist Stop intent before simulated crash: %v", err)
	}
	// Simulate the Provider completing Stop after the intent commit but before
	// Taxion persists its terminal observation.
	if _, err := f.runtime.Stop(f.ctx, provider.StopRequest{
		OperationID: intent.OperationID, SessionID: "session_1", FencingToken: 1,
	}); err != nil {
		t.Fatalf("simulate Provider Stop side effect: %v", err)
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: true, Stopped: true, Quiescent: true,
		OperationID: intent.OperationID, Status: "STOPPED",
		ReceiptID: "receipt_stop", SessionID: "session_1",
		EvidenceSHA256: strings.Repeat("9", 64),
	}
	restarted := restartTaskProviderController(t, f)
	_, replayedIntent, observation, err := restarted.StopTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		f.permissionRequest, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil || replayedIntent.ID != intent.ID || !observation.Stopped ||
		!observation.Quiescent || observation.ReconcilesUnknown || observation.CreatedRevision == 0 ||
		f.runtime.stopCalls != 1 || f.runtime.lookupCalls != 2 {
		t.Fatalf("restart did not reconcile the existing Provider Stop: intent=%#v observation=%#v stopCalls=%d lookupCalls=%d err=%v",
			replayedIntent, observation, f.runtime.stopCalls, f.runtime.lookupCalls, err)
	}
}

func TestTaskProviderStopRetriesOnlyAfterRestartLookupConfirmsNotFound(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	enableTaskStopLookup(&f)
	commandID := "cmd_f4040404040404040404040404040404"
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before Stop intent: %v", err)
	}
	_, intent, err := f.database.AddProviderStopIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, "session_1",
		commandID, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil {
		t.Fatalf("persist Stop intent before simulated crash: %v", err)
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: intent.OperationID, Status: provider.LookupStatusNotFound,
		SessionID: "session_1", EvidenceSHA256: strings.Repeat("8", 64),
	}
	restarted := restartTaskProviderController(t, f)
	_, replayedIntent, observation, err := restarted.StopTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		f.permissionRequest, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil || replayedIntent.ID != intent.ID || !observation.Stopped ||
		!observation.Quiescent || f.runtime.stopCalls != 1 || f.runtime.lookupCalls != 2 {
		t.Fatalf("restart did not retry the original Stop after confirmed NOT_FOUND: intent=%#v observation=%#v stopCalls=%d lookupCalls=%d err=%v",
			replayedIntent, observation, f.runtime.stopCalls, f.runtime.lookupCalls, err)
	}
}

func TestTaskProviderStopLookupRecoveryDoesNotRequireStopProof(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	commandID := "cmd_12121212121212121212121212121212"
	f.runtime.stopErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeTimeout}
	_, intent, unknown, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if err == nil || !providerStopObservationUnknown(unknown) || f.runtime.stopCalls != 1 {
		t.Fatalf("initial ambiguous Stop was not retained: observation=%#v calls=%d err=%v", unknown, f.runtime.stopCalls, err)
	}
	f.runtime.stopErr = nil
	f.runtime.capabilities.StopProof = false
	f.runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: intent.OperationID, SessionID: intent.SessionID,
		Status: "STOPPED", ReceiptID: "receipt_stop", Stopped: true, Quiescent: true, EvidenceSHA256: strings.Repeat("9", 64),
	}
	_, replayedIntent, stopped, err := f.stop(commandID, domain.ProviderStopReasonTaskComplete)
	if err != nil || replayedIntent.ID != intent.ID || !stopped.Stopped || !stopped.Quiescent || f.runtime.stopCalls != 1 {
		t.Fatalf("Stop Lookup recovery incorrectly required StopProof or repeated Stop: intent=%#v observation=%#v calls=%d err=%v",
			replayedIntent, stopped, f.runtime.stopCalls, err)
	}
}

func TestTaskProviderStopRecoversClaimCommittedBeforeDispatchAfterRestart(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	enableTaskStopLookup(&f)
	commandID := "cmd_13131313131313131313131313131313"
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before Stop intent: %v", err)
	}
	_, intent, err := f.database.AddProviderStopIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, "session_1",
		commandID, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil {
		t.Fatalf("persist Stop intent before simulated crash: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before dispatch claim: %v", err)
	}
	_, claim, created, err := f.database.AddProviderStopDispatchClaim(
		f.ctx, f.jobID, job.Revision, intent.ID,
		"cmd_14141414141414141414141414141414", f.principal,
	)
	if err != nil || !created {
		t.Fatalf("persist pre-dispatch claim: claim=%#v created=%v err=%v", claim, created, err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after pre-dispatch claim: %v", err)
	}
	if _, active := domain.ActiveProviderStopDispatchClaim(job, intent.OperationID); !active {
		t.Fatal("pre-dispatch claim is not active")
	}
	if _, started := domain.ProviderStopDispatchStartedForClaim(job, claim.ID); started {
		t.Fatal("pre-dispatch claim unexpectedly has a dispatch-start fact")
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: intent.OperationID, SessionID: "session_1",
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("8", 64),
	}

	restarted := restartTaskProviderController(t, f)
	_, replayedIntent, observation, err := restarted.StopTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		f.permissionRequest, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil || replayedIntent.ID != intent.ID || !observation.Stopped ||
		!observation.Quiescent || f.runtime.stopCalls != 1 || f.runtime.lookupCalls != 2 {
		t.Fatalf("restart did not reclaim a claim committed before dispatch: intent=%#v observation=%#v stopCalls=%d lookupCalls=%d err=%v",
			replayedIntent, observation, f.runtime.stopCalls, f.runtime.lookupCalls, err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after recovered Stop: %v", err)
	}
	if len(job.ProviderStopDispatchClaims) != 2 || len(job.ProviderStopDispatchStarts) != 1 ||
		len(job.ProviderStopDispatchReleases) != 2 {
		t.Fatalf("recovered dispatch facts mismatch: claims=%d starts=%d releases=%d",
			len(job.ProviderStopDispatchClaims), len(job.ProviderStopDispatchStarts), len(job.ProviderStopDispatchReleases))
	}
	if job.ProviderStopDispatchReleases[0].ClaimID != claim.ID ||
		job.ProviderStopDispatchReleases[0].Outcome != domain.ProviderStopDispatchReleasedNotDispatched ||
		job.ProviderStopDispatchReleases[0].EvidenceSHA256 != strings.Repeat("8", 64) {
		t.Fatalf("old claim was not released as not dispatched: %#v", job.ProviderStopDispatchReleases[0])
	}
	if _, started := domain.ProviderStopDispatchStartedForClaim(job, job.ProviderStopDispatchClaims[1].ID); !started {
		t.Fatal("replacement claim has no durable dispatch-start fact")
	}
	if _, _, _, err := f.database.StartProviderStopDispatchClaim(f.ctx, f.jobID, claim.ID, "", f.principal); err == nil {
		t.Fatal("released stale claim was allowed to cross the dispatch-start fence")
	}
}

func TestTaskProviderStopReclaimsStartedClaimWithProviderFencing(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	enableTaskStopLookup(&f)
	enableTaskStopFencing(&f)
	commandID := "cmd_17171717171717171717171717171717"
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before Stop intent: %v", err)
	}
	_, intent, err := f.database.AddProviderStopIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, "session_1",
		commandID, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil {
		t.Fatalf("persist Stop intent: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before dispatch claim: %v", err)
	}
	_, oldClaim, created, err := f.database.AddProviderStopDispatchClaim(
		f.ctx, f.jobID, job.Revision, intent.ID,
		"cmd_18181818181818181818181818181818", f.principal,
	)
	if err != nil || !created {
		t.Fatalf("persist old dispatch claim: claim=%#v created=%v err=%v", oldClaim, created, err)
	}
	if _, _, started, err := f.database.StartProviderStopDispatchClaim(
		f.ctx, f.jobID, oldClaim.ID, strings.Repeat("9", 64), f.principal,
	); err != nil || !started {
		t.Fatalf("persist old dispatch-start fact: started=%v err=%v", started, err)
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: intent.OperationID, SessionID: "session_1",
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("8", 64),
	}

	restarted := restartTaskProviderController(t, f)
	_, replayedIntent, observation, err := restarted.StopTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		f.permissionRequest, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil || replayedIntent.ID != intent.ID || !observation.Stopped || !observation.Quiescent {
		t.Fatalf("fenced Stop recovery failed: intent=%#v observation=%#v err=%v", replayedIntent, observation, err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after fenced Stop recovery: %v", err)
	}
	if len(job.ProviderStopDispatchClaims) != 2 || len(job.ProviderStopDispatchStarts) != 2 ||
		len(job.ProviderStopDispatchReleases) != 2 ||
		job.ProviderStopDispatchStarts[0].FencingEvidenceSHA256 != strings.Repeat("9", 64) ||
		job.ProviderStopDispatchStarts[1].FencingEvidenceSHA256 != strings.Repeat("9", 64) {
		t.Fatalf("fenced recovery facts mismatch: claims=%d starts=%d releases=%d",
			len(job.ProviderStopDispatchClaims), len(job.ProviderStopDispatchStarts), len(job.ProviderStopDispatchReleases))
	}
	release := job.ProviderStopDispatchReleases[0]
	if release.ClaimID != oldClaim.ID || release.Outcome != domain.ProviderStopDispatchReleasedFencedNotFound ||
		release.EvidenceSHA256 != strings.Repeat("8", 64) {
		t.Fatalf("old started claim was not released with verified NOT_FOUND evidence: %#v", release)
	}
	newClaim := job.ProviderStopDispatchClaims[1]
	if newClaim.CreatedRevision <= oldClaim.CreatedRevision {
		t.Fatalf("replacement claim did not advance fencing token: old=%d new=%d",
			oldClaim.CreatedRevision, newClaim.CreatedRevision)
	}
	if len(f.runtime.stopRequests) != 1 || f.runtime.stopRequests[0].FencingToken != newClaim.CreatedRevision ||
		!f.runtime.stopRequests[0].RequireFencing {
		t.Fatalf("Provider Stop did not receive replacement fencing token: requests=%#v claim=%#v",
			f.runtime.stopRequests, newClaim)
	}
	duplicate, duplicateErr := f.runtime.Stop(f.ctx, provider.StopRequest{
		OperationID: intent.OperationID, SessionID: "session_1", FencingToken: newClaim.CreatedRevision, RequireFencing: true,
	})
	if duplicateErr != nil || duplicate.OperationID != intent.OperationID || f.runtime.stopCalls != 1 {
		t.Fatalf("same operation was not deduplicated: result=%#v calls=%d err=%v", duplicate, f.runtime.stopCalls, duplicateErr)
	}
	_, staleErr := f.runtime.Stop(f.ctx, provider.StopRequest{
		OperationID: intent.OperationID, SessionID: "session_1", FencingToken: oldClaim.CreatedRevision, RequireFencing: true,
	})
	if staleErr == nil || f.runtime.stopCalls != 1 {
		t.Fatalf("stale caller was not fenced after recovery: calls=%d err=%v", f.runtime.stopCalls, staleErr)
	}
}

func TestTaskProviderStopDoesNotReclaimClaimAfterDispatchStart(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	enableTaskStopLookup(&f)
	commandID := "cmd_15151515151515151515151515151515"
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before Stop intent: %v", err)
	}
	_, intent, err := f.database.AddProviderStopIntent(
		f.ctx, f.jobID, job.Revision, f.execution.ID, "session_1",
		commandID, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if err != nil {
		t.Fatalf("persist Stop intent: %v", err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob before dispatch claim: %v", err)
	}
	_, claim, created, err := f.database.AddProviderStopDispatchClaim(
		f.ctx, f.jobID, job.Revision, intent.ID,
		"cmd_16161616161616161616161616161616", f.principal,
	)
	if err != nil || !created {
		t.Fatalf("persist dispatch claim: claim=%#v created=%v err=%v", claim, created, err)
	}
	if _, _, started, err := f.database.StartProviderStopDispatchClaim(f.ctx, f.jobID, claim.ID, "", f.principal); err != nil || !started {
		t.Fatalf("persist dispatch-start fact: started=%v err=%v", started, err)
	}
	if _, _, err := f.database.ReleaseProviderStopDispatchClaim(
		f.ctx, f.jobID, claim.ID, f.principal, domain.ProviderStopDispatchReleasedNotDispatched, strings.Repeat("8", 64),
	); err == nil {
		t.Fatal("started claim was released as not dispatched")
	}
	if _, _, err := f.database.ReleaseProviderStopDispatchClaim(
		f.ctx, f.jobID, claim.ID, f.principal, domain.ProviderStopDispatchReleasedFencedNotFound, strings.Repeat("8", 64),
	); err == nil {
		t.Fatal("started claim without dispatch fencing evidence was released as fenced NOT_FOUND")
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: intent.OperationID, SessionID: "session_1",
		Status: provider.LookupStatusNotFound, EvidenceSHA256: strings.Repeat("8", 64),
	}

	restarted := restartTaskProviderController(t, f)
	_, replayedIntent, _, err := restarted.StopTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		f.permissionRequest, domain.ProviderStopReasonTaskComplete, f.principal,
	)
	if !errors.Is(err, provider.ErrStopUnconfirmed) || replayedIntent.ID != intent.ID ||
		f.runtime.stopCalls != 0 {
		t.Fatalf("started claim was incorrectly reclaimed or redispatched: intent=%#v stopCalls=%d err=%v",
			replayedIntent, f.runtime.stopCalls, err)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after conservative recovery: %v", err)
	}
	if len(job.ProviderStopDispatchClaims) != 1 || len(job.ProviderStopDispatchStarts) != 1 ||
		len(job.ProviderStopDispatchReleases) != 0 {
		t.Fatalf("started claim was changed during recovery: claims=%d starts=%d releases=%d",
			len(job.ProviderStopDispatchClaims), len(job.ProviderStopDispatchStarts), len(job.ProviderStopDispatchReleases))
	}
	if _, active := domain.ActiveProviderStopDispatchClaim(job, intent.OperationID); !active {
		t.Fatal("started claim was released after NOT_FOUND")
	}
}

func TestTaskProviderStopRevalidatesWorktreeAfterClaimBeforeDispatch(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	f.worktree.failAt = f.worktree.verifyCalls + 3
	f.worktree.verifyErr = errors.New("Worktree changed after claim")
	_, intent, _, err := f.stop("cmd_56565656565656565656565656565656", domain.ProviderStopReasonTaskComplete)
	if intent.ID == "" || err == nil || f.runtime.stopCalls != 0 {
		t.Fatalf("Stop dispatched after post-claim Worktree verification failed: intent=%#v calls=%d err=%v",
			intent, f.runtime.stopCalls, err)
	}
	job, readErr := f.database.GetJob(f.ctx, f.jobID)
	if readErr != nil {
		t.Fatalf("GetJob: %v", readErr)
	}
	if len(job.ProviderStopDispatchStarts) != 0 {
		t.Fatalf("Stop dispatch started despite failed post-claim verification: %#v", job.ProviderStopDispatchStarts)
	}
	if _, active := domain.ActiveProviderStopDispatchClaim(job, domain.ProviderStopOperationID(intent.CommandID)); active {
		t.Fatal("pre-dispatch Stop claim was not released after verification failure")
	}
}

func TestTaskProviderStopDispatchFactsRequireQuiescentAndDefinitiveEvidence(t *testing.T) {
	t.Run("reject start after quiescence", func(t *testing.T) {
		f := newTaskProviderStopFixture(t)
		job, err := f.database.GetJob(f.ctx, f.jobID)
		if err != nil {
			t.Fatal(err)
		}
		principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
		now := f.controller.now()
		intent, err := domain.NewProviderStopIntent(job, f.execution.ID, principalID, "session_1",
			"cmd_57575757575757575757575757575757", domain.ProviderStopReasonTaskComplete, now.Add(time.Second))
		if err != nil {
			t.Fatalf("NewProviderStopIntent: %v", err)
		}
		job.ProviderStopIntents = append(job.ProviderStopIntents, intent)
		job.Revision++
		job.UpdatedAt = intent.CreatedAt
		claim, err := domain.NewProviderStopDispatchClaim(job, intent.ID, principalID,
			"cmd_58585858585858585858585858585858", now.Add(2*time.Second))
		if err != nil {
			t.Fatalf("NewProviderStopDispatchClaim: %v", err)
		}
		job.ProviderStopDispatchClaims = append(job.ProviderStopDispatchClaims, claim)
		job.Revision++
		job.UpdatedAt = claim.CreatedAt
		quiescent, err := domain.NewProviderStopObservation(job, intent.ID, principalID,
			"cmd_59595959595959595959595959595959", true, true, strings.Repeat("a", 64), false, now.Add(3*time.Second))
		if err != nil {
			t.Fatalf("NewProviderStopObservation: %v", err)
		}
		job.ProviderStopObservations = append(job.ProviderStopObservations, quiescent)
		job.Revision++
		job.UpdatedAt = quiescent.CreatedAt
		if _, err := domain.NewProviderStopDispatchStart(job, claim.ID, principalID,
			domain.ProviderStopDispatchStartCommandID(claim.ID), "", now.Add(4*time.Second)); err == nil {
			t.Fatal("Stop dispatch started after a quiescent observation")
		}
	})

	t.Run("started definitive failure requires matching observation", func(t *testing.T) {
		f := newTaskProviderStopFixture(t)
		job, err := f.database.GetJob(f.ctx, f.jobID)
		if err != nil {
			t.Fatal(err)
		}
		principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
		now := f.controller.now()
		intent, err := domain.NewProviderStopIntent(job, f.execution.ID, principalID, "session_1",
			"cmd_67676767676767676767676767676767", domain.ProviderStopReasonTaskComplete, now.Add(time.Second))
		if err != nil {
			t.Fatalf("NewProviderStopIntent: %v", err)
		}
		job.ProviderStopIntents = append(job.ProviderStopIntents, intent)
		job.Revision++
		job.UpdatedAt = intent.CreatedAt
		claim, err := domain.NewProviderStopDispatchClaim(job, intent.ID, principalID,
			"cmd_68686868686868686868686868686868", now.Add(2*time.Second))
		if err != nil {
			t.Fatalf("NewProviderStopDispatchClaim: %v", err)
		}
		job.ProviderStopDispatchClaims = append(job.ProviderStopDispatchClaims, claim)
		job.Revision++
		job.UpdatedAt = claim.CreatedAt
		start, err := domain.NewProviderStopDispatchStart(job, claim.ID, principalID,
			domain.ProviderStopDispatchStartCommandID(claim.ID), "", now.Add(3*time.Second))
		if err != nil {
			t.Fatalf("NewProviderStopDispatchStart: %v", err)
		}
		job.ProviderStopDispatchStarts = append(job.ProviderStopDispatchStarts, start)
		job.Revision++
		job.UpdatedAt = start.CreatedAt
		releaseCommand := domain.ProviderStopDispatchReleaseCommandID(claim.ID, domain.ProviderStopDispatchReleasedDefinitiveFailure)
		if _, err := domain.NewProviderStopDispatchRelease(job, claim.ID, principalID, releaseCommand,
			domain.ProviderStopDispatchReleasedDefinitiveFailure, "", now.Add(4*time.Second)); err == nil {
			t.Fatal("started Stop claim was released without persisted definitive-failure evidence")
		}
		evidence := strings.Repeat("b", 64)
		failureObservation, err := domain.NewProviderStopObservation(job, intent.ID, principalID,
			"cmd_69696969696969696969696969696969", false, false, evidence, false, now.Add(4*time.Second))
		if err != nil {
			t.Fatalf("NewProviderStopObservation: %v", err)
		}
		job.ProviderStopObservations = append(job.ProviderStopObservations, failureObservation)
		job.Revision++
		job.UpdatedAt = failureObservation.CreatedAt
		if _, err := domain.NewProviderStopDispatchRelease(job, claim.ID, principalID, releaseCommand,
			domain.ProviderStopDispatchReleasedDefinitiveFailure, evidence, now.Add(5*time.Second)); err != nil {
			t.Fatalf("matching definitive-failure evidence did not release Stop claim: %v", err)
		}
	})
}
