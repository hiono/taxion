package domain

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func taskTurnFixture(t *testing.T) (Job, RoleExecution, ExecutionPermission, time.Time) {
	t.Helper()
	job, task, now := approvedJobWithTaskForExecutionPermission(t)
	request := executionPermissionTestRequest(job, now)
	request.ReservedTurns, request.ReservedTokens = 2, 100
	digest, err := ExecutionPermissionRequestSHA256(request)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := NewExecutionPermission(job, request, digest, executionPermissionTestAssessment(request, digest, now), now)
	if err != nil {
		t.Fatal(err)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = permission.CreatedAt
	tempRoot := t.TempDir()
	snapshot := WorktreeSnapshot{
		RepositoryID: task.TargetRepositories[0], RepositoryPath: filepath.Join(tempRoot, "source"),
		WorktreePath: filepath.Join(tempRoot, "worktree"), BaselineCommit: strings.Repeat("a", 40),
	}
	start, err := NewTaskRoleExecutionStart(job, task.ID, permission.ID,
		"cmd_d1010101010101010101010101010101", digest, snapshot, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	job.WorktreeAssignments = append(job.WorktreeAssignments, start.WorktreeAssignment)
	job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
	job.Revision++
	job.UpdatedAt = start.ProviderStartIntent.CreatedAt
	observation, err := NewProviderStartObservation(job, start.ProviderStartIntent.ID, "",
		"cmd_d2020202020202020202020202020202", ProviderStartAccepted, "session_task", "receipt_task",
		strings.Repeat("b", 64), false, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	job.ProviderStartObservations = append(job.ProviderStartObservations, observation)
	job.Revision++
	job.UpdatedAt = observation.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return job, start.RoleExecution, permission, observation.CreatedAt.Add(time.Minute)
}

func TestTaskTurnIntentUsesExecutionPermissionAndAcceptedSession(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_d3030303030303030303030303030303", strings.Repeat("c", 64), 80, now)
	if err != nil {
		t.Fatal(err)
	}
	if turn.TaskID != execution.TaskID || turn.ExecutionPermissionID != permission.ID || turn.TurnNumber != 1 {
		t.Fatalf("incorrect Task Turn binding: %#v", turn)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskTurnRejectsDifferentSessionAndUnresolvedPriorTurn(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	first, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_d4040404040404040404040404040404", strings.Repeat("d", 64), 80, now)
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, first)
	job.Revision++
	job.UpdatedAt = first.CreatedAt
	if _, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_other",
		"cmd_d5050505050505050505050505050505", strings.Repeat("e", 64), 1, now.Add(time.Minute)); err == nil {
		t.Fatal("Session change was accepted")
	}
	if _, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_d6060606060606060606060606060606", strings.Repeat("f", 64), 1, now.Add(time.Minute)); err == nil {
		t.Fatal("new Turn started before previous Turn observation")
	}
}

func TestTaskTurnUnknownReconciliationAndCumulativeBudget(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_d7070707070707070707070707070707", strings.Repeat("1", 64), 80, now)
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	unknown, err := NewTaskTurnObservation(job, turn.ID, "cmd_d8080808080808080808080808080808",
		TaskTurnUnknown, 0, "", false, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	if _, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_d9090909090909090909090909090909", strings.Repeat("2", 64), 1, now.Add(2*time.Minute)); err == nil {
		t.Fatal("new Turn started while prior Turn UNKNOWN")
	}
	if _, err := NewTaskTurnObservation(job, turn.ID, "cmd_da0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
		TaskTurnCompleted, 75, strings.Repeat("3", 64), true, now.Add(2*time.Minute)); err == nil {
		t.Fatal("terminal Task Turn was accepted without final Result and complete Usage")
	}
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	content := "completed Task Coder output"
	contentSHA := sha256HexDomain(content)
	result, err := NewProviderResultObservation(job, execution.ID, principalID, turn.OperationID, turn.ID,
		turn.ProviderSessionID, content, contentSHA, true, strings.Repeat("a", 64), now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("create final Task Result: %v", err)
	}
	job.ProviderResultObservations = append(job.ProviderResultObservations, result)
	job.Revision++
	job.UpdatedAt = result.CreatedAt
	if _, err := NewProviderUsageObservation(job, execution.ID, principalID, turn.OperationID, turn.ID,
		turn.ProviderSessionID, "usage_task_turn_over_limit", 81, true, strings.Repeat("6", 64), now.Add(3*time.Minute)); err == nil {
		t.Fatal("complete Provider Usage exceeded the Task Turn reserved token budget")
	}
	usage, err := NewProviderUsageObservation(job, execution.ID, principalID, turn.OperationID, turn.ID,
		turn.ProviderSessionID, "usage_task_turn", 75, true, strings.Repeat("7", 64), now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("create complete Task Usage: %v", err)
	}
	job.ProviderUsageObservations = append(job.ProviderUsageObservations, usage)
	job.Revision++
	job.UpdatedAt = usage.CreatedAt
	if _, err := NewProviderUsageObservation(job, execution.ID, principalID, turn.OperationID, turn.ID,
		turn.ProviderSessionID, "usage_task_turn_duplicate", 76, true, strings.Repeat("9", 64), now.Add(3*time.Minute+30*time.Second)); err == nil {
		t.Fatal("a second complete Provider Usage with a different UsageID was accepted for the same Turn")
	}
	completed, err := NewTaskTurnObservation(job, turn.ID, "cmd_da0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
		TaskTurnCompleted, 75, strings.Repeat("6", 64), true, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, completed)
	job.Revision++
	job.UpdatedAt = completed.CreatedAt
	if _, err := NewProviderUsageObservation(job, execution.ID, principalID, turn.OperationID, turn.ID,
		turn.ProviderSessionID, "usage_after_terminal", 1, true, strings.Repeat("b", 64), now.Add(4*time.Minute+30*time.Second)); err == nil {
		t.Fatal("Provider Usage was accepted after terminal Task Turn observation")
	}
	if _, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_db0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", strings.Repeat("4", 64), 26, now.Add(5*time.Minute)); err == nil {
		t.Fatal("Turn exceeded remaining token budget")
	}
	if _, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_dc0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c", strings.Repeat("5", 64), 25, now.Add(6*time.Minute)); err != nil {
		t.Fatalf("Turn at remaining budget rejected: %v", err)
	}
	principalID = job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	stopIntent, err := NewProviderStopIntent(job, execution.ID, principalID, turn.ProviderSessionID,
		"cmd_d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1", ProviderStopReasonTaskComplete, now.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("create Provider Stop intent: %v", err)
	}
	job.ProviderStopIntents = append(job.ProviderStopIntents, stopIntent)
	job.Revision++
	job.UpdatedAt = stopIntent.CreatedAt
	stopObservation, err := NewProviderStopObservation(job, stopIntent.ID, principalID,
		"cmd_d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2", true, true, strings.Repeat("8", 64), false, now.Add(8*time.Minute))
	if err != nil {
		t.Fatalf("create Provider Stop observation: %v", err)
	}
	job.ProviderStopObservations = append(job.ProviderStopObservations, stopObservation)
	job.Revision++
	job.UpdatedAt = stopObservation.CreatedAt
	output, err := NewJobOutput(job, execution.TaskID, execution.ID, turn.ID,
		strings.Repeat("9", 40), strings.Repeat("8", 40), now.Add(9*time.Minute))
	if err != nil {
		t.Fatalf("create immutable JobOutput: %v", err)
	}
	tampered := output
	tampered.ProviderStopEvidenceSHA256 = strings.Repeat("f", 64)
	if err := tampered.Validate(job); err == nil {
		t.Fatal("JobOutput accepted changed Provider Stop evidence")
	}
	tampered = output
	tampered.CommitSHA = "invalid"
	if err := tampered.Validate(job); err == nil {
		t.Fatal("JobOutput accepted an invalid Git commit identity")
	}
	job.JobOutputs = append(job.JobOutputs, output)
	job.Revision++
	job.UpdatedAt = output.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskTurnMustFollowPreviousTerminalObservationTime(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	first, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_dddddddddddddddddddddddddddddddd", strings.Repeat("d", 64), 40, now)
	if err != nil {
		t.Fatalf("first Task Turn: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, first)
	job.Revision++
	job.UpdatedAt = first.CreatedAt

	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	resultContent := "completed Task Coder output"
	result, err := NewProviderResultObservation(job, execution.ID, principalID,
		first.OperationID, first.ID, first.ProviderSessionID, resultContent,
		sha256HexDomain(resultContent), true, strings.Repeat("e", 64), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("final Provider Result: %v", err)
	}
	job.ProviderResultObservations = append(job.ProviderResultObservations, result)
	job.Revision++
	job.UpdatedAt = result.CreatedAt

	usage, err := NewProviderUsageObservation(job, execution.ID, principalID,
		first.OperationID, first.ID, first.ProviderSessionID, "usage_task_turn",
		10, true, strings.Repeat("f", 64), now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("complete Provider Usage: %v", err)
	}
	job.ProviderUsageObservations = append(job.ProviderUsageObservations, usage)
	job.Revision++
	job.UpdatedAt = usage.CreatedAt

	terminal, err := NewTaskTurnObservation(job, first.ID,
		"cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", TaskTurnCompleted, 10,
		strings.Repeat("e", 64), false, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("terminal observation: %v", err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, terminal)
	job.Revision++
	job.UpdatedAt = terminal.CreatedAt

	if _, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_ffffffffffffffffffffffffffffffff", strings.Repeat("f", 64), 20,
		now.Add(30*time.Second)); err == nil {
		t.Fatal("new Task Turn was accepted before the previous terminal observation")
	}
	next, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_11111111111111111111111111111111", strings.Repeat("1", 64), 20,
		now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("Task Turn after terminal observation: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, next)
	job.Revision++
	job.UpdatedAt = next.CreatedAt

	job.TaskTurnIntents[1].CreatedAt = now.Add(30 * time.Second)
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate accepted a Task Turn timestamp before the previous terminal observation")
	}
}

func TestWorktreeInitialStateSHA256Requires64HexCharacters(t *testing.T) {
	job, execution, _, now := taskTurnFixture(t)
	task, found := taskByID(job, execution.TaskID)
	if !found || len(job.WorktreeAssignments) != 1 {
		t.Fatal("Task Worktree fixture is incomplete")
	}
	snapshot := job.WorktreeAssignments[0].Snapshot()
	snapshot.InitialStateSHA256 = strings.Repeat("c", 64)
	if _, err := NewWorktreeAssignment(job, task, execution, snapshot, now); err != nil {
		t.Fatalf("valid SHA-256 initial state was rejected: %v", err)
	}
	snapshot.InitialStateSHA256 = strings.Repeat("c", 40)
	if _, err := NewWorktreeAssignment(job, task, execution, snapshot, now); err == nil {
		t.Fatal("40-character Git commit ID was accepted as an InitialStateSHA256")
	}
	job.WorktreeAssignments[0].InitialStateSHA256 = strings.Repeat("c", 64)
	if err := job.Validate(); err != nil {
		t.Fatalf("valid persisted SHA-256 initial state was rejected: %v", err)
	}
	job.WorktreeAssignments[0].InitialStateSHA256 = strings.Repeat("c", 40)
	if err := job.Validate(); err == nil {
		t.Fatal("persisted 40-character Git commit ID was accepted as an InitialStateSHA256")
	}

	duplicateOwner := job
	duplicateOwner.RoleExecutions = append(append([]RoleExecution(nil), job.RoleExecutions...), execution)
	duplicateOwner.RoleExecutions[len(duplicateOwner.RoleExecutions)-1].ID = "role_execution_other"
	if err := validateWorktreeAssignments(duplicateOwner); err == nil {
		t.Fatal("Worktree assignment was accepted for a non-owner RoleExecution")
	}
}

func TestTaskTurnDispatchCannotReleaseClaimAfterUnknownObservationAsDefinitiveFailure(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_13131313131313131313131313131313", strings.Repeat("1", 64), 40, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	claim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_14141414141414141414141414141414", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnDispatchClaim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claim)
	job.Revision++
	job.UpdatedAt = claim.CreatedAt
	unknown, err := NewTaskTurnObservation(job, turn.ID,
		"cmd_15151515151515151515151515151515", TaskTurnUnknown, 0, "", false, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnObservation UNKNOWN: %v", err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	commandID := TaskTurnDispatchReleaseCommandID(claim.ID, TaskTurnDispatchReleasedDefinitiveFailure)
	if _, err := NewTaskTurnDispatchRelease(job, claim.ID, principalID, commandID,
		TaskTurnDispatchReleasedDefinitiveFailure, "", now.Add(3*time.Minute)); err == nil {
		t.Fatal("UNKNOWN Task Turn claim was released as definitive_failure without reconciliation evidence")
	}
}

func TestTaskTurnDispatchDefinitiveFailureIsScopedToReplacementClaim(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_18181818181818181818181818181818", strings.Repeat("1", 64), 40, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	firstClaim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_19191919191919191919191919191919", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("New first dispatch claim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, firstClaim)
	job.Revision++
	job.UpdatedAt = firstClaim.CreatedAt
	unknown, err := NewTaskTurnObservation(job, turn.ID,
		"cmd_20202020202020202020202020202020", TaskTurnUnknown, 0, "", false, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("New Task Turn UNKNOWN: %v", err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	retryCommand := TaskTurnDispatchReleaseCommandID(firstClaim.ID, TaskTurnDispatchReleasedIdempotentRetry)
	firstRelease, err := NewTaskTurnDispatchRelease(job, firstClaim.ID, principalID, retryCommand,
		TaskTurnDispatchReleasedIdempotentRetry, strings.Repeat("a", 64), now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("release first claim for idempotent retry: %v", err)
	}
	job.TaskTurnDispatchReleases = append(job.TaskTurnDispatchReleases, firstRelease)
	job.Revision++
	job.UpdatedAt = firstRelease.CreatedAt
	secondClaim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_21212121212121212121212121212121", now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("New replacement dispatch claim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, secondClaim)
	job.Revision++
	job.UpdatedAt = secondClaim.CreatedAt
	failureCommand := TaskTurnDispatchReleaseCommandID(secondClaim.ID, TaskTurnDispatchReleasedDefinitiveFailure)
	if _, err := NewTaskTurnDispatchRelease(job, secondClaim.ID, principalID, failureCommand,
		TaskTurnDispatchReleasedDefinitiveFailure, "", now.Add(5*time.Minute)); err != nil {
		t.Fatalf("definitive pre-send failure of replacement claim was blocked by older UNKNOWN: %v", err)
	}
}

func TestTaskCoderResumeCanUseAcceptedStartBeforeFirstTurn(t *testing.T) {
	job, execution, _, now := taskTurnFixture(t)
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	if len(job.TaskTurnIntents) != 0 {
		t.Fatalf("fixture unexpectedly contains Task Turns: %d", len(job.TaskTurnIntents))
	}
	if _, err := NewPlanningResumeIntent(job, execution.ID, principalID, "session_task",
		"cmd_22222222222222222222222222222222", strings.Repeat("c", 64), true, now.Add(time.Minute)); err != nil {
		t.Fatalf("accepted Task Coder Start should be resumable before the first Turn: %v", err)
	}
}

func TestTaskCoderResumeObservationRequiresProviderResponsePath(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	principalID := job.IntakeProvenance.PrincipalID
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_e1010101010101010101010101010101", strings.Repeat("c", 64), 50, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt

	resume, err := NewPlanningResumeIntent(job, execution.ID, principalID, "session_task",
		"cmd_e2020202020202020202020202020202", strings.Repeat("d", 64), true, now.Add(time.Second))
	if err != nil {
		t.Fatalf("NewPlanningResumeIntent: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, resume)
	job.Revision++
	job.UpdatedAt = resume.CreatedAt

	claimCommandID := PlanningResumeDispatchClaimCommandID(resume.ID)
	claim, err := NewPlanningResumeDispatchClaim(
		job, resume.ID, principalID, claimCommandID, now.Add(1500*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewPlanningResumeDispatchClaim: %v", err)
	}
	job.PlanningResumeDispatchClaims = append(job.PlanningResumeDispatchClaims, claim)
	job.Revision++
	job.UpdatedAt = claim.CreatedAt
	if _, err := NewPlanningResumeIntent(job, execution.ID, principalID, "session_task",
		"cmd_f5050505050505050505050505050505", strings.Repeat("f", 64), true, now.Add(1600*time.Millisecond)); err == nil {
		t.Fatal("a later Resume intent was accepted while the Session dispatch claim was active")
	}

	evidence := strings.Repeat("e", 64)
	if _, err := NewPlanningResumeObservation(job, resume.ID, principalID,
		"cmd_e3030303030303030303030303030303", PlanningResumeAttached, evidence, false, now.Add(2*time.Second)); err == nil {
		t.Fatal("generic Planning Resume path accepted caller-supplied Coder ATTACHED evidence")
	}

	response := ProviderResumeResponse{
		OperationID: resume.OperationID, SessionID: resume.ProviderSessionID,
		Attached: true, EvidenceSHA256: evidence,
	}
	observation, err := NewTaskProviderResumeObservation(job, resume.ID,
		"cmd_e4040404040404040404040404040404", PlanningResumeAttached, evidence,
		response, false, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("NewTaskProviderResumeObservation: %v", err)
	}
	if observation.ProviderResponse == nil || !observation.ProviderResponse.Attached ||
		observation.ProviderResponse.OperationID != resume.OperationID ||
		observation.ProviderResponse.SessionID != resume.ProviderSessionID {
		t.Fatalf("Provider Resume response was not bound to observation: %#v", observation)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, observation)
	job.Revision++
	job.UpdatedAt = observation.CreatedAt
	if _, err := NewPlanningResumeIntent(job, execution.ID, principalID, "session_task",
		"cmd_f5050505050505050505050505050505", strings.Repeat("f", 64), true, now.Add(3*time.Second)); err == nil {
		t.Fatal("a later Resume intent was accepted while the previous Session dispatch claim remained active")
	}
}
