package domain

import (
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
	snapshot := WorktreeSnapshot{
		RepositoryID: task.TargetRepositories[0], RepositoryPath: "/tmp/taxion-src",
		WorktreePath: "/tmp/taxion-wt", BaselineCommit: strings.Repeat("a", 40),
	}
	start, err := NewTaskRoleExecutionStart(job, task.ID, permission.ID,
		"cmd_d1010101010101010101010101010101", digest, snapshot, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	job.WorktreeAssignments = append(job.WorktreeAssignments, start.WorktreeAssignment)
	job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
	job.WorktreeLifecycleEnforcedFromRevision = start.WorktreeAssignment.CreatedRevision
	job.ProviderLifecycleEnforcedFromRevision = start.ProviderStartIntent.CreatedRevision
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
	usage, err := NewProviderUsageObservation(job, execution.ID, principalID, turn.OperationID, turn.ID,
		turn.ProviderSessionID, "usage_task_turn", 75, true, strings.Repeat("7", 64), now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("create complete Task Usage: %v", err)
	}
	job.ProviderUsageObservations = append(job.ProviderUsageObservations, usage)
	job.Revision++
	job.UpdatedAt = usage.CreatedAt
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
}
