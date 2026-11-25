package domain

import (
	"strings"
	"testing"
	"time"
)

func TestJobQuestionAuthorizationBindsOneApprovedQuestionAndPlannerSession(t *testing.T) {
	job, task, now := approvedJobWithTaskForExecutionPermission(t)
	permissionRequest := executionPermissionTestRequest(job, now)
	permissionSHA, err := ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	permission, err := NewExecutionPermission(
		job, permissionRequest, permissionSHA,
		executionPermissionTestAssessment(permissionRequest, permissionSHA, now), now,
	)
	if err != nil {
		t.Fatalf("NewExecutionPermission: %v", err)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = now

	start, err := NewTaskRoleExecutionStart(
		job, task.ID, permission.ID, "cmd_71717171717171717171717171717171",
		permission.RequestSHA256, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("NewTaskRoleExecutionStart: %v", err)
	}
	job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
	job.Revision++
	job.UpdatedAt = start.RoleExecution.CreatedAt

	var planner RoleExecution
	for _, execution := range job.RoleExecutions {
		if execution.Role == RolePlanner {
			planner = execution
			break
		}
	}
	if planner.ID == "" {
		t.Fatal("approved Job fixture has no Planner RoleExecution")
	}
	message, err := NewJobMessage(job, "cmd_72727272727272727272727272727272", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorCoder, ID: start.RoleExecution.ID},
		Recipient: ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Kind:      MessageKindQuestion, Body: "Please clarify the approved implementation constraint.",
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewJobMessage: %v", err)
	}
	job.JobMessages = append(job.JobMessages, message)
	job.Revision++
	job.UpdatedAt = message.CreatedAt

	var planningGrant PlanningAuthorization
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID == planner.PlanningAuthorizationID {
			planningGrant = grant
			break
		}
	}
	if planningGrant.ID == "" {
		t.Fatal("Planner PlanningAuthorization was not found")
	}
	startObservation, found := latestProviderStartObservation(job, planner.ProviderStartOperationID)
	if !found || startObservation.Status != ProviderStartAccepted {
		t.Fatal("Planner does not have an accepted Provider Session")
	}
	contract := job.JobContractRevisions[len(job.JobContractRevisions)-1].Contract
	authorizationTime := now.Add(3 * time.Minute)
	request := JobQuestionAuthorizationRequest{
		JobID: job.ID, CommandID: "cmd_73737373737373737373737373737373",
		QuestionMessageID: message.ID, PlannerRoleExecutionID: planner.ID,
		ProviderSessionID: startObservation.ProviderSessionID,
		RepositoryID:      planningGrant.RepositoryID,
		ProviderID:        planningGrant.ProviderID, AccountID: planningGrant.AccountID,
		ConfigurationSHA256: planningGrant.ConfigurationSHA256,
		ReadOnlyBoundary: PlanningReadOnlyBoundary{
			Mode: PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1", SHA256: strings.Repeat("d", 64),
		},
		Budget:   PlanningBudget{MaxTurns: 1, MaxTokens: 100},
		Deadline: contract.Deadline.Add(-time.Minute),
	}
	authorization, err := NewJobQuestionAuthorization(job, request, authorizationTime)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorization: %v", err)
	}
	if authorization.JobApprovalID != job.JobApprovals[len(job.JobApprovals)-1].ID ||
		authorization.QuestionMessageID != message.ID ||
		authorization.PlannerRoleExecutionID != planner.ID ||
		authorization.ProviderSessionID != startObservation.ProviderSessionID {
		t.Fatalf("authorization lost its exact authority or question binding: %#v", authorization)
	}
	if err := authorization.ValidateFor(
		job, message.ID, planner.ID, startObservation.ProviderSessionID, authorizationTime.Add(time.Second),
	); err != nil {
		t.Fatalf("ValidateFor exact question and Session: %v", err)
	}
	if err := authorization.ValidateFor(
		job, message.ID, planner.ID, "session_foreign", authorizationTime.Add(time.Second),
	); err == nil {
		t.Fatal("JobQuestionAuthorization authorized a different Provider Session")
	}

	job.JobQuestionAuthorizations = append(job.JobQuestionAuthorizations, authorization)
	job.Revision++
	job.UpdatedAt = authorization.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with JobQuestionAuthorization: %v", err)
	}
}

func TestJobQuestionAuthorizationRejectsScopeAndBudgetExpansion(t *testing.T) {
	job, task, now := approvedJobWithTaskForExecutionPermission(t)
	permissionRequest := executionPermissionTestRequest(job, now)
	permissionSHA, err := ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	permission, err := NewExecutionPermission(
		job, permissionRequest, permissionSHA,
		executionPermissionTestAssessment(permissionRequest, permissionSHA, now), now,
	)
	if err != nil {
		t.Fatalf("NewExecutionPermission: %v", err)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = now
	start, err := NewTaskRoleExecutionStart(
		job, task.ID, permission.ID, "cmd_74747474747474747474747474747474",
		permission.RequestSHA256, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("NewTaskRoleExecutionStart: %v", err)
	}
	job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
	job.Revision++
	job.UpdatedAt = start.RoleExecution.CreatedAt
	planner := job.RoleExecutions[0]
	message, err := NewJobMessage(job, "cmd_75757575757575757575757575757575", JobMessageRequest{
		Sender: ActorRef{Kind: ActorCoder, ID: start.RoleExecution.ID},
		Recipient: ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Kind: MessageKindQuestion, Body: "Please clarify the approved implementation constraint.",
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewJobMessage: %v", err)
	}
	job.JobMessages = append(job.JobMessages, message)
	job.Revision++
	job.UpdatedAt = message.CreatedAt
	grant := job.PlanningAuthorizations[0]
	startObservation, _ := latestProviderStartObservation(job, planner.ProviderStartOperationID)
	contract := job.JobContractRevisions[len(job.JobContractRevisions)-1].Contract
	base := JobQuestionAuthorizationRequest{
		JobID: job.ID, CommandID: "cmd_76767676767676767676767676767676",
		QuestionMessageID: message.ID, PlannerRoleExecutionID: planner.ID,
		ProviderSessionID: startObservation.ProviderSessionID,
		RepositoryID:      grant.RepositoryID, ProviderID: grant.ProviderID,
		AccountID: grant.AccountID, ConfigurationSHA256: grant.ConfigurationSHA256,
		ReadOnlyBoundary: PlanningReadOnlyBoundary{
			Mode: PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_2", SHA256: strings.Repeat("d", 64),
		},
		Budget: PlanningBudget{MaxTurns: 1, MaxTokens: 100}, Deadline: contract.Deadline.Add(-time.Minute),
	}
	tests := []struct {
		name   string
		mutate func(*JobQuestionAuthorizationRequest)
	}{
		{name: "foreign repository", mutate: func(r *JobQuestionAuthorizationRequest) {
			r.RepositoryID = "https://github.com/other/project@" + strings.Repeat("b", 40)
		}},
		{name: "foreign Provider", mutate: func(r *JobQuestionAuthorizationRequest) { r.ProviderID = "provider.foreign" }},
		{name: "foreign session", mutate: func(r *JobQuestionAuthorizationRequest) { r.ProviderSessionID = "session_foreign" }},
		{name: "budget exceeds contract", mutate: func(r *JobQuestionAuthorizationRequest) { r.Budget.MaxTokens = contract.Budget.MaxTokens + 1 }},
		{name: "deadline exceeds contract", mutate: func(r *JobQuestionAuthorizationRequest) { r.Deadline = contract.Deadline.Add(time.Minute) }},
		{name: "mutable context", mutate: func(r *JobQuestionAuthorizationRequest) { r.ReadOnlyBoundary.Mode = "mutable" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.mutate(&request)
			if _, err := NewJobQuestionAuthorization(job, request, now.Add(3*time.Minute)); err == nil {
				t.Fatal("invalid JobQuestionAuthorization request was accepted")
			}
			if err := job.Validate(); err != nil {
				t.Fatalf("rejected request mutated Job: %v", err)
			}
		})
	}
}
