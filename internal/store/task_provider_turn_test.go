package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func TestTaskCoderTurnResumeAndStopFactsPersistAcrossRestart(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_task_turn_store"

	planner, _, sourceResult := createJobContractPlanner(t, database, jobID, principal)
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before contract: %v", err)
	}
	contract := validStoreJobContract()
	_, revision, err := database.AddJobContractRevision(ctx, jobID, job.Revision,
		"cmd_e1010101010101010101010101010101", planner.ID,
		sourceResult.ID, sourceResult.ContentSHA256, contract)
	if err != nil {
		t.Fatalf("AddJobContractRevision: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.AddJobApproval(ctx, jobID, job.Revision,
		"cmd_e2020202020202020202020202020202", revision.Revision,
		revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve Task execution", principal); err != nil {
		t.Fatalf("AddJobApproval: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, task, err := database.AddTask(ctx, job.Revision, domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_e3030303030303030303030303030303",
		Title: "Task Turn store fixture", Purpose: "Persist Task-bound Turn facts",
		AcceptanceCriteria: []string{"Turn facts restore with permission binding"},
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Round(0)
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_e4040404040404040404040404040404",
		Role: domain.RoleCoder, Effect: "repository.write", Resource: contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: contract.ConfigurationSHA256,
		ReservedTurns:       2, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	requestSHA, err := domain.ExecutionPermissionRequestSHA256(request)
	if err != nil {
		t.Fatal(err)
	}
	assessment := domain.ExecutionEffectAssessment{
		Role: request.Role, Effect: request.Effect, Resource: request.Resource,
		ProviderID: request.ProviderID, AccountID: request.AccountID,
		ConfigurationSHA256: request.ConfigurationSHA256, RequestSHA256: requestSHA,
		Reversible: true, Mutating: true, ValidUntil: request.ExpiresAt.Add(time.Minute),
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	_, permission, err := database.AddExecutionPermission(ctx, job.Revision, request, requestSHA, assessment)
	if err != nil {
		t.Fatalf("AddExecutionPermission: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	tempRoot := t.TempDir()
	snapshot := domain.WorktreeSnapshot{
		RepositoryID: task.TargetRepositories[0], RepositoryPath: filepath.Join(tempRoot, "source"),
		WorktreePath: filepath.Join(tempRoot, "worktree"), BaselineCommit: strings.Repeat("a", 40),
	}
	_, start, err := database.StartTaskExecution(ctx, job.Revision, jobID, task.ID,
		permission.ID, "cmd_e5050505050505050505050505050505", requestSHA, snapshot)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.AddTaskProviderStartObservation(ctx, jobID, job.Revision,
		start.ProviderStartIntent.ID, "cmd_e6060606060606060606060606060606",
		domain.ProviderStartAccepted, "session_1", "receipt_task_store",
		strings.Repeat("b", 64), false); err != nil {
		t.Fatalf("AddTaskProviderStartObservation: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	turnCommand := "cmd_e7070707070707070707070707070707"
	receipt, turn, err := database.AddTaskTurnIntent(ctx, jobID, job.Revision,
		start.RoleExecution.ID, permission.ID, "session_1",
		turnCommand, strings.Repeat("c", 64), 80)
	if err != nil {
		t.Fatalf("AddTaskTurnIntent: %v", err)
	}
	if receipt.Revision != job.Revision+1 || turn.TurnNumber != 1 {
		t.Fatalf("invalid Task Turn receipt: %#v %#v", receipt, turn)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, turnClaim, _, err := database.AddTaskTurnDispatchClaim(
		ctx, jobID, job.Revision, turn.ID, "cmd_e8181818181818181818181818181818", principal,
	)
	if err != nil {
		t.Fatalf("AddTaskTurnDispatchClaim: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = database.AddTaskTurnDispatchObservation(
		ctx, jobID, job.Revision, turnClaim.ID,
		domain.TaskTurnDispatchObservationCommandID(turnClaim.ID, domain.TaskTurnDispatchObservationStarted),
		domain.TaskTurnDispatchObservationStarted, "", principal,
	)
	if err != nil {
		t.Fatalf("AddTaskTurnDispatchObservation: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	observationReceipt, unknown, err := database.AddTaskTurnObservationForClaim(ctx, jobID, job.Revision,
		turn.ID, turnClaim.ID, "cmd_e8080808080808080808080808080808",
		domain.TaskTurnUnknown, 0, "", false)
	if err != nil {
		t.Fatalf("AddTaskTurnObservation: %v", err)
	}
	if observationReceipt.Revision != job.Revision+1 || unknown.Status != domain.TaskTurnUnknown {
		t.Fatalf("invalid UNKNOWN observation receipt: %#v %#v", observationReceipt, unknown)
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, resume, err := database.AddPlanningResumeIntent(ctx, jobID, job.Revision,
		start.RoleExecution.ID, "session_1", "cmd_e9090909090909090909090909090909",
		strings.Repeat("f", 64), true, principal)
	if err != nil {
		t.Fatalf("AddPlanningResumeIntent for Task Coder: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	resumeClaimCommand := domain.PlanningResumeDispatchClaimCommandID(resume.ID)
	_, resumeClaim, _, err := database.AddPlanningResumeDispatchClaim(
		ctx, jobID, job.Revision, resume.ID, resumeClaimCommand, principal,
	)
	if err != nil {
		t.Fatalf("AddPlanningResumeDispatchClaim: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, resumeUnknown, err := database.AddTaskProviderResumeObservation(ctx, jobID, job.Revision,
		resume.ID, "cmd_ea0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a",
		domain.PlanningResumeUnknown, "", domain.ProviderResumeResponse{}, false)
	if err != nil || resumeUnknown.Status != domain.PlanningResumeUnknown {
		t.Fatalf("persist Task Coder Resume UNKNOWN: %#v %v", resumeUnknown, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	resumeEvidence := strings.Repeat("1", 64)
	resumeResponse := domain.ProviderResumeResponse{
		OperationID: resume.OperationID, SessionID: resume.ProviderSessionID,
		Attached: true, EvidenceSHA256: resumeEvidence,
	}
	_, resumeAttached, err := database.AddTaskProviderResumeObservation(ctx, jobID, job.Revision,
		resume.ID, "cmd_eb0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b",
		domain.PlanningResumeAttached, resumeEvidence, resumeResponse, true)
	if err != nil || resumeAttached.Status != domain.PlanningResumeAttached || !resumeAttached.ReconcilesUnknown {
		t.Fatalf("reconcile Task Coder Resume: %#v %v", resumeAttached, err)
	}
	if _, _, err := database.ReleasePlanningResumeDispatchClaim(
		ctx, jobID, resumeClaim.ID, principal, domain.PlanningResumeDispatchReleasedAttached,
	); err != nil {
		t.Fatalf("ReleasePlanningResumeDispatchClaim: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	content := "completed Task Coder result"
	contentSum := sha256.Sum256([]byte(content))
	contentSHA := hex.EncodeToString(contentSum[:])
	_, result, err := database.AddProviderResultObservation(ctx, jobID, job.Revision,
		start.RoleExecution.ID, turn.OperationID, turn.ID, "session_1", content, contentSHA, true, strings.Repeat("6", 64), principal)
	if err != nil || !result.Final {
		t.Fatalf("persist Task Coder final Result: %#v %v", result, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, usage, err := database.AddProviderUsageObservation(ctx, jobID, job.Revision,
		start.RoleExecution.ID, turn.OperationID, turn.ID, "session_1", "usage_turn_1", 12, true, strings.Repeat("7", 64), principal)
	if err != nil || !usage.Complete {
		t.Fatalf("persist Task Coder complete Usage: %#v %v", usage, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, completedTurn, err := database.AddTaskTurnObservationForClaim(ctx, jobID, job.Revision,
		turn.ID, turnClaim.ID, "cmd_ec0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c",
		domain.TaskTurnCompleted, 12, strings.Repeat("6", 64), true)
	if err != nil || completedTurn.Status != domain.TaskTurnCompleted {
		t.Fatalf("reconcile Task Turn with final Result / Usage: %#v %v", completedTurn, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.ReleaseTaskTurnDispatchClaim(
		ctx, jobID, turnClaim.ID, principal, domain.TaskTurnDispatchReleasedCompleted, "",
	); err != nil {
		t.Fatalf("ReleaseTaskTurnDispatchClaim: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, stopIntent, err := database.AddProviderStopIntent(ctx, jobID, job.Revision,
		start.RoleExecution.ID, "session_1", "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		domain.ProviderStopReasonTaskComplete, principal)
	if err != nil {
		t.Fatalf("AddProviderStopIntent for Task Coder: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, stopUnknown, err := database.AddProviderStopObservation(ctx, jobID, job.Revision,
		stopIntent.ID, "cmd_ed0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d",
		false, false, "", false, principal)
	if err != nil || stopUnknown.Stopped || stopUnknown.Quiescent {
		t.Fatalf("persist Task Coder Stop UNKNOWN: %#v %v", stopUnknown, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	_, stopConfirmed, err := database.AddProviderStopObservation(ctx, jobID, job.Revision,
		stopIntent.ID, "cmd_ee0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e",
		true, true, strings.Repeat("2", 64), true, principal)
	if err != nil || !stopConfirmed.Stopped || !stopConfirmed.Quiescent || !stopConfirmed.ReconcilesUnknown {
		t.Fatalf("reconcile Task Coder Stop: %#v %v", stopConfirmed, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	outputReceipt, output, err := database.AddJobOutput(ctx, jobID, job.Revision, task.ID,
		start.RoleExecution.ID, turn.ID, strings.Repeat("9", 40), strings.Repeat("8", 40))
	if err != nil || outputReceipt.Revision != job.Revision+1 ||
		output.CommitSHA != strings.Repeat("9", 40) || output.TreeSHA != strings.Repeat("8", 40) {
		t.Fatalf("persist immutable JobOutput: %#v %#v %v", outputReceipt, output, err)
	}
	replayReceipt, replayOutput, err := database.AddJobOutput(ctx, jobID, job.Revision, task.ID,
		start.RoleExecution.ID, turn.ID, strings.Repeat("9", 40), strings.Repeat("8", 40))
	if err != nil || replayReceipt != outputReceipt || replayOutput != output {
		t.Fatalf("exact JobOutput replay changed immutable fact: %#v %#v %v", replayReceipt, replayOutput, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
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
	if len(restored.TaskTurnIntents) != 1 || len(restored.TaskTurnObservations) != 2 || len(restored.ProviderResultObservations) != 2 || len(restored.ProviderUsageObservations) != 1 || len(restored.JobOutputs) != 1 ||
		len(restored.PlanningResumeIntents) != 1 || len(restored.PlanningResumeObservations) != 2 ||
		len(restored.ProviderStopIntents) != 1 || len(restored.ProviderStopObservations) != 2 ||
		restored.TaskTurnIntents[0].ID != turn.ID ||
		restored.TaskTurnObservations[0].Status != domain.TaskTurnUnknown || restored.TaskTurnObservations[1].Status != domain.TaskTurnCompleted || restored.JobOutputs[0].CommitSHA != strings.Repeat("9", 40) ||
		restored.PlanningResumeIntents[0].ID != resume.ID ||
		restored.PlanningResumeObservations[1].Status != domain.PlanningResumeAttached ||
		!restored.PlanningResumeObservations[1].ReconcilesUnknown ||
		restored.ProviderStopIntents[0].ID != stopIntent.ID ||
		!restored.ProviderStopObservations[1].Stopped || !restored.ProviderStopObservations[1].Quiescent ||
		!restored.ProviderStopObservations[1].ReconcilesUnknown {
		t.Fatalf("Task Coder Turn / Resume / Stop facts were not restored: %#v", restored)
	}
	if err := restored.Validate(); err != nil {
		t.Fatalf("restored aggregate validation: %v", err)
	}
}
