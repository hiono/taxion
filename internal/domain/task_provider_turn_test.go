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

func appendTaskTurnDispatchObservationForTest(
	t *testing.T,
	job *Job,
	claim TaskTurnDispatchClaim,
	status, evidenceSHA256 string,
	createdAt time.Time,
) TaskTurnDispatchObservation {
	t.Helper()
	commandID := TaskTurnDispatchObservationCommandID(claim.ID, status, evidenceSHA256)
	observation, err := NewTaskTurnDispatchObservation(
		*job, claim.ID, claim.PrincipalID, commandID, status, evidenceSHA256, createdAt,
	)
	if err != nil {
		t.Fatalf("NewTaskTurnDispatchObservation(%s): %v", status, err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, observation)
	job.Revision++
	job.UpdatedAt = observation.CreatedAt
	return observation
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
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	claim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_d8181818181818181818181818181818", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnDispatchClaim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claim)
	job.Revision++
	job.UpdatedAt = claim.CreatedAt
	appendTaskTurnDispatchObservationForTest(t, &job, claim, TaskTurnDispatchObservationStarted, "", now.Add(70*time.Second))
	unknown, err := NewTaskTurnObservationForClaim(job, turn.ID, claim.ID, "cmd_d8080808080808080808080808080808",
		TaskTurnUnknown, 0, "", false, now.Add(80*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	returnedEvidence := strings.Repeat("9", 64)
	returnedUnknown, err := NewTaskTurnDispatchObservation(
		job, claim.ID, principalID,
		TaskTurnDispatchObservationCommandID(claim.ID, TaskTurnDispatchObservationUnknownReturned, returnedEvidence),
		TaskTurnDispatchObservationUnknownReturned, returnedEvidence, now.Add(80*time.Second+time.Second),
	)
	if err != nil {
		t.Fatalf("Task Turn returned UNKNOWN: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, returnedUnknown)
	job.Revision++
	job.UpdatedAt = returnedUnknown.CreatedAt
	resume, err := NewPlanningResumeIntent(
		job, execution.ID, principalID, "session_task",
		"cmd_d9191919191919191919191919191919", strings.Repeat("4", 64), true, now.Add(90*time.Second),
	)
	if err != nil || resume.ProviderSessionID != turn.ProviderSessionID {
		t.Fatalf("Resume for an UNKNOWN Task Turn did not preserve the accepted Session: resume=%#v err=%v", resume, err)
	}
	if _, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_d9090909090909090909090909090909", strings.Repeat("2", 64), 1, now.Add(2*time.Minute)); err == nil {
		t.Fatal("new Turn started while prior Turn UNKNOWN")
	}
	if _, err := NewTaskTurnObservationForClaim(job, turn.ID, claim.ID, "cmd_da0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
		TaskTurnCompleted, 75, strings.Repeat("3", 64), true, now.Add(2*time.Minute)); err == nil {
		t.Fatal("terminal Task Turn was accepted without final Result and complete Usage")
	}
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
	completed, err := NewTaskTurnObservationForClaim(job, turn.ID, claim.ID, "cmd_da0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
		TaskTurnCompleted, 75, strings.Repeat("6", 64), true, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, completed)
	job.Revision++
	job.UpdatedAt = completed.CreatedAt
	releaseCommand := TaskTurnDispatchReleaseCommandID(claim.ID, TaskTurnDispatchReleasedCompleted)
	release, err := NewTaskTurnDispatchRelease(job, claim.ID, principalID, releaseCommand,
		TaskTurnDispatchReleasedCompleted, "", "", now.Add(4*time.Minute+30*time.Second))
	if err != nil {
		t.Fatalf("release completed Task Turn dispatch claim: %v", err)
	}
	job.TaskTurnDispatchReleases = append(job.TaskTurnDispatchReleases, release)
	job.Revision++
	job.UpdatedAt = release.CreatedAt
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
	claim, err := NewTaskTurnDispatchClaim(job, first.ID, principalID,
		"cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("dispatch claim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claim)
	job.Revision++
	job.UpdatedAt = claim.CreatedAt
	appendTaskTurnDispatchObservationForTest(t, &job, claim, TaskTurnDispatchObservationStarted, "", now.Add(45*time.Second))
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

	terminal, err := NewTaskTurnObservationForClaim(job, first.ID, claim.ID,
		"cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", TaskTurnCompleted, 10,
		strings.Repeat("e", 64), false, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("terminal observation: %v", err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, terminal)
	job.Revision++
	job.UpdatedAt = terminal.CreatedAt
	releaseCommand := TaskTurnDispatchReleaseCommandID(claim.ID, TaskTurnDispatchReleasedCompleted)
	release, err := NewTaskTurnDispatchRelease(job, claim.ID, principalID, releaseCommand,
		TaskTurnDispatchReleasedCompleted, "", "", now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("release completed dispatch claim: %v", err)
	}
	job.TaskTurnDispatchReleases = append(job.TaskTurnDispatchReleases, release)
	job.Revision++
	job.UpdatedAt = release.CreatedAt

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
	appendTaskTurnDispatchObservationForTest(t, &job, claim, TaskTurnDispatchObservationStarted, "", now.Add(90*time.Second))
	unknown, err := NewTaskTurnObservationForClaim(job, turn.ID, claim.ID,
		"cmd_15151515151515151515151515151515", TaskTurnUnknown, 0, "", false, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnObservation UNKNOWN: %v", err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	failureEvidence := strings.Repeat("f", 64)
	appendTaskTurnDispatchObservationForTest(t, &job, claim, TaskTurnDispatchObservationDefinitiveFailure, failureEvidence, now.Add(150*time.Second))
	commandID := TaskTurnDispatchReleaseCommandID(claim.ID, TaskTurnDispatchReleasedDefinitiveFailure)
	if _, err := NewTaskTurnDispatchRelease(job, claim.ID, principalID, commandID,
		TaskTurnDispatchReleasedDefinitiveFailure, failureEvidence, "", now.Add(3*time.Minute)); err == nil {
		t.Fatal("UNKNOWN Task Turn claim was released as definitive_failure despite a prior ambiguous Provider operation")
	}

	control, controlExecution, controlPermission, controlNow := taskTurnFixture(t)
	controlTurn, err := NewTaskTurnIntent(control, controlExecution.ID, controlPermission.ID, "session_task",
		"cmd_16161616161616161616161616161616", strings.Repeat("2", 64), 40, controlNow)
	if err != nil {
		t.Fatalf("control NewTaskTurnIntent: %v", err)
	}
	control.TaskTurnIntents = append(control.TaskTurnIntents, controlTurn)
	control.Revision++
	control.UpdatedAt = controlTurn.CreatedAt
	controlPrincipal := control.JobApprovals[len(control.JobApprovals)-1].ApproverPrincipalID
	controlClaim, err := NewTaskTurnDispatchClaim(control, controlTurn.ID, controlPrincipal,
		"cmd_17171717171717171717171717171717", controlNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("control NewTaskTurnDispatchClaim: %v", err)
	}
	control.TaskTurnDispatchClaims = append(control.TaskTurnDispatchClaims, controlClaim)
	control.Revision++
	control.UpdatedAt = controlClaim.CreatedAt
	appendTaskTurnDispatchObservationForTest(t, &control, controlClaim, TaskTurnDispatchObservationStarted, "", controlNow.Add(90*time.Second))
	controlFailureEvidence := strings.Repeat("e", 64)
	appendTaskTurnDispatchObservationForTest(t, &control, controlClaim, TaskTurnDispatchObservationDefinitiveFailure, controlFailureEvidence, controlNow.Add(2*time.Minute))
	controlCommandID := TaskTurnDispatchReleaseCommandID(controlClaim.ID, TaskTurnDispatchReleasedDefinitiveFailure)
	if _, err := NewTaskTurnDispatchRelease(control, controlClaim.ID, controlPrincipal, controlCommandID,
		TaskTurnDispatchReleasedDefinitiveFailure, controlFailureEvidence, "", controlNow.Add(3*time.Minute)); err != nil {
		t.Fatalf("definitive failure without UNKNOWN was rejected: %v", err)
	}
}

func TestProviderStartDispatchBoundaryRequiresUnexpiredPermission(t *testing.T) {
	job, execution, _, now := taskTurnFixture(t)
	observations := job.ProviderStartObservations[:0]
	for _, observation := range job.ProviderStartObservations {
		if observation.OperationID != execution.ProviderStartOperationID {
			observations = append(observations, observation)
		}
	}
	job.ProviderStartObservations = observations
	if err := job.Validate(); err != nil {
		t.Fatalf("Job without Start observation: %v", err)
	}
	commandID := "cmd_61616161616161616161616161616161"
	valid, err := NewProviderStartObservation(job, execution.ProviderStartOperationID, "",
		commandID, ProviderStartDispatching, "", "", "", false, now)
	if err != nil {
		t.Fatalf("valid dispatch boundary: %v", err)
	}
	job.ProviderStartObservations = append(job.ProviderStartObservations, valid)
	job.Revision++
	job.UpdatedAt = valid.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with dispatch boundary: %v", err)
	}

	expiredJob, expiredExecution, expiredPermission, _ := taskTurnFixture(t)
	expiredJob.PlanningTurnIntents = nil
	expiredJob.PlanningTurnObservations = nil
	expiredJob.PlanningEffectDecisions = nil
	expiredJob.PlanningResumeIntents = nil
	expiredJob.PlanningResumeObservations = nil
	expiredJob.PlanningResumeDispatchClaims = nil
	expiredJob.PlanningResumeDispatchReleases = nil
	expiredJob.PlanningBoundaryDecisions = nil
	expiredObservations := expiredJob.ProviderStartObservations[:0]
	for _, observation := range expiredJob.ProviderStartObservations {
		if observation.OperationID != expiredExecution.ProviderStartOperationID {
			expiredObservations = append(expiredObservations, observation)
		}
	}
	expiredJob.ProviderStartObservations = expiredObservations
	if _, err := NewProviderStartObservation(expiredJob, expiredExecution.ProviderStartOperationID, "",
		"cmd_62626262626262626262626262626262", ProviderStartDispatching, "", "", "", false,
		expiredPermission.ExpiresAt.Add(time.Second)); err == nil {
		t.Fatal("expired ExecutionPermission crossed the Provider Start dispatch boundary")
	}
}

func TestTaskTurnDispatchStartUsesResumeStateAtItsCreatedRevision(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_63636363636363636363636363636363", strings.Repeat("6", 64), 40, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	claim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_64646464646464646464646464646464", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnDispatchClaim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claim)
	job.Revision++
	job.UpdatedAt = claim.CreatedAt
	started, err := NewTaskTurnDispatchObservation(job, claim.ID, principalID,
		TaskTurnDispatchObservationCommandID(claim.ID, TaskTurnDispatchObservationStarted, ""),
		TaskTurnDispatchObservationStarted, "", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnDispatchObservation STARTED: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, started)
	job.Revision++
	job.UpdatedAt = started.CreatedAt

	// A later Resume intent must not retroactively invalidate the earlier dispatch fact.
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, PlanningResumeIntent{
		ID: "resume_later", RoleExecutionID: execution.ID, CreatedRevision: job.Revision + 1,
	})
	if err := started.Validate(job); err != nil {
		t.Fatalf("historical dispatch observation was invalidated by a later Resume intent: %v", err)
	}
}

func TestTaskTurnDispatchClaimRequiresLatestResumeAttached(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_31313131313131313131313131313131", strings.Repeat("3", 64), 40, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	resume, err := NewPlanningResumeIntent(job, execution.ID, principalID, "session_task",
		"cmd_32323232323232323232323232323232", strings.Repeat("4", 64), true, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewPlanningResumeIntent: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, resume)
	job.Revision++
	job.UpdatedAt = resume.CreatedAt
	if _, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_33333333333333333333333333333333", now.Add(2*time.Minute)); err == nil {
		t.Fatal("Task Turn acquired a dispatch claim while the latest Resume was unresolved")
	}
}

func TestPlanningResumeIntentRejectsActiveTaskTurnDispatch(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_41414141414141414141414141414141", strings.Repeat("5", 64), 40, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	claim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_42424242424242424242424242424242", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnDispatchClaim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claim)
	job.Revision++
	job.UpdatedAt = claim.CreatedAt
	if _, err := NewPlanningResumeIntent(job, execution.ID, principalID, "session_task",
		"cmd_43434343434343434343434343434343", strings.Repeat("6", 64), true, now.Add(2*time.Minute)); err == nil {
		t.Fatal("Resume intent was created while a Task Turn dispatch claim was active")
	}
}

func TestPlanningResumeIntentRejectsRetryDispatchAfterPriorUnknown(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_71717171717171717171717171717171", strings.Repeat("7", 64), 40, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID

	firstClaim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_72727272727272727272727272727272", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("first dispatch claim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, firstClaim)
	job.Revision++
	job.UpdatedAt = firstClaim.CreatedAt
	firstStarted, err := NewTaskTurnDispatchObservation(job, firstClaim.ID, principalID,
		TaskTurnDispatchObservationCommandID(firstClaim.ID, TaskTurnDispatchObservationStarted, ""),
		TaskTurnDispatchObservationStarted, "", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("first dispatch STARTED: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, firstStarted)
	job.Revision++
	job.UpdatedAt = firstStarted.CreatedAt
	unknown, err := NewTaskTurnObservationForClaim(job, turn.ID, firstClaim.ID,
		"cmd_73737373737373737373737373737373", TaskTurnUnknown, 0, "", false, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("first dispatch UNKNOWN: %v", err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	lookupEvidence := strings.Repeat("8", 64)
	lookup, err := NewTaskTurnDispatchObservation(job, firstClaim.ID, principalID,
		TaskTurnDispatchObservationCommandID(firstClaim.ID, TaskTurnDispatchObservationLookupNotFound, lookupEvidence),
		TaskTurnDispatchObservationLookupNotFound, lookupEvidence, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("first dispatch Lookup NOT_FOUND: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, lookup)
	job.Revision++
	job.UpdatedAt = lookup.CreatedAt
	release, err := NewTaskTurnDispatchRelease(job, firstClaim.ID, principalID,
		"cmd_74747474747474747474747474747474", TaskTurnDispatchReleasedIdempotentRetry,
		strings.Repeat("9", 64), lookupEvidence, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("release first claim for idempotent retry: %v", err)
	}
	job.TaskTurnDispatchReleases = append(job.TaskTurnDispatchReleases, release)
	job.Revision++
	job.UpdatedAt = release.CreatedAt

	retryClaim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_75757575757575757575757575757575", now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("retry dispatch claim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, retryClaim)
	job.Revision++
	job.UpdatedAt = retryClaim.CreatedAt
	retryStarted, err := NewTaskTurnDispatchObservation(job, retryClaim.ID, principalID,
		TaskTurnDispatchObservationCommandID(retryClaim.ID, TaskTurnDispatchObservationStarted, ""),
		TaskTurnDispatchObservationStarted, "", now.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("retry dispatch STARTED: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, retryStarted)
	job.Revision++
	job.UpdatedAt = retryStarted.CreatedAt

	if _, err := NewPlanningResumeIntent(job, execution.ID, principalID, "session_task", "cmd_76767676767676767676767676767676", strings.Repeat("a", 64), true, now.Add(8*time.Minute)); err == nil || !strings.Contains(err.Error(), "task_turn_dispatch_in_progress") {
		t.Fatalf("Resume intent during a retry dispatch = %v, want task_turn_dispatch_in_progress", err)
	}
}

func TestPlanningResumeIntentAllowsRecoveryOfUnknownTaskTurn(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_65656565656565656565656565656565", strings.Repeat("7", 64), 40, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	claim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_66666666666666666666666666666666", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnDispatchClaim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claim)
	job.Revision++
	job.UpdatedAt = claim.CreatedAt
	started, err := NewTaskTurnDispatchObservation(job, claim.ID, principalID,
		TaskTurnDispatchObservationCommandID(claim.ID, TaskTurnDispatchObservationStarted, ""),
		TaskTurnDispatchObservationStarted, "", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Task Turn dispatch STARTED: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, started)
	job.Revision++
	job.UpdatedAt = started.CreatedAt
	unknown, err := NewTaskTurnObservationForClaim(job, turn.ID, claim.ID,
		"cmd_67676767676767676767676767676767", TaskTurnUnknown, 0, "", false, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Task Turn UNKNOWN: %v", err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	returnedEvidence := strings.Repeat("9", 64)
	returnedUnknown, err := NewTaskTurnDispatchObservation(
		job, claim.ID, principalID,
		TaskTurnDispatchObservationCommandID(claim.ID, TaskTurnDispatchObservationUnknownReturned, returnedEvidence),
		TaskTurnDispatchObservationUnknownReturned, returnedEvidence, now.Add(3*time.Minute+30*time.Second),
	)
	if err != nil {
		t.Fatalf("Task Turn returned UNKNOWN: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, returnedUnknown)
	job.Revision++
	job.UpdatedAt = returnedUnknown.CreatedAt
	resume, err := NewPlanningResumeIntent(job, execution.ID, principalID, "session_task",
		"cmd_68686868686868686868686868686868", strings.Repeat("8", 64), true, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("Resume for UNKNOWN Task Turn recovery: %v", err)
	}
	if resume.ProviderSessionID != turn.ProviderSessionID {
		t.Fatalf("Resume changed the Provider Session: %#v", resume)
	}
}

func TestTaskTurnDispatchAllowsMultipleLookupEvidenceAndDefinitiveFailure(t *testing.T) {
	job, execution, permission, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, permission.ID, "session_task",
		"cmd_51515151515151515151515151515151", strings.Repeat("7", 64), 40, now)
	if err != nil {
		t.Fatalf("NewTaskTurnIntent: %v", err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	claim, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_52525252525252525252525252525252", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTaskTurnDispatchClaim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claim)
	job.Revision++
	job.UpdatedAt = claim.CreatedAt
	appendTaskTurnDispatchObservationForTest(t, &job, claim, TaskTurnDispatchObservationStarted, "", now.Add(90*time.Second))
	firstLookup, err := NewTaskTurnDispatchObservation(job, claim.ID, principalID,
		"cmd_53535353535353535353535353535353", TaskTurnDispatchObservationLookupNotFound, strings.Repeat("a", 64), now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("first Lookup NOT_FOUND: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, firstLookup)
	job.Revision++
	job.UpdatedAt = firstLookup.CreatedAt
	secondLookup, err := NewTaskTurnDispatchObservation(job, claim.ID, principalID,
		"cmd_54545454545454545454545454545454", TaskTurnDispatchObservationLookupNotFound, strings.Repeat("b", 64), now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("second Lookup NOT_FOUND with refreshed evidence: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, secondLookup)
	job.Revision++
	job.UpdatedAt = secondLookup.CreatedAt
	failureEvidence := strings.Repeat("c", 64)
	failure, err := NewTaskTurnDispatchObservation(job, claim.ID, principalID,
		"cmd_55555555555555555555555555555555", TaskTurnDispatchObservationDefinitiveFailure, failureEvidence, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("definitive failure after Lookup NOT_FOUND: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, failure)
	job.Revision++
	job.UpdatedAt = failure.CreatedAt
	releaseID := TaskTurnDispatchReleaseCommandID(claim.ID, TaskTurnDispatchReleasedDefinitiveFailure)
	if _, err := NewTaskTurnDispatchRelease(job, claim.ID, principalID, releaseID,
		TaskTurnDispatchReleasedDefinitiveFailure, failureEvidence, "", now.Add(5*time.Minute)); err != nil {
		t.Fatalf("definitive failure release after prior Lookup NOT_FOUND: %v", err)
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
	appendTaskTurnDispatchObservationForTest(t, &job, firstClaim, TaskTurnDispatchObservationStarted, "", now.Add(90*time.Second))
	unknown, err := NewTaskTurnObservationForClaim(job, turn.ID, firstClaim.ID,
		"cmd_20202020202020202020202020202020", TaskTurnUnknown, 0, "", false, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("New Task Turn UNKNOWN: %v", err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	lookupEvidence := strings.Repeat("a", 64)
	appendTaskTurnDispatchObservationForTest(t, &job, firstClaim, TaskTurnDispatchObservationLookupNotFound, lookupEvidence, now.Add(3*time.Minute))
	retryCommand := TaskTurnDispatchReleaseCommandID(firstClaim.ID, TaskTurnDispatchReleasedIdempotentRetry)
	idempotencyEvidence := strings.Repeat("b", 64)
	firstRelease, err := NewTaskTurnDispatchRelease(job, firstClaim.ID, principalID, retryCommand,
		TaskTurnDispatchReleasedIdempotentRetry, idempotencyEvidence, lookupEvidence, now.Add(3*time.Minute+30*time.Second))
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
	failureEvidence := strings.Repeat("b", 64)
	appendTaskTurnDispatchObservationForTest(t, &job, secondClaim, TaskTurnDispatchObservationPreDispatchFailure, failureEvidence, now.Add(4*time.Minute+30*time.Second))
	failureCommand := TaskTurnDispatchReleaseCommandID(secondClaim.ID, TaskTurnDispatchReleasedDefinitiveFailure)
	if _, err := NewTaskTurnDispatchRelease(job, secondClaim.ID, principalID, failureCommand,
		TaskTurnDispatchReleasedDefinitiveFailure, failureEvidence, "", now.Add(5*time.Minute)); err != nil {
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

func TestProviderStartHasDispatchBoundaryDistinguishesLookupUnknown(t *testing.T) {
	job, execution, _, _ := taskTurnFixture(t)
	job.ProviderStartObservations = []ProviderStartObservation{
		{OperationID: execution.ProviderStartOperationID, Status: ProviderStartDispatching},
		{OperationID: execution.ProviderStartOperationID, Status: ProviderStartUnknown},
	}
	if !providerStartHasDispatchBoundary(job, execution.ProviderStartOperationID) {
		t.Fatal("UNKNOWN after a persisted dispatch boundary was treated as known-not-dispatched")
	}
	job.ProviderStartObservations = []ProviderStartObservation{
		{OperationID: execution.ProviderStartOperationID, Status: ProviderStartUnknown},
	}
	if providerStartHasDispatchBoundary(job, execution.ProviderStartOperationID) {
		t.Fatal("lookup UNKNOWN without a dispatch boundary was treated as an issued Start")
	}
}

func TestTaskTurnUnknownReturnedIsRecordedForRetryClaim(t *testing.T) {
	job, execution, _, now := taskTurnFixture(t)
	turn, err := NewTaskTurnIntent(job, execution.ID, execution.ExecutionPermissionID, "session_task",
		"cmd_d7171717171717171717171717171717", strings.Repeat("1", 64), 80, now)
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnIntents = append(job.TaskTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	principalID := job.JobApprovals[len(job.JobApprovals)-1].ApproverPrincipalID
	claimA, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_d8181818181818181818181818181818", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claimA)
	job.Revision++
	job.UpdatedAt = claimA.CreatedAt
	appendTaskTurnDispatchObservationForTest(t, &job, claimA, TaskTurnDispatchObservationStarted, "", now.Add(70*time.Second))
	unknown, err := NewTaskTurnObservationForClaim(job, turn.ID, claimA.ID,
		"cmd_d82828282828282828282828282828282", TaskTurnUnknown, 0, "", false, now.Add(80*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	job.TaskTurnObservations = append(job.TaskTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	lookupEvidence := strings.Repeat("8", 64)
	appendTaskTurnDispatchObservationForTest(t, &job, claimA, TaskTurnDispatchObservationLookupNotFound, lookupEvidence, now.Add(81*time.Second))
	releaseA, err := NewTaskTurnDispatchRelease(job, claimA.ID, principalID,
		TaskTurnDispatchReleaseCommandID(claimA.ID, TaskTurnDispatchReleasedIdempotentRetry),
		TaskTurnDispatchReleasedIdempotentRetry, strings.Repeat("7", 64), lookupEvidence, now.Add(82*time.Second))
	if err != nil {
		t.Fatalf("release first UNKNOWN claim for idempotent retry: %v", err)
	}
	job.TaskTurnDispatchReleases = append(job.TaskTurnDispatchReleases, releaseA)
	job.Revision++
	job.UpdatedAt = releaseA.CreatedAt
	claimB, err := NewTaskTurnDispatchClaim(job, turn.ID, principalID,
		"cmd_d8383838383838383838383838383838", now.Add(83*time.Second))
	if err != nil {
		t.Fatalf("create retry claim: %v", err)
	}
	job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claimB)
	job.Revision++
	job.UpdatedAt = claimB.CreatedAt
	appendTaskTurnDispatchObservationForTest(t, &job, claimB, TaskTurnDispatchObservationStarted, "", now.Add(84*time.Second))
	returnedEvidence := strings.Repeat("9", 64)
	returned, err := NewTaskTurnDispatchObservation(
		job, claimB.ID, principalID,
		TaskTurnDispatchObservationCommandID(claimB.ID, TaskTurnDispatchObservationUnknownReturned, returnedEvidence),
		TaskTurnDispatchObservationUnknownReturned, returnedEvidence, now.Add(85*time.Second),
	)
	if err != nil {
		t.Fatalf("record retry claim's returned UNKNOWN using the prior Turn observation: %v", err)
	}
	job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, returned)
	job.Revision++
	job.UpdatedAt = returned.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job with claim-specific UNKNOWN_RETURNED did not validate: %v", err)
	}
}
