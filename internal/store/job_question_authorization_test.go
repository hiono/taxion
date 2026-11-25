package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func TestJobQuestionAuthorizationStorePersistsAndReplays(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	planner, _, sourceResult := createJobContractPlanner(t, database, "job_question_authorization_store", principal)
	job, err := database.GetJob(ctx, "job_question_authorization_store")
	if err != nil {
		t.Fatalf("GetJob before JobContract revision: %v", err)
	}
	contract := validStoreJobContract()
	_, revision, err := database.AddJobContractRevision(
		ctx, job.ID, job.Revision, "cmd_81818181818181818181818181818181",
		planner.ID, sourceResult.ID, sourceResult.ContentSHA256, contract,
	)
	if err != nil {
		t.Fatalf("AddJobContractRevision: %v", err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob before approval: %v", err)
	}
	if _, _, err := database.AddJobApproval(
		ctx, job.ID, job.Revision, "cmd_82828282828282828282828282828282",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve bounded execution", principal,
	); err != nil {
		t.Fatalf("AddJobApproval: %v", err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob before Task: %v", err)
	}
	_, task, err := database.AddTask(ctx, job.Revision, domain.TaskRequest{
		JobID: job.ID, CommandID: "cmd_83838383838383838383838383838383",
		Title: "Question authorization task", Purpose: "Create a Role question for the Taskless Planner",
		AcceptanceCriteria: []string{"The question is bound to the approved Job"},
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob before ExecutionPermission: %v", err)
	}
	now := time.Now().UTC().Round(0)
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: job.ID, TaskID: task.ID, CommandID: "cmd_84848484848484848484848484848484",
		Role: "CODER", Effect: "repository.write", Resource: contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 10, ExpiresAt: now.Add(30 * time.Minute),
	}
	permissionSHA, err := domain.ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	assessment := domain.ExecutionEffectAssessment{
		Role: permissionRequest.Role, Effect: permissionRequest.Effect, Resource: permissionRequest.Resource,
		ProviderID: permissionRequest.ProviderID, AccountID: permissionRequest.AccountID,
		ConfigurationSHA256: permissionRequest.ConfigurationSHA256, RequestSHA256: permissionSHA,
		Reversible: true, Mutating: true, ValidUntil: permissionRequest.ExpiresAt.Add(5 * time.Minute),
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	_, permission, err := database.AddExecutionPermission(ctx, job.Revision, permissionRequest, permissionSHA, assessment)
	if err != nil {
		t.Fatalf("AddExecutionPermission: %v", err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob before Task execution: %v", err)
	}
	if _, _, err := database.StartTaskExecution(
		ctx, job.Revision, job.ID, task.ID, permission.ID,
		"cmd_85858585858585858585858585858585", permission.RequestSHA256,
	); err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob before QUESTION: %v", err)
	}
	var plannerStart domain.ProviderStartIntent
	var plannerSession string
	for _, start := range job.ProviderStartIntents {
		if start.RoleExecutionID == planner.ID {
			plannerStart = start
			break
		}
	}
	for _, observation := range job.ProviderStartObservations {
		if observation.OperationID == plannerStart.ID && observation.Status == domain.ProviderStartAccepted {
			plannerSession = observation.ProviderSessionID
		}
	}
	if plannerStart.ID == "" || plannerSession == "" {
		t.Fatal("Planner has no accepted Provider Session")
	}
	messageReceipt, message, err := database.AddJobMessage(ctx, job.ID, job.Revision, "cmd_86868686868686868686868686868686", domain.JobMessageRequest{
		Sender:    domain.ActorRef{Kind: domain.ActorCoder, ID: job.RoleExecutions[len(job.RoleExecutions)-1].ID},
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: planner.ID},
		Kind:      domain.MessageKindQuestion, Body: "Please clarify the approved implementation constraint.",
	})
	if err != nil {
		t.Fatalf("AddJobMessage: %v", err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob before JobQuestionAuthorization: %v", err)
	}
	var plannerGrant domain.PlanningAuthorization
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID == planner.PlanningAuthorizationID {
			plannerGrant = grant
			break
		}
	}
	request := domain.JobQuestionAuthorizationRequest{
		JobID: job.ID, CommandID: "cmd_87878787878787878787878787878787",
		QuestionMessageID: message.ID, PlannerRoleExecutionID: planner.ID,
		ProviderSessionID: plannerSession, RepositoryID: contract.TargetRepositories[0],
		PlanningRepositoryID: plannerGrant.RepositoryID,
		ProviderID: plannerStart.ProviderID, AccountID: plannerStart.AccountID,
		ConfigurationSHA256: plannerStart.ConfigurationSHA256,
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_store",
			SHA256: strings.Repeat("d", 64),
		},
		Budget:   domain.PlanningBudget{MaxTurns: 1, MaxTokens: 10},
		Deadline: contract.Deadline.Add(-time.Minute),
	}
	receipt, authorization, err := database.AddJobQuestionAuthorization(ctx, job.ID, job.Revision, request)
	if err != nil {
		t.Fatalf("AddJobQuestionAuthorization: %v", err)
	}
	if receipt.Revision != job.Revision+1 || authorization.QuestionMessageID != message.ID ||
		authorization.JobApprovalID != job.JobApprovals[len(job.JobApprovals)-1].ID {
		t.Fatalf("JobQuestionAuthorization receipt or bindings mismatch: %#v %#v", receipt, authorization)
	}
	restored, err := database.GetJob(ctx, job.ID)
	if err != nil || len(restored.JobQuestionAuthorizations) != 1 {
		t.Fatalf("GetJob after JobQuestionAuthorization: count=%d err=%v", len(restored.JobQuestionAuthorizations), err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatalf("restored Job.Validate: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close before restart: %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen Store: %v", err)
	}
	defer reopened.Close()
	restarted, err := reopened.GetJob(ctx, job.ID)
	if err != nil || len(restarted.JobQuestionAuthorizations) != 1 {
		t.Fatalf("GetJob after restart: count=%d err=%v", len(restarted.JobQuestionAuthorizations), err)
	}
	replay, replayed, err := reopened.AddJobQuestionAuthorization(ctx, restarted.ID, restarted.Revision, request)
	if err != nil || replay != receipt || replayed.ID != authorization.ID {
		t.Fatalf("JobQuestionAuthorization replay changed receipt or grant: %#v %#v %v", replay, replayed, err)
	}
	changed := request
	changed.ReadOnlyBoundary.SHA256 = strings.Repeat("f", 64)
	if _, _, err := reopened.AddJobQuestionAuthorization(ctx, restarted.ID, restarted.Revision, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed JobQuestionAuthorization payload returned %v, want ErrIdempotencyConflict", err)
	}
	_ = messageReceipt
}
